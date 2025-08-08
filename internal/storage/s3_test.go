package storage_test

import (
    "context"
    "strings"
    "testing"
    "time"

    "github.com/yourname/gotube/internal/config"
    "github.com/yourname/gotube/internal/storage"
)

func TestS3Presign(t *testing.T) {
    cfg := &config.Config{
        S3Endpoint: "localhost:9000",
        S3Region: "us-east-1",
        S3AccessKey: "minioadmin",
        S3SecretKey: "minioadmin",
        S3Bucket: "test-bucket",
        S3UseSSL: false,
    }
    s3, err := storage.NewS3Client(cfg)
    if err != nil {
        t.Skip("minio not available in CI:", err)
    }
    // upload small object
    body := strings.NewReader("hello")
    if err := s3.Put(context.Background(), "hello.txt", body, int64(body.Len()), "text/plain"); err != nil {
        t.Fatal(err)
    }
    u, err := s3.Presign(context.Background(), "hello.txt", 5*time.Minute)
    if err != nil {
        t.Fatal(err)
    }
    if u == "" {
        t.Fatal("empty url")
    }
}
