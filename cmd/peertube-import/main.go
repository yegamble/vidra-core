// Command peertube-import is the operator CLI for the one-way PeerTube → Vidra
// migration (fix_plan P18, .ralph/specs/peertube-import.md). It reads an existing
// PeerTube instance's PostgreSQL database + media storage (READ-ONLY on the
// source) and maps it into this Vidra instance.
//
// The DESTINATION (this Vidra instance) — its database, media storage backend,
// and the KEK used to seal imported actor keys — comes from the same environment
// the server reads (config.Load): DATABASE_URL, STORAGE_*, FEDERATION_KEY_KEK.
// The SOURCE comes from --source-* flags (an operator's shell, where holding the
// read-only source credentials is appropriate).
//
// Safety:
//   - Read-only on the source (the connection pins read-only transactions).
//   - Idempotent + resumable: a durable ledger skips already-imported rows, so a
//     re-run (or --resume after a crash) continues cleanly.
//   - --dry-run reports the plan and writes nothing.
//   - --force overrides the supported-version refusal. It exists for a HUMAN who
//     has verified compatibility by hand; automated agents MUST NOT pass it.
//
// Example:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	peertube-import \
//	  --source-dsn 'postgres://readonly:pw@oldhost:5432/peertube_prod?sslmode=disable' \
//	  --source-storage local --source-local-root /mnt/peertube-media \
//	  --conflict-policy skip --dry-run
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/peertubeimport"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
)

func main() {
	var (
		sourceDSN        = flag.String("source-dsn", "", "READ-ONLY DSN of the source PeerTube PostgreSQL (required)")
		sourceStorage    = flag.String("source-storage", "local", "source media storage backend: local | s3")
		sourceLocalRoot  = flag.String("source-local-root", "", "source media directory (read-only), for --source-storage=local")
		s3Endpoint       = flag.String("source-s3-endpoint", "", "source S3 endpoint host[:port] (no scheme), for --source-storage=s3")
		s3Bucket         = flag.String("source-s3-bucket", "", "source S3 bucket")
		s3AccessKey      = flag.String("source-s3-access-key", "", "source S3 access key (or PEERTUBE_SOURCE_S3_ACCESS_KEY)")
		s3SecretKey      = flag.String("source-s3-secret-key", "", "source S3 secret key (or PEERTUBE_SOURCE_S3_SECRET_KEY)")
		s3Region         = flag.String("source-s3-region", "", "source S3 region")
		s3UseSSL         = flag.Bool("source-s3-use-ssl", true, "source S3 uses https")
		s3ForcePathStyle = flag.Bool("source-s3-force-path-style", false, "source S3 path-style addressing (MinIO)")
		conflictPolicy   = flag.String("conflict-policy", "skip", "collision policy: skip | rename | merge | fail")
		dryRun           = flag.Bool("dry-run", false, "report the plan + conflicts and write NOTHING")
		resume           = flag.Bool("resume", false, "continue a prior import (default behaviour: already-imported rows are skipped)")
		force            = flag.Bool("force", false, "override the supported-version refusal (HUMAN operators only; agents MUST NOT set this)")
		noMedia          = flag.Bool("no-media", false, "import metadata only; do not copy media files")
	)
	flag.Parse()

	if *sourceDSN == "" {
		fatal("--source-dsn is required")
	}
	policy, err := peertubeimport.ParseConflictPolicy(*conflictPolicy)
	if err != nil {
		fatal(err.Error())
	}

	// Source S3 credentials may come from the environment so they are not exposed
	// in the process argument list.
	if *s3AccessKey == "" {
		*s3AccessKey = os.Getenv("PEERTUBE_SOURCE_S3_ACCESS_KEY")
	}
	if *s3SecretKey == "" {
		*s3SecretKey = os.Getenv("PEERTUBE_SOURCE_S3_SECRET_KEY")
	}

	// Destination config from the same environment the server reads.
	cfg, err := config.Load()
	if err != nil {
		fatal("load destination config: " + err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("connect destination database: " + err.Error())
	}
	defer db.Close()

	destMedia, err := buildDestMedia(ctx, cfg)
	if err != nil {
		fatal("open destination media storage: " + err.Error())
	}

	var srcMedia storage.Backend
	if !*noMedia {
		srcMedia, err = peertubeimport.OpenSourceStorage(peertubeimport.SourceStorageConfig{
			Backend:          *sourceStorage,
			LocalRoot:        *sourceLocalRoot,
			S3Endpoint:       *s3Endpoint,
			S3Bucket:         *s3Bucket,
			S3AccessKey:      *s3AccessKey,
			S3SecretKey:      *s3SecretKey,
			S3Region:         *s3Region,
			S3UseSSL:         *s3UseSSL,
			S3ForcePathStyle: *s3ForcePathStyle,
		})
		if err != nil {
			fatal("open source media storage: " + err.Error())
		}
	}

	src, err := peertubeimport.OpenSource(ctx, *sourceDSN)
	if err != nil {
		fatal(err.Error())
	}
	defer src.Close()

	// Actor-key sealer: reuse the federation KEK when configured so imported
	// account/channel private keys are sealed exactly like the server seals them.
	var sealKey func(string) (string, error)
	if cfg.FederationKeyKEK != "" {
		cipher, cerr := secretbox.NewCipherFromBase64(cfg.FederationKeyKEK)
		if cerr != nil {
			fatal("build key cipher: " + cerr.Error())
		}
		sealKey = func(pem string) (string, error) { return cipher.Seal([]byte(pem)) }
	}

	importer := peertubeimport.NewImporter(db.Pool, src, peertubeimport.Options{
		Policy:    policy,
		Force:     *force,
		SrcMedia:  srcMedia,
		DestMedia: destMedia,
		SealKey:   sealKey,
	})

	version, err := importer.Preflight(ctx)
	if err != nil {
		fatal(err.Error())
	}
	log.Printf("preflight OK: source PeerTube schema version %d (policy=%s, dry-run=%v, resume=%v)", version, policy, *dryRun, *resume)

	var report *peertubeimport.Report
	if *dryRun {
		report, err = importer.Plan(ctx, version)
	} else {
		start := time.Now()
		report, err = importer.Run(ctx, version, func(r *peertubeimport.Report) {
			log.Printf("progress: %s", r.Summary())
		})
		if err == nil {
			log.Printf("import finished in %s", time.Since(start).Round(time.Second))
		}
	}
	if err != nil {
		fatal(err.Error())
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))
}

// buildDestMedia builds the destination (this Vidra instance) media backend from
// config — the same selection cmd/api uses.
func buildDestMedia(ctx context.Context, cfg *config.Config) (storage.Backend, error) {
	switch cfg.StorageBackend {
	case "local":
		return storage.NewLocal(cfg.StorageLocalRoot)
	case "s3":
		b, err := storage.NewS3(storage.S3Config{
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
		if err := b.EnsureBucket(ctx); err != nil {
			return nil, err
		}
		return b, nil
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.StorageBackend)
	}
}

func fatal(msg string) {
	log.SetFlags(0)
	log.Fatalf("peertube-import: %s", msg)
}
