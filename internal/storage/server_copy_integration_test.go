//go:build integration

package storage

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestS3ServerCopyAcrossBuckets(t *testing.T) {
	source := newS3TestBackend(t)
	destination := &S3{client: source.client, bucket: fmt.Sprintf("vidra-copy-%d", time.Now().UnixNano()), region: source.region}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := destination.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := destination.client.RemoveBucket(context.Background(), destination.bucket); err != nil {
			t.Errorf("remove isolated copy bucket: %v", err)
		}
	})
	copier, err := NewS3Copier(destination, envOr("S3_TEST_ACCESS_KEY", "vidra"), envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, multipart := range []bool{false, true} {
		t.Run(fmt.Sprintf("multipart_%v", multipart), func(t *testing.T) {
			srcKey, dstKey := testKey(t, source, "source.mp4"), testKey(t, destination, "copied.mp4")
			body := strings.Repeat("unchanged source media\n", 300000)
			if _, err := source.client.PutObject(ctx, source.bucket, srcKey, strings.NewReader(body), int64(len(body)), minio.PutObjectOptions{
				ContentType: "text/html", UserMetadata: map[string]string{"untrusted": "discard"}, CacheControl: "private",
			}); err != nil {
				t.Fatal(err)
			}
			if multipart {
				info, err := source.client.StatObject(ctx, source.bucket, srcKey, minio.StatObjectOptions{})
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the real multipart protocol with a modest last part;
				// the fake server separately covers 4–16 GiB range boundaries.
				_, err = copier.(*s3Copier).copyMultipart(ctx, minio.CopySrcOptions{Bucket: source.bucket, Object: srcKey,
					VersionID: info.VersionID, MatchETag: strconv.Quote(info.ETag)}, dstKey, "video/mp4", info.Size)
				if err != nil {
					t.Fatal(err)
				}
			} else if n, err := copier.Copy(ctx, source, srcKey, dstKey, 16<<30); err != nil || n != int64(len(body)) {
				t.Fatalf("Copy = %d, %v", n, err)
			}
			info, err := destination.client.StatObject(ctx, destination.bucket, dstKey, minio.StatObjectOptions{})
			if err != nil || info.ContentType != "video/mp4" || info.Size != int64(len(body)) || len(info.UserMetadata) != 0 || info.Metadata.Get("Cache-Control") != "" {
				t.Fatalf("destination metadata differs: content type=%q size=%d metadata=%v error=%v", info.ContentType, info.Size, info.UserMetadata, err)
			}
			for _, object := range []struct {
				backend *S3
				key     string
			}{{source, srcKey}, {destination, dstKey}} {
				rc, err := object.backend.Open(ctx, object.key)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil || string(got) != body {
					t.Fatalf("source or copied bytes changed: %v", err)
				}
			}
		})
	}
}
