package atproto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildPostRecordShape(t *testing.T) {
	created := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	rec := buildPostRecord("My Great Video", "https://videos.example/videos/abc", created, nil)

	if rec["$type"] != feedPostCollection {
		t.Errorf("$type = %v, want %s", rec["$type"], feedPostCollection)
	}
	if rec["text"] != "My Great Video" {
		t.Errorf("text = %v", rec["text"])
	}
	if rec["createdAt"] != "2026-07-03T12:00:00Z" {
		t.Errorf("createdAt = %v, want RFC3339 UTC", rec["createdAt"])
	}
	embed, ok := rec["embed"].(map[string]any)
	if !ok {
		t.Fatalf("embed missing or wrong type")
	}
	if embed["$type"] != "app.bsky.embed.external" {
		t.Errorf("embed $type = %v", embed["$type"])
	}
	external, ok := embed["external"].(map[string]any)
	if !ok {
		t.Fatalf("external missing")
	}
	if external["uri"] != "https://videos.example/videos/abc" {
		t.Errorf("external uri = %v", external["uri"])
	}
	if external["title"] != "My Great Video" {
		t.Errorf("external title = %v", external["title"])
	}
	if _, hasThumb := external["thumb"]; hasThumb {
		t.Errorf("no thumb was supplied but embed carries one")
	}

	// The whole record must marshal to valid JSON.
	if _, err := json.Marshal(rec); err != nil {
		t.Fatalf("record does not marshal: %v", err)
	}
}

func TestBuildPostRecordWithThumb(t *testing.T) {
	blob := Blob(`{"$type":"blob","ref":{"$link":"bafyabc"},"mimeType":"image/jpeg","size":42}`)
	rec := buildPostRecord("Title", "https://x/videos/1", time.Now(), blob)
	embed := rec["embed"].(map[string]any)
	external := embed["external"].(map[string]any)
	thumb, ok := external["thumb"]
	if !ok {
		t.Fatalf("thumb not attached to embed")
	}
	// The blob is embedded verbatim (json.RawMessage round-trips exactly).
	out, err := json.Marshal(map[string]any{"thumb": thumb})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "bafyabc") {
		t.Errorf("thumb blob not embedded verbatim: %s", out)
	}
}

func TestBuildPostRecordTruncatesLongTitle(t *testing.T) {
	long := strings.Repeat("x", maxPostText+50)
	rec := buildPostRecord(long, "https://x/videos/1", time.Now(), nil)
	if got := len([]rune(rec["text"].(string))); got != maxPostText {
		t.Errorf("text length = %d, want capped at %d", got, maxPostText)
	}
}
