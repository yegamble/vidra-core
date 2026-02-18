package ipfs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:5001", "http://localhost:9094", 30*time.Second)
	if client == nil {
		t.Fatal("expected client to be created")
	}

	if !client.IsEnabled() {
		t.Error("expected IPFS to be enabled")
	}
}

func TestNewClient_Disabled(t *testing.T) {
	client := NewClient("", "", 30*time.Second)
	if client == nil {
		t.Fatal("expected client to be created")
	}

	if client.IsEnabled() {
		t.Error("expected IPFS to be disabled")
	}
}

func TestClient_AddFile_Disabled(t *testing.T) {
	client := NewClient("", "", 30*time.Second)
	ctx := context.Background()

	_, err := client.AddFile(ctx, "/tmp/test.txt")
	if err == nil {
		t.Error("expected error when IPFS is disabled")
	}
}

func TestClient_AddDirectory_Disabled(t *testing.T) {
	client := NewClient("", "", 30*time.Second)
	ctx := context.Background()

	_, err := client.AddDirectory(ctx, "/tmp/testdir")
	if err == nil {
		t.Error("expected error when IPFS is disabled")
	}
}

func TestClient_AddDirectory_NotADirectory(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	client := NewClient("http://localhost:5001", "", 30*time.Second)
	ctx := context.Background()

	_, err = client.AddDirectory(ctx, tmpFile.Name())
	if err == nil {
		t.Error("expected error when path is not a directory")
	}
}

func TestClient_AddDirectory_NonExistent(t *testing.T) {
	client := NewClient("http://localhost:5001", "", 30*time.Second)
	ctx := context.Background()

	_, err := client.AddDirectory(ctx, "/nonexistent/directory")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestClient_AddFile_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello IPFS!")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	client := NewClient("http://localhost:5001", "", 60*time.Second)
	ctx := context.Background()

	cid, err := client.AddFile(ctx, testFile)
	if err != nil {
		t.Skipf("IPFS not available: %v", err)
	}

	if cid == "" {
		t.Error("expected CID to be returned")
	}

	t.Logf("Uploaded file with CID: %s", cid)
}

func TestClient_AddDirectory_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"file1.txt":        "Content 1",
		"file2.txt":        "Content 2",
		"subdir/file3.txt": "Content 3",
	}

	for name, content := range files {
		filePath := filepath.Join(testDir, name)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	client := NewClient("http://localhost:5001", "", 60*time.Second)
	ctx := context.Background()

	cid, err := client.AddDirectory(ctx, testDir)
	if err != nil {
		t.Skipf("IPFS not available: %v", err)
	}

	if cid == "" {
		t.Error("expected CID to be returned")
	}

	t.Logf("Uploaded directory with CID: %s", cid)
}

func TestClient_Cat_Disabled(t *testing.T) {
	client := NewClient("", "", time.Second)
	_, err := client.Cat(context.Background(), "QmTest")
	if err == nil {
		t.Fatal("expected error when IPFS is disabled")
	}
	if !strings.Contains(err.Error(), "IPFS not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_Cat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/cat" && r.URL.Query().Get("arg") == "QmTestCID" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello ipfs content"))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	rc, err := client.Cat(context.Background(), "QmTestCID")
	if err != nil {
		t.Fatalf("Cat() error = %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "hello ipfs content" {
		t.Fatalf("Cat() content = %q, want %q", string(data), "hello ipfs content")
	}
}

func TestClient_Cat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("dag node not found"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	_, err := client.Cat(context.Background(), "QmNotFound")
	if err == nil {
		t.Fatal("expected error on server error")
	}
}

func TestClient_Unpin_Disabled(t *testing.T) {
	client := NewClient("", "", time.Second)
	err := client.Unpin(context.Background(), "QmTest")
	if err == nil {
		t.Fatal("expected error when IPFS is disabled")
	}
	if !strings.Contains(err.Error(), "IPFS not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_Unpin_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/pin/rm" && r.URL.Query().Get("arg") == "QmTestCID" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Pins":["QmTestCID"]}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	if err := client.Unpin(context.Background(), "QmTestCID"); err != nil {
		t.Fatalf("Unpin() error = %v", err)
	}
}

func TestClient_Unpin_NotPinned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`not pinned or pinned indirectly`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	err := client.Unpin(context.Background(), "QmNotPinned")
	if err != nil {
		t.Fatalf("Unpin() should succeed for not-pinned CID, got error: %v", err)
	}
}

func TestClient_IsPinned_Disabled(t *testing.T) {
	client := NewClient("", "", time.Second)
	pinned, err := client.IsPinned(context.Background(), "QmTest")
	if err == nil {
		t.Fatal("expected error when IPFS is disabled")
	}
	if !strings.Contains(err.Error(), "IPFS not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
	if pinned {
		t.Fatal("expected pinned=false when disabled")
	}
}

func TestClient_IsPinned_True(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/pin/ls" && r.URL.Query().Get("arg") == "QmTestCID" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Keys":{"QmTestCID":{"Type":"recursive"}}}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	pinned, err := client.IsPinned(context.Background(), "QmTestCID")
	if err != nil {
		t.Fatalf("IsPinned() error = %v", err)
	}
	if !pinned {
		t.Fatal("IsPinned() = false, want true")
	}
}

func TestClient_IsPinned_False(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`path 'QmNotPinned' is not pinned`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	pinned, err := client.IsPinned(context.Background(), "QmNotPinned")
	if err != nil {
		t.Fatalf("IsPinned() error = %v", err)
	}
	if pinned {
		t.Fatal("IsPinned() = true, want false")
	}
}

func TestClient_AddReader_Disabled(t *testing.T) {
	client := NewClient("", "", time.Second)
	_, err := client.AddReader(context.Background(), "test.txt", strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected error when IPFS is disabled")
	}
	if !strings.Contains(err.Error(), "IPFS not enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_AddReader_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/add" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Name":"test.txt","Hash":"QmResultCID","Size":"9"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	cid, err := client.AddReader(context.Background(), "test.txt", strings.NewReader("some data"))
	if err != nil {
		t.Fatalf("AddReader() error = %v", err)
	}
	if cid != "QmResultCID" {
		t.Fatalf("AddReader() CID = %q, want %q", cid, "QmResultCID")
	}
}

func TestParseIPFSAddResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "single file",
			input:   `{"Name":"test.txt","Hash":"QmTest123","Size":"100"}`,
			want:    "QmTest123",
			wantErr: false,
		},
		{
			name: "multiple files (directory)",
			input: `{"Name":"file1.txt","Hash":"QmFile1","Size":"100"}
{"Name":"file2.txt","Hash":"QmFile2","Size":"200"}
{"Name":"","Hash":"QmDirectory","Size":"300"}`,
			want:    "QmDirectory",
			wantErr: false,
		},
		{
			name:    "empty response",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   "not json",
			want:    "",
			wantErr: true,
		},
		{
			name:    "missing hash",
			input:   `{"Name":"test.txt","Size":"100"}`,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIPFSAddResponse(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIPFSAddResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIPFSAddResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}
