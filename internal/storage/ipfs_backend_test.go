package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"athena/internal/ipfs"
)

func TestNewIPFSBackend(t *testing.T) {
	t.Run("missing client", func(t *testing.T) {
		backend, err := NewIPFSBackend(IPFSConfig{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if backend != nil {
			t.Fatal("expected nil backend on error")
		}
		if !strings.Contains(err.Error(), "IPFS client is required") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("default gateway", func(t *testing.T) {
		client := ipfs.NewClient("http://127.0.0.1:5001", "", time.Second)
		backend, err := NewIPFSBackend(IPFSConfig{Client: client})
		if err != nil {
			t.Fatalf("NewIPFSBackend() error = %v", err)
		}
		if backend.gatewayURL != "https://ipfs.io" {
			t.Fatalf("gatewayURL = %q, want %q", backend.gatewayURL, "https://ipfs.io")
		}
	})

	t.Run("custom gateway", func(t *testing.T) {
		client := ipfs.NewClient("http://127.0.0.1:5001", "", time.Second)
		backend, err := NewIPFSBackend(IPFSConfig{
			Client:     client,
			GatewayURL: "https://gateway.example.com",
		})
		if err != nil {
			t.Fatalf("NewIPFSBackend() error = %v", err)
		}
		if backend.gatewayURL != "https://gateway.example.com" {
			t.Fatalf("gatewayURL = %q", backend.gatewayURL)
		}
	})
}

func TestIPFSBackend_Methods(t *testing.T) {
	client := ipfs.NewClient("http://127.0.0.1:5001", "", 100*time.Millisecond)
	backend, err := NewIPFSBackend(IPFSConfig{Client: client, GatewayURL: "https://gateway.example.com"})
	if err != nil {
		t.Fatalf("NewIPFSBackend() error = %v", err)
	}

	t.Run("Upload unsupported", func(t *testing.T) {
		err := backend.Upload(context.Background(), "k", strings.NewReader("data"), "text/plain")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not supported for IPFS") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("UploadFile error wraps client failure", func(t *testing.T) {
		err := backend.UploadFile(context.Background(), "k", "/path/does/not/exist.mp4", "video/mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to upload file to IPFS") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Download not implemented", func(t *testing.T) {
		body, err := backend.Download(context.Background(), "cid123")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if body != nil {
			t.Fatal("expected nil body on error")
		}
		if !strings.Contains(err.Error(), "download not implemented") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("GetURL and GetSignedURL", func(t *testing.T) {
		gotURL := backend.GetURL("cid123")
		if gotURL != "https://gateway.example.com/ipfs/cid123" {
			t.Fatalf("GetURL() = %q", gotURL)
		}

		signedURL, err := backend.GetSignedURL(context.Background(), "cid123", time.Minute)
		if err != nil {
			t.Fatalf("GetSignedURL() error = %v", err)
		}
		if signedURL != gotURL {
			t.Fatalf("GetSignedURL() = %q, want %q", signedURL, gotURL)
		}
	})

	t.Run("Delete and Exists stubs", func(t *testing.T) {
		if err := backend.Delete(context.Background(), "cid123"); err == nil {
			t.Fatal("expected delete error, got nil")
		}

		exists, err := backend.Exists(context.Background(), "cid123")
		if err == nil {
			t.Fatal("expected exists error, got nil")
		}
		if exists {
			t.Fatal("exists = true, want false")
		}
	})

	t.Run("Copy no-op", func(t *testing.T) {
		if err := backend.Copy(context.Background(), "src", "dst"); err != nil {
			t.Fatalf("Copy() error = %v", err)
		}
	})

	t.Run("GetMetadata returns CID-backed defaults", func(t *testing.T) {
		meta, err := backend.GetMetadata(context.Background(), "cid123")
		if err != nil {
			t.Fatalf("GetMetadata() error = %v", err)
		}
		if meta.Key != "cid123" {
			t.Fatalf("meta.Key = %q", meta.Key)
		}
		if meta.ETag != "cid123" {
			t.Fatalf("meta.ETag = %q", meta.ETag)
		}
		if meta.ContentType != "application/octet-stream" {
			t.Fatalf("meta.ContentType = %q", meta.ContentType)
		}
		if meta.LastModified.IsZero() {
			t.Fatal("meta.LastModified is zero")
		}
	})
}
