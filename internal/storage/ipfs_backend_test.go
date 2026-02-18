package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

	t.Run("Upload error from client when not running", func(t *testing.T) {
		err := backend.Upload(context.Background(), "k", strings.NewReader("data"), "text/plain")
		if err == nil {
			t.Fatal("expected error when IPFS not running, got nil")
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

	t.Run("Download error from client", func(t *testing.T) {
		body, err := backend.Download(context.Background(), "cid123")
		if err == nil {
			t.Fatal("expected error when IPFS not running, got nil")
		}
		if body != nil {
			t.Fatal("expected nil body on error")
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

	t.Run("Delete error from client when not running", func(t *testing.T) {
		if err := backend.Delete(context.Background(), "cid123"); err == nil {
			t.Fatal("expected delete error when IPFS not running, got nil")
		}
	})

	t.Run("Exists error from client when not running", func(t *testing.T) {
		exists, err := backend.Exists(context.Background(), "cid123")
		if err == nil {
			t.Fatal("expected exists error when IPFS not running, got nil")
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

func TestIPFSBackend_WithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v0/cat":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("file content here"))
		case "/api/v0/pin/rm":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Pins":["QmTestCID"]}`))
		case "/api/v0/pin/ls":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Keys":{"QmTestCID":{"Type":"recursive"}}}`))
		case "/api/v0/add":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Name":"data.bin","Hash":"QmNewCID","Size":"17"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := ipfs.NewClient(server.URL, "", time.Second)
	backend, err := NewIPFSBackend(IPFSConfig{Client: client, GatewayURL: "https://gateway.example.com"})
	if err != nil {
		t.Fatalf("NewIPFSBackend() error = %v", err)
	}

	t.Run("Upload via reader succeeds", func(t *testing.T) {
		err := backend.Upload(context.Background(), "key", strings.NewReader("some content"), "text/plain")
		if err != nil {
			t.Fatalf("Upload() error = %v", err)
		}
	})

	t.Run("Download returns content", func(t *testing.T) {
		rc, err := backend.Download(context.Background(), "QmTestCID")
		if err != nil {
			t.Fatalf("Download() error = %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(data) != "file content here" {
			t.Fatalf("Download() content = %q, want %q", string(data), "file content here")
		}
	})

	t.Run("Delete unpins CID", func(t *testing.T) {
		if err := backend.Delete(context.Background(), "QmTestCID"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("Exists returns true for pinned CID", func(t *testing.T) {
		exists, err := backend.Exists(context.Background(), "QmTestCID")
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if !exists {
			t.Fatal("Exists() = false, want true")
		}
	})
}
