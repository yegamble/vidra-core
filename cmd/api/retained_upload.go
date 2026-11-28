package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/gommon/bytes"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

const retainedUsage = "recover-scan-upload --upload UUID --owner UUID --privacy VISIBILITY --sha256 HEX [--retry-job UUID] [--apply]"

func runRetainedUpload(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("recover-scan-upload", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("upload", "", "failed upload session UUID")
	owner := flags.String("owner", "", "expected creator UUID")
	privacy := flags.String("privacy", "", "expected current visibility")
	hash := flags.String("sha256", "", "recorded original SHA-256")
	prior := flags.String("retry-job", "", "retry one specific enrollment previously declined for eligibility")
	apply := flags.Bool("apply", false, "queue the verified source for normal processing")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	uploadID, uerr := uuid.Parse(*id)
	ownerID, oerr := uuid.Parse(*owner)
	digest, herr := hex.DecodeString(*hash)
	var retryJob uuid.UUID
	if *prior != "" {
		var err error
		if retryJob, err = uuid.Parse(*prior); err != nil || retryJob == uuid.Nil {
			fmt.Fprintln(stderr, retainedUsage)
			return 2
		}
	}
	if flags.NArg() != 0 || uerr != nil || oerr != nil || uploadID == uuid.Nil || ownerID == uuid.Nil || *privacy == "" || herr != nil || len(digest) != 32 {
		fmt.Fprintln(stderr, retainedUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil || cfg.ClamAVAddr == "" || cfg.MalwareScanMode != "fail-closed" {
		fmt.Fprintln(stderr, "A configured fail-closed scanner is required.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, "The database could not be opened.")
		return 1
	}
	defer db.Close()
	bootMax, _ := bytes.Parse(cfg.UploadMaxSize)
	settings := instancesettings.NewService(db.Queries(), instancesettings.Defaults{UploadsEnabled: cfg.UploadsEnabled, UploadMaxSizeBytes: bootMax})
	if err := settings.Load(ctx); err != nil || !settings.Bool(instancesettings.KeyUploadsEnabled) {
		fmt.Fprintln(stderr, "Uploads must be enabled before a retained source can be recovered.")
		return 1
	}
	blobs, err := verifyBlobsBackend(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "The configured media store is unavailable.")
		return 1
	}
	stat, ok := blobs.(storage.ObjectStatter)
	if !ok {
		fmt.Fprintln(stderr, "The configured store cannot check the retained object.")
		return 1
	}
	result, err := queueRetainedUpload(ctx, db, stat, sqlcgen.GetRetainedScanSourceParams{
		UploadID: uploadID, OwnerID: ownerID, Privacy: *privacy, Sha256: hex.EncodeToString(digest),
	}, *apply, settings.Int(instancesettings.KeyUploadMaxSizeBytes), retryJob)
	if err != nil {
		fmt.Fprintln(stderr, "The upload is ineligible or busy. No recovery was queued by this attempt.")
		return 1
	}
	fmt.Fprintln(stdout, result)
	return 0
}

func queueRetainedUpload(ctx context.Context, db *store.Store, blobs storage.ObjectStatter, p sqlcgen.GetRetainedScanSourceParams, apply bool, maxSize int64, retryJob uuid.UUID) (string, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.Queries().WithTx(tx)
	if _, err = q.LockRetainedUpload(ctx, p.UploadID); err != nil {
		return "", err
	}
	record, err := q.GetRecordedScanRecovery(ctx, sqlcgen.GetRecordedScanRecoveryParams(p))
	if retryJob != uuid.Nil {
		if err != nil || record.ID != retryJob {
			return "", errors.New("recovery job does not match this enrollment")
		}
		if record.State != "failed" {
			return "previously queued, current job state " + record.State, nil
		}
		if _, err := q.LockDeclinedRetainedScanRecovery(ctx, sqlcgen.LockDeclinedRetainedScanRecoveryParams{
			JobID: retryJob, DeclinedReason: video.RetainedRecoveryDeclined,
		}); err != nil {
			return "", err
		}
	} else if err == nil {
		return "previously queued, current job state " + record.State, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	src, err := q.GetRetainedScanSource(ctx, p)
	if err != nil {
		return "", err
	}
	if maxSize > 0 && src.SizeBytes > maxSize {
		return "", errors.New("the original exceeds the current upload limit")
	}
	size, exists, err := blobs.Stat(ctx, src.StorageKey)
	if err != nil || !exists || size != src.SizeBytes {
		return "", errors.New("the stored source is unavailable or has changed")
	}
	if !apply {
		if retryJob != uuid.Nil {
			return fmt.Sprintf("eligible previously declined job, %d bytes, dry run", src.SizeBytes), nil
		}
		return fmt.Sprintf("eligible, %d bytes, dry run", src.SizeBytes), nil
	}
	jobID := retryJob
	reason := "operator requested a full rescan of an existing failed upload"
	if retryJob != uuid.Nil {
		updated, err := q.RequeueDeclinedRetainedScanRecovery(ctx, sqlcgen.RequeueDeclinedRetainedScanRecoveryParams{JobID: retryJob, DeclinedReason: video.RetainedRecoveryDeclined})
		if err != nil {
			return "", err
		}
		if updated != 1 {
			return "", errors.New("recovery job changed")
		}
		reason = "operator retried the first recovery attempt after its terminal eligibility refusal"
	} else {
		job, err := q.EnqueueUploadFinalizeJob(ctx, sqlcgen.EnqueueUploadFinalizeJobParams{
			UploadID: src.UploadID, VideoID: src.VideoID, Purpose: "upload",
		})
		if err != nil {
			return "", err
		}
		err = q.RecordRetainedScanRecovery(ctx, sqlcgen.RecordRetainedScanRecoveryParams{
			JobID: job.ID, UploadID: src.UploadID, FileID: src.FileID, OwnerID: src.OwnerID,
			ChannelID: src.ChannelID, Privacy: src.Privacy, Sha256: src.Sha256, SizeBytes: src.SizeBytes,
		})
		if err != nil {
			return "", err
		}
		jobID = job.ID
	}
	n, err := q.MarkRetainedUploadQueued(ctx, src.UploadID)
	if err != nil || n != 1 {
		return "", errors.New("upload changed")
	}
	err = audit.NewService(q).Record(ctx, audit.Event{
		Actor: audit.ActorSnapshot{Kind: "system"}, Action: observability.ActionUploadScanRecovery,
		Result: observability.ResultSuccess, ResourceType: "video", ResourceID: src.VideoID.String(),
		JobID: jobID, Reason: reason,
	})
	if err != nil {
		return "", err
	}
	return "queued for full processing", tx.Commit(ctx)
}
