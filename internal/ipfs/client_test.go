package ipfs

import (
	"context"
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
	// Create a temp file
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

// TestClient_AddFile_Integration tests file upload to a real IPFS node
// This test is skipped by default and requires a running IPFS node
func TestClient_AddFile_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Create a temporary test file
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

// TestClient_AddDirectory_Integration tests directory upload to a real IPFS node
func TestClient_AddDirectory_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Create a temporary test directory with files
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create some test files
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
