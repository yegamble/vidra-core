package storage

import (
    "context"
    "io"
    "net/url"
    "time"

    "github.com/minio/minio-go/v7"
    "github.com/minio/minio-go/v7/pkg/credentials"
    "github.com/yourname/gotube/internal/config"
)

type Storage interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Presign(ctx context.Context, key string, expiry time.Duration) (string, error)
}

type S3 struct {
    cli    *minio.Client
    bucket string
}

func NewS3Client(cfg *config.Config) (*S3, error) {
    // endpoint can be "s3.amazonaws.com" or "nyc3.digitaloceanspaces.com" or "s3.us-west-2.amazonaws.com" or "localhost:9000" for MinIO
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

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
    _, err := s.cli.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
    return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
    return s.cli.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
}

func (s *S3) Presign(ctx context.Context, key string, expiry time.Duration) (string, error) {
    reqParams := make(url.Values)
    presignedURL, err := s.cli.PresignedGetObject(ctx, s.bucket, key, expiry, reqParams)
    if err != nil {
        return "", err
    }
    return presignedURL.String(), nil
}
