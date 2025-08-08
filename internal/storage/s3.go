package storage

import (
    "context"
    "io"
    "net/url"
    "time"

    "github.com/minio/minio-go/v7"
    "github.com/minio/minio-go/v7/pkg/credentials"
    "github.com/yegamble/athena/internal/config"
)

// Storage defines the minimal interface required by the HTTP API to store
// and retrieve video files.  Additional implementations (e.g. using S3
// compatible services) must satisfy this interface.
type Storage interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Presign(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// S3 implements the Storage interface using MinIO's S3 client.  It is
// compatible with AWS S3 and other S3‑like services.
type S3 struct {
    cli    *minio.Client
    bucket string
}

// NewS3Client constructs a new S3 storage client using the provided
// configuration.  It ensures the configured bucket exists, creating it if
// necessary.
func NewS3Client(cfg *config.Config) (*S3, error) {
    cli, err := minio.New(cfg.S3Endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
        Secure: cfg.S3UseSSL,
        Region: cfg.S3Region,
    })
    if err != nil {
        return nil, err
    }
    // ensure bucket exists
    ctx := context.Background()
    exists, err := cli.BucketExists(ctx, cfg.S3Bucket)
    if err != nil {
        return nil, err
    }
    if !exists {
        if err := cli.MakeBucket(ctx, cfg.S3Bucket, minio.MakeBucketOptions{Region: cfg.S3Region}); err != nil {
            return nil, err
        }
    }
    return &S3{cli: cli, bucket: cfg.S3Bucket}, nil
}

// Put uploads a new object to the configured bucket.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
    _, err := s.cli.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
    return err
}

// Get retrieves an object from the bucket for streaming or download.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
    return s.cli.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
}

// Presign generates a pre‑signed URL for downloading an object.  The caller
// can specify the expiry duration for the URL.
func (s *S3) Presign(ctx context.Context, key string, expiry time.Duration) (string, error) {
    reqParams := make(url.Values)
    presignedURL, err := s.cli.PresignedGetObject(ctx, s.bucket, key, expiry, reqParams)
    if err != nil {
        return "", err
    }
    return presignedURL.String(), nil
}