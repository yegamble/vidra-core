package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vidra/vidra-core/internal/blobverify"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
)

// The `verify-blobs` subcommand answers "does the object store still hold
// everything the database references?" — the check a restore has no other way
// to make (phase-2 storage, work item 7).
//
// It is a sibling of `migrate`: same binary, same published image, same
// environment. It runs on the **api** service rather than the migrate one-shot,
// and that is not interchangeable — the migrate service is given DATABASE_URL
// and nothing else, while this needs the whole STORAGE_* set to know which
// bucket or directory to ask. Hence:
//
//	docker compose run --rm api verify-blobs
//	docker compose run --rm api verify-blobs --hash --timeout=2h
//
// Exit codes, which are the contract deploy/restore.sh reads:
//
//	0  the database and the store agree
//	3  they do not — objects are missing, corrupt, or unreadable
//	1  the check could not be made at all (database or bucket unreachable)
//
// 3 rather than 1 for a finding, because a restore must be able to tell "I
// verified and it is wrong" from "I could not verify", and must continue in
// both cases: a restore that aborted here would leave the site down over a
// media problem that no amount of not-booting fixes.
const verifyBlobsUsage = "verify-blobs [--hash] [--deep] [--json] [--timeout <duration>]"

// exitInconsistent is the "verified, and it is wrong" exit code.
const exitInconsistent = 3

// defaultVerifyTimeout bounds a whole run. Generous on purpose: the fast pass
// is one HEAD request per referenced object and finishes in seconds on a real
// library, but --hash re-reads every byte of every original, and a check that
// gave up half way through a large library would be worse than useless — it
// would report missing objects it never asked about. Callers that know their
// library (deploy/restore.sh) pass a shorter one.
const defaultVerifyTimeout = time.Hour

// verifyBlobsJSON is the --json document: the report, flattened, plus the two
// facts that belong to the RUN rather than to the comparison.
type verifyBlobsJSON struct {
	blobverify.Report
	// StorageMigrationActive says a campaign was in flight while this ran, which
	// is the one condition under which a missing object is expected rather than
	// alarming (see the warning in runVerifyBlobs).
	StorageMigrationActive bool `json:"storage_migration_active"`
	// ExitCode is echoed so a consumer that captured stdout but lost the
	// process status still knows what this run concluded.
	ExitCode int `json:"exit_code"`
}

// runVerifyBlobs executes the subcommand and returns the process exit code. It
// returns a code rather than an error because this command has THREE outcomes
// and main's error path only knows about one.
func runVerifyBlobs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-blobs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: api %s\n\n", verifyBlobsUsage)
		fs.PrintDefaults()
	}
	var (
		hash    = fs.Bool("hash", false, "re-download every object whose row carries a content hash and compare (reads the whole library)")
		deep    = fs.Bool("deep", false, "enumerate every HLS tree through the storage backend instead of trusting its master manifest")
		asJSON  = fs.Bool("json", false, "print the report as one JSON document instead of a human summary")
		timeout = fs.Duration("timeout", defaultVerifyTimeout, "abandon the run after this long")
	)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "verify-blobs takes no positional arguments (got %q)\nusage: api %s\n", fs.Arg(0), verifyBlobsUsage)
		return 1
	}
	if *timeout <= 0 {
		fmt.Fprintf(stderr, "verify-blobs: --timeout must be positive\n")
		return 1
	}

	// SIGINT/SIGTERM cancel the run rather than killing it mid-report: an
	// operator who interrupts a two-hour --hash pass on a production bucket
	// should get the cancellation reported, not a truncated pipe.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	// The FULL config, unlike `migrate` — this command's whole job is to talk to
	// the store the deployment is configured for, so running it with a partial
	// environment would verify the wrong bucket, or a development default, and
	// report a healthy answer about a store nobody serves from.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "verify-blobs: configuration is not usable: %v\n", err)
		return 1
	}

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "verify-blobs: the database is unreachable: %v\n", err)
		return 1
	}
	defer db.Close()
	q := db.Queries()

	blobs, err := verifyBlobsBackend(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "verify-blobs: the media store is unreachable: %v\n", err)
		return 1
	}

	// A storage migration deliberately puts the two stores out of step: after
	// cutover the primary is the TARGET, and every object the copy workers have
	// not reached yet is genuinely absent from it. Saying so up front is the
	// difference between a report an operator acts on and one they panic at.
	migrating, merr := q.HasActiveStorageMigration(ctx)
	if merr != nil {
		// Not fatal. The comparison is still worth making; only its
		// interpretation is less certain, and the note says so.
		fmt.Fprintf(stderr, "verify-blobs: could not tell whether a storage migration is in flight (%v) — if one is, missing objects below may simply not have been copied yet\n", merr)
	}

	rep, err := blobverify.Verify(ctx, q, blobs, blobverify.Options{Hash: *hash, Deep: *deep})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "verify-blobs: gave up after %s — nothing was verified. Re-run with a longer --timeout\n", *timeout)
			return 1
		}
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "verify-blobs: interrupted — nothing was verified\n")
			return 1
		}
		fmt.Fprintf(stderr, "verify-blobs: %v\n", err)
		return 1
	}

	code := 0
	if !rep.Consistent() {
		code = exitInconsistent
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(verifyBlobsJSON{Report: rep, StorageMigrationActive: migrating, ExitCode: code}); err != nil {
			fmt.Fprintf(stderr, "verify-blobs: could not write the report: %v\n", err)
			return 1
		}
		return code
	}

	fmt.Fprintf(stdout, "%s: %s\n", storageLabel(cfg), storage.Describe(blobs))
	fmt.Fprint(stdout, rep.Text())
	if migrating {
		fmt.Fprint(stdout, "\nNOTE: a storage migration campaign is in flight. Until it finishes and the source is deleted, the two stores are deliberately out of step and an object reported missing here may simply not have been copied yet. See \"Moving the media store\" in docs/operations.md.\n")
	}
	return code
}

// storageLabel names the store in one word, so the first line of the report
// says WHICH store was asked. A verification of the wrong bucket that reports
// "all present" is the worst output this command could produce.
func storageLabel(cfg *config.Config) string {
	if cfg.StorageBackend == "local" {
		return "media store (local disk)"
	}
	return "media store (object storage)"
}

// verifyBlobsBackend builds the primary media backend for a READ-ONLY check.
//
// It deliberately does not go through buildStorageBackend: that one calls
// EnsureBucket, which CREATES the bucket when it is absent. For the server that
// is right — a first boot has to make its store. For a consistency check it is
// exactly wrong: a typo'd bucket name would be silently created, found empty,
// and reported as "every object is missing" instead of "that bucket does not
// exist", and the deployment would be left with a stray bucket it never wanted.
func verifyBlobsBackend(ctx context.Context, cfg *config.Config) (storage.Backend, error) {
	switch cfg.StorageBackend {
	case "local":
		return storage.NewLocal(cfg.StorageLocalRoot)
	case "s3":
		s3b, err := storage.NewS3(storage.S3Config{
			Endpoint:       cfg.StorageS3Endpoint,
			Bucket:         cfg.StorageS3Bucket,
			AccessKey:      cfg.StorageS3AccessKey,
			SecretKey:      cfg.StorageS3SecretKey,
			Region:         cfg.StorageS3Region,
			UseSSL:         cfg.StorageS3UseSSL,
			ForcePathStyle: cfg.StorageS3ForcePathStyle,
		})
		if err != nil {
			return nil, err
		}
		exists, err := s3b.BucketExists(ctx)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("bucket %q does not exist (this check never creates one)", cfg.StorageS3Bucket)
		}
		return s3b, nil
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}
