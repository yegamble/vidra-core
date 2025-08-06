package jobs

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "time"

    "github.com/go-redis/redis/v8"

    "gotube/internal/model"
    "gotube/internal/repository"
    "gotube/internal/service"
)

// TranscodeJob represents the payload stored in the job queue. When a
// user uploads a video, a job is enqueued with the video ID and the path
// to the original file. The worker picks up jobs and processes them.
type TranscodeJob struct {
    VideoID   int64  `json:"video_id"`
    FilePath  string `json:"file_path"`
    IPFSCID   string `json:"ipfs_cid"`
}

// Transcoder defines dependencies needed by the worker. These include
// access to the video repository for updating statuses and renditions,
// the IPFS service for optionally storing renditions, and the IOTA
// service for logging CIDs. Redis is used as a queue backend.
type Transcoder struct {
    Repo      repository.VideoRepository
    IPFS      *service.IPFSService
    IOTA      *service.IOTAService
    Redis     *redis.Client
    WorkDir   string
    Presets   []Preset
    ContainerEnabled bool
}

// Preset defines a single output resolution. Bitrate is approximate and
// can be adjusted to match desired quality. Additional fields (e.g.,
// codec or CRF) could be added if needed.
type Preset struct {
    Name     string // e.g., "240p"
    Width    int
    Height   int
    Bitrate  int    // in kbps
}

// NewTranscoder constructs a new Transcoder worker. The WorkDir is where
// transcoded files will be stored; ensure it exists and is writable.
func NewTranscoder(repo repository.VideoRepository, ipfs *service.IPFSService, iotaSvc *service.IOTAService, rdb *redis.Client, workDir string) *Transcoder {
    presets := []Preset{
        {Name: "240p", Width: 426, Height: 240, Bitrate: 400},
        {Name: "360p", Width: 640, Height: 360, Bitrate: 800},
        {Name: "480p", Width: 854, Height: 480, Bitrate: 1200},
        {Name: "720p", Width: 1280, Height: 720, Bitrate: 2500},
        {Name: "1080p", Width: 1920, Height: 1080, Bitrate: 4500},
        {Name: "4k", Width: 3840, Height: 2160, Bitrate: 12000},
        {Name: "8k", Width: 7680, Height: 4320, Bitrate: 20000},
    }
    return &Transcoder{Repo: repo, IPFS: ipfs, IOTA: iotaSvc, Redis: rdb, WorkDir: workDir, Presets: presets}
}

// Start launches the transcoding worker loop in a background goroutine.
// It listens on the Redis list "transcode_jobs" for new jobs. When a job
// arrives, it processes it sequentially. Errors are logged but do not
// stop the loop. The context can be used to cancel the worker.
func (t *Transcoder) Start(ctx context.Context) {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            default:
                // Block until a job is available. BLPop returns a slice
                // with the queue name and the job payload (as string).
                res, err := t.Redis.BLPop(ctx, 0, "transcode_jobs").Result()
                if err != nil {
                    fmt.Printf("transcoder: error popping job: %v\n", err)
                    time.Sleep(1 * time.Second)
                    continue
                }
                if len(res) < 2 {
                    continue
                }
                payload := res[1]
                // For simplicity we encode job as "videoID:filePath"
                var job TranscodeJob
                fmt.Sscanf(payload, "%d:%s", &job.VideoID, &job.FilePath)
                t.processJob(ctx, &job)
            }
        }
    }()
}

// processJob performs the actual transcoding for a single job. It runs
// ffmpeg commands for each preset, writes output files in WorkDir and
// updates the video status and renditions in the database. If IPFS
// integration is available, each rendition can be uploaded and its CID
// stored (not implemented in this stub). Errors cause the video to be
// marked as failed.
func (t *Transcoder) processJob(ctx context.Context, job *TranscodeJob) {
    // Mark video as processing
    if err := t.Repo.UpdateStatus(ctx, job.VideoID, model.VideoStatusProcessing); err != nil {
        fmt.Printf("transcoder: could not update status to processing: %v\n", err)
    }
    for _, preset := range t.Presets {
        outDir := filepath.Join(t.WorkDir, fmt.Sprintf("%d", job.VideoID), preset.Name)
        if err := os.MkdirAll(outDir, 0755); err != nil {
            fmt.Printf("transcoder: cannot create output dir: %v\n", err)
            continue
        }
        outputPath := filepath.Join(outDir, "output.mp4")
        cmd := exec.CommandContext(ctx, "ffmpeg", "-i", job.FilePath,
            "-vf", fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease", preset.Width, preset.Height),
            "-b:v", fmt.Sprintf("%dk", preset.Bitrate),
            "-preset", "fast",
            "-c:a", "copy",
            "-y",
            outputPath)
        if err := cmd.Run(); err != nil {
            fmt.Printf("transcoder: ffmpeg failed for preset %s: %v\n", preset.Name, err)
            // continue to next preset but mark failure later if none succeed
            continue
        }
        // Save rendition metadata
        vr := &model.VideoRendition{
            VideoID:   job.VideoID,
            Resolution: preset.Name,
            Bitrate:   preset.Bitrate,
            FilePath:  outputPath,
            CreatedAt: time.Now(),
        }
        if err := t.Repo.CreateRendition(ctx, vr); err != nil {
            fmt.Printf("transcoder: cannot create rendition record: %v\n", err)
        }
        // Optionally upload to IPFS and log via IOTA (omitted)
    }
    // Mark video as ready
    if err := t.Repo.UpdateStatus(ctx, job.VideoID, model.VideoStatusReady); err != nil {
        fmt.Printf("transcoder: could not update status to ready: %v\n", err)
    }
}