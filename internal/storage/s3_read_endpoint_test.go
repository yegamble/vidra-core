package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestS3ReadEndpoint(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "proxy"}[proxy], func(t *testing.T) {
			var reads, writes, ranges atomic.Int32
			read := func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=test-access/") {
					t.Error("read was not signed")
				}
				if r.URL.Path != "/vidra-media/media.mp4" {
					t.Errorf("unexpected object path: %s", r.URL.Path)
				}
				if r.Header.Get("Range") != "" {
					ranges.Add(1)
				}
				w.Header().Set("ETag", `"test-etag"`)
				http.ServeContent(w, r, "media.mp4", time.Unix(1000, 0), strings.NewReader("0123456789"))
			}
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut || r.Method == http.MethodDelete {
					writes.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("ETag", `"test-etag"`)
					if r.Method == http.MethodDelete {
						w.WriteHeader(http.StatusNoContent)
					}
					return
				}
				if proxy {
					t.Error("object read bypassed configured proxy")
				}
				read(w, r)
			}))
			defer origin.Close()
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					t.Error("write reached read proxy")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				read(w, r)
			}))
			defer edge.Close()
			cfg := validS3Config()
			cfg.Endpoint, cfg.Region = strings.TrimPrefix(origin.URL, "http://"), "us-east-1"
			if proxy {
				cfg.ReadEndpoint = strings.TrimPrefix(edge.URL, "http://")
			}
			store, err := NewS3(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := store.Put(ctx, "media.mp4", strings.NewReader("0123456789")); err != nil {
				t.Fatal(err)
			}
			obj, err := store.Open(ctx, "media.mp4")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = obj.Close() }()
			if _, err := obj.(io.Seeker).Seek(4, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(obj)
			if err != nil || string(data) != "456789" {
				t.Fatalf("ranged read = %q, %v", data, err)
			}
			if err := store.Delete(ctx, "media.mp4"); err != nil {
				t.Fatal(err)
			}
			if reads.Load() < 2 || ranges.Load() == 0 || writes.Load() != 2 {
				t.Fatalf("reads=%d ranges=%d writes=%d", reads.Load(), ranges.Load(), writes.Load())
			}
		})
	}
}

func TestS3ReadEndpointPreservesServerCopy(t *testing.T) {
	f := newCopyProtocol(t, 10)
	cfg := validS3Config()
	cfg.Endpoint, cfg.Region, cfg.Bucket = f.destination.client.EndpointURL().Host, "us-east-1", "destination-bucket"
	cfg.AccessKey = "destination-bucket"
	cfg.ReadEndpoint = "127.0.0.1:1" // Copy must never contact this unavailable read proxy.
	destination, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	copier, err := NewS3Copier(destination, "copy-access", "test-copy-secret")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 10); n != 10 || err != nil {
		t.Fatalf("server copy = %d, %v", n, err)
	}
}
