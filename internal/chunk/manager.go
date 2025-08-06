package chunk

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "sync"
    "time"

    "github.com/go-redis/redis/v8"
)

// ChunkedUploadManager manages chunked uploads for large files. It stores
// received chunks on disk and merges them once all parts are uploaded.
// Redis is used to track upload progress and coordinate merges.
type ChunkedUploadManager struct {
    Redis       *redis.Client
    TempDir     string
    MaxChunkSize int
    MaxMemory   int64
    mu          sync.Mutex
}

// NewChunkedUploadManager creates a new manager. tempDir is where
// temporary chunks are stored. maxChunkSize defines the maximum size
// accepted per chunk (in bytes). maxMemory sets the maximum memory
// allocated by ParseMultipartForm.
func NewChunkedUploadManager(rdb *redis.Client, tempDir string, maxChunkSize int, maxMemory int64) *ChunkedUploadManager {
    return &ChunkedUploadManager{
        Redis:       rdb,
        TempDir:     tempDir,
        MaxChunkSize: maxChunkSize,
        MaxMemory:   maxMemory,
    }
}

// HandleChunkUpload accepts a single chunk and writes it to disk. It reads
// upload_id, chunk_number and total_chunks from the form fields and
// updates upload progress in Redis. When all chunks are present it
// triggers a merge asynchronously.
func (cum *ChunkedUploadManager) HandleChunkUpload(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseMultipartForm(cum.MaxMemory); err != nil {
        http.Error(w, "failed to parse form", http.StatusBadRequest)
        return
    }
    uploadID := r.FormValue("upload_id")
    chunkNumber := r.FormValue("chunk_number")
    totalChunks := r.FormValue("total_chunks")
    if uploadID == "" || chunkNumber == "" || totalChunks == "" {
        http.Error(w, "missing upload_id or chunk parameters", http.StatusBadRequest)
        return
    }
    file, _, err := r.FormFile("chunk")
    if err != nil {
        http.Error(w, "failed to get chunk", http.StatusBadRequest)
        return
    }
    defer file.Close()
    chunkPath := filepath.Join(cum.TempDir, fmt.Sprintf("%s_chunk_%s", uploadID, chunkNumber))
    if err := cum.saveChunkToFile(file, chunkPath); err != nil {
        http.Error(w, "failed to save chunk", http.StatusInternalServerError)
        return
    }
    // update progress in redis
    cum.updateUploadProgress(r.Context(), uploadID, chunkNumber, totalChunks)
    // check if all chunks received
    if cum.allChunksReceived(r.Context(), uploadID, totalChunks) {
        go cum.mergeChunksAsync(uploadID, totalChunks)
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"status": "chunk_received", "chunk": chunkNumber})
}

// saveChunkToFile writes a chunk to a temp file.
func (cum *ChunkedUploadManager) saveChunkToFile(file io.Reader, path string) error {
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        return err
    }
    out, err := os.Create(path)
    if err != nil {
        return err
    }
    defer out.Close()
    _, err = io.Copy(out, file)
    return err
}

// updateUploadProgress increments the count of received chunks.
func (cum *ChunkedUploadManager) updateUploadProgress(ctx context.Context, uploadID, chunkNumber, totalChunks string) {
    if cum.Redis == nil {
        return
    }
    // Use a Redis set to track which chunk numbers have been uploaded
    key := fmt.Sprintf("upload:%s:chunks", uploadID)
    cum.Redis.SAdd(ctx, key, chunkNumber)
    // Set TTL for cleanup
    cum.Redis.Expire(ctx, key, time.Hour)
}

// allChunksReceived checks whether the number of chunks received matches totalChunks.
func (cum *ChunkedUploadManager) allChunksReceived(ctx context.Context, uploadID, totalChunks string) bool {
    if cum.Redis == nil {
        return false
    }
    key := fmt.Sprintf("upload:%s:chunks", uploadID)
    total, _ := strconv.Atoi(totalChunks)
    count, err := cum.Redis.SCard(ctx, key).Result()
    if err != nil {
        return false
    }
    return int(count) >= total
}

// mergeChunksAsync merges all chunks into a single file. It collects chunk
// files, sorts them numerically and concatenates them. After merging,
// it could trigger further processing (e.g. transcoding). It also
// cleans up chunk files.
func (cum *ChunkedUploadManager) mergeChunksAsync(uploadID, totalChunks string) {
    cum.mu.Lock()
    defer cum.mu.Unlock()
    total, _ := strconv.Atoi(totalChunks)
    mergedPath := filepath.Join(cum.TempDir, fmt.Sprintf("%s_merged", uploadID))
    mergedFile, err := os.Create(mergedPath)
    if err != nil {
        fmt.Printf("merge error: %v\n", err)
        return
    }
    defer mergedFile.Close()
    for i := 1; i <= total; i++ {
        chunkPath := filepath.Join(cum.TempDir, fmt.Sprintf("%s_chunk_%d", uploadID, i))
        chunk, err := os.Open(chunkPath)
        if err != nil {
            fmt.Printf("open chunk %s: %v\n", chunkPath, err)
            return
        }
        io.Copy(mergedFile, chunk)
        chunk.Close()
        os.Remove(chunkPath)
    }
    // TODO: notify video processing pipeline with mergedPath
    fmt.Printf("Merged file created: %s\n", mergedPath)
    // Clean up Redis keys
    if cum.Redis != nil {
        ctx := context.Background()
        key := fmt.Sprintf("upload:%s:chunks", uploadID)
        cum.Redis.Del(ctx, key)
    }
}