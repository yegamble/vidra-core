package obs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testAuditEntry parses an audit log line as JSON.
func parseAuditEntry(t *testing.T, line string) map[string]interface{} {
	t.Helper()
	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("failed to parse audit entry %q: %v", line, err)
	}
	return entry
}

// readAuditLines reads all non-empty lines from a file.
func readAuditLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open audit log: %v", err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestAuditLogger_CreateEntry(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))

	al.Create("videos", "alice", MapAuditView{"video-id": "v123", "video-name": "My Video"})

	// Close to flush the channel before reading the file
	al.Close()
	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) == 0 {
		t.Fatal("expected audit log entry, got none")
	}

	entry := parseAuditEntry(t, lines[0])
	if entry["action"] != "create" {
		t.Errorf("expected action=create, got %v", entry["action"])
	}
	if entry["user"] != "alice" {
		t.Errorf("expected user=alice, got %v", entry["user"])
	}
	if entry["domain"] != "videos" {
		t.Errorf("expected domain=videos, got %v", entry["domain"])
	}
	if entry["video-id"] != "v123" {
		t.Errorf("expected video-id=v123, got %v", entry["video-id"])
	}
	// Must have a timestamp
	if _, ok := entry["timestamp"]; !ok {
		t.Error("expected timestamp field in audit entry")
	}
}

func TestAuditLogger_DeleteEntry(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))
	defer al.Close()

	al.Delete("users", "admin", MapAuditView{"user-id": "u456", "user-username": "bob"})
	al.Close()

	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) == 0 {
		t.Fatal("expected audit log entry")
	}

	entry := parseAuditEntry(t, lines[0])
	if entry["action"] != "delete" {
		t.Errorf("expected action=delete, got %v", entry["action"])
	}
	if entry["domain"] != "users" {
		t.Errorf("expected domain=users, got %v", entry["domain"])
	}
}

func TestAuditLogger_UpdateEntry_WithDiff(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))
	defer al.Close()

	old := MapAuditView{"video-name": "Old Title", "video-privacy": "public"}
	new_ := MapAuditView{"video-name": "New Title", "video-privacy": "public"}
	al.Update("videos", "alice", new_, old)
	al.Close()

	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) == 0 {
		t.Fatal("expected audit log entry")
	}

	entry := parseAuditEntry(t, lines[0])
	if entry["action"] != "update" {
		t.Errorf("expected action=update, got %v", entry["action"])
	}
	// Old value should be present
	if entry["video-name"] != "Old Title" {
		t.Errorf("expected old video-name=Old Title, got %v", entry["video-name"])
	}
	// Changed value should have new- prefix
	if entry["new-video-name"] != "New Title" {
		t.Errorf("expected new-video-name=New Title, got %v", entry["new-video-name"])
	}
	// Unchanged value should not have new- prefix entry
	if _, hasNew := entry["new-video-privacy"]; hasNew {
		t.Error("expected no new-video-privacy since value did not change")
	}
}

func TestAuditLogger_AsyncWrites(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))

	// Write multiple entries concurrently
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		i := i
		go func() {
			al.Create("videos", "alice", MapAuditView{"index": float64(i)})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	al.Close() // flushes channel

	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) != 10 {
		t.Errorf("expected 10 audit entries, got %d", len(lines))
	}
}

func TestAuditLogger_Timestamp(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))
	defer al.Close()

	before := time.Now()
	al.Create("config", "admin", MapAuditView{"key": "value"})
	al.Close()

	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) == 0 {
		t.Fatal("expected audit entry")
	}

	entry := parseAuditEntry(t, lines[0])
	tsStr, ok := entry["timestamp"].(string)
	if !ok {
		t.Fatalf("expected timestamp string, got %T: %v", entry["timestamp"], entry["timestamp"])
	}
	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		t.Fatalf("failed to parse timestamp %q: %v", tsStr, err)
	}
	if ts.Before(before) {
		t.Errorf("audit timestamp %v is before test start %v", ts, before)
	}
}

func TestAuditLoggerFactory(t *testing.T) {
	dir := t.TempDir()
	al := NewAuditLogger(filepath.Join(dir, "audit.log"))
	defer al.Close()

	videoAuditor := AuditLoggerFactory("videos", al)
	videoAuditor.Create("alice", MapAuditView{"video-id": "v1"})
	al.Close()

	lines := readAuditLines(t, filepath.Join(dir, "audit.log"))
	if len(lines) == 0 {
		t.Fatal("expected audit entry via factory")
	}
	entry := parseAuditEntry(t, lines[0])
	if entry["domain"] != "videos" {
		t.Errorf("expected domain=videos via factory, got %v", entry["domain"])
	}
}
