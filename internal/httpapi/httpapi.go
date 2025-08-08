package httpapi

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    boxofiles "github.com/ipfs/boxo/files"
    "github.com/ipfs/kubo/client/rpc"
    "github.com/jmoiron/sqlx"
    "github.com/redis/go-redis/v9"
    "github.com/yegamble/athena/internal/config"
    "github.com/yegamble/athena/internal/storage"
)

// deps bundles together the various dependencies required by the HTTP handlers.
// Passing a single struct keeps handler signatures clean and makes it easy
// to extend the API with new services.
type deps struct {
    db   *sqlx.DB
    rdb  *redis.Client
    ipfs *rpc.HttpApi
    s3   storage.Storage
    cfg  *config.Config
}

// Mount registers all API routes under the given chi router.  Callers should
// provide the required dependencies which will be stored on a deps struct.
func Mount(r chi.Router, db *sqlx.DB, rdb *redis.Client, ipfs *rpc.HttpApi, s3 storage.Storage, cfg *config.Config) {
    d := &deps{db: db, rdb: rdb, ipfs: ipfs, s3: s3, cfg: cfg}
    r.Route("/api/v1", func(r chi.Router) {
        r.Post("/videos", d.uploadVideo)
        r.Get("/videos/{id}", d.getVideo)
    })
}

// Video represents a single uploaded video record.  It is stored in the
// database and returned in JSON responses.
type Video struct {
    ID        uuid.UUID `db:"id" json:"id"`
    Title     string    `db:"title" json:"title"`
    IPFSHash  *string   `db:"ipfs_hash" json:"ipfs_hash,omitempty"`
    S3Key     *string   `db:"s3_key" json:"s3_key,omitempty"`
    CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// uploadVideo handles POST /videos.  It accepts a multipart form with a file
// and optional title, uploads the file to IPFS (and falls back to S3 if
// necessary), persists a record in the database and returns the video
// metadata.
func (d *deps) uploadVideo(w http.ResponseWriter, r *http.Request) {
    // parse multipart
    if err := r.ParseMultipartForm(1 << 30); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    file, header, err := r.FormFile("file")
    if err != nil {
        http.Error(w, "file required", http.StatusBadRequest)
        return
    }
    defer file.Close()

    title := r.FormValue("title")
    if title == "" {
        title = header.Filename
    }

    // Try IPFS first
    ipfsCID, err := d.addToIPFS(r.Context(), file, header)
    var s3key *string
    var ipfsh *string
    if err == nil && ipfsCID != "" {
        ipfsh = &ipfsCID
    } else {
        // fallback to S3
        if _, err := file.Seek(0, io.SeekStart); err != nil {
            http.Error(w, "rewind failed", http.StatusInternalServerError)
            return
        }
        key := fmt.Sprintf("videos/%s", uuid.New().String())
        if err := d.s3.Put(r.Context(), key, file, header.Size, header.Header.Get("Content-Type")); err != nil {
            http.Error(w, "s3 upload failed: "+err.Error(), http.StatusBadGateway)
            return
        }
        s3key = &key
    }

    v := &Video{
        ID:        uuid.New(),
        Title:     title,
        IPFSHash:  ipfsh,
        S3Key:     s3key,
        CreatedAt: time.Now(),
    }
    // Persist (simplified for starter)
    if _, err := d.db.Exec(`INSERT INTO videos (id, title, ipfs_hash, s3_key, created_at) VALUES ($1,$2,$3,$4,$5)`,
        v.ID, v.Title, v.IPFSHash, v.S3Key, v.CreatedAt); err != nil {
        http.Error(w, "db insert failed: "+err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(v)
}

// addToIPFS streams the uploaded file into the configured IPFS client and
// returns the root CID when successful.
func (d *deps) addToIPFS(ctx context.Context, file multipart.File, header *multipart.FileHeader) (string, error) {
    f, err := boxofiles.NewReaderPathFile(header.Filename, file, nil)
    if err != nil {
        return "", err
    }
    res, err := d.ipfs.Unixfs().Add(ctx, f)
    if err != nil {
        return "", err
    }
    return res.RootCid().String(), nil
}

// getVideo handles GET /videos/{id}.  It returns the stored metadata and
// constructs a playback URL.  IPFS is preferred; otherwise a pre‑signed S3
// URL is returned.
func (d *deps) getVideo(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    var v Video
    if err := d.db.Get(&v, `SELECT id, title, ipfs_hash, s3_key, created_at FROM videos WHERE id = $1`, id); err != nil {
        http.Error(w, "not found", http.StatusNotFound)
        return
    }
    // response struct encapsulates metadata and streaming URL
    type Resp struct {
        Video  Video  `json:"video"`
        URL    string `json:"url"`
        Source string `json:"source"`
    }
    if v.IPFSHash != nil && *v.IPFSHash != "" {
        // NOTE: for production, run your own gateway or use delegated routing in the browser.
        url := fmt.Sprintf("ipfs://%s", *v.IPFSHash)
        json.NewEncoder(w).Encode(Resp{Video: v, URL: url, Source: "ipfs"})
        return
    }
    if v.S3Key != nil {
        u, err := d.s3.Presign(r.Context(), *v.S3Key, 15*time.Minute)
        if err != nil {
            http.Error(w, "presign failed: "+err.Error(), http.StatusInternalServerError)
            return
        }
        json.NewEncoder(w).Encode(Resp{Video: v, URL: u, Source: "s3"})
        return
    }
    http.Error(w, "no sources available", http.StatusBadGateway)
}