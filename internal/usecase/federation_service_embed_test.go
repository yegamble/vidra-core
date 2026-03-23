package usecase

import (
	"testing"
)

func TestExtractEmbedInfo_Video(t *testing.T) {
	svc := &federationService{}

	rec := map[string]any{
		"embed": map[string]any{
			"$type": "app.bsky.embed.video",
			// Other fields are not required for our detection
		},
	}

	embedType, url, title, desc := svc.extractEmbedInfo(rec)
	if embedType == nil || *embedType != "video" {
		t.Fatalf("expected embedType=video, got %v", deref(embedType))
	}
	if url != nil || title != nil || desc != nil {
		t.Fatalf("expected no external details for video embed")
	}
}

func TestExtractEmbedInfo_RecordWithMediaVideo(t *testing.T) {
	svc := &federationService{}
	rec := map[string]any{
		"embed": map[string]any{
			"$type": "app.bsky.embed.recordWithMedia",
			"media": map[string]any{
				"$type": "app.bsky.embed.video",
			},
		},
	}
	embedType, url, title, desc := svc.extractEmbedInfo(rec)
	if embedType == nil || *embedType != "video" {
		t.Fatalf("expected embedType=video for recordWithMedia, got %v", deref(embedType))
	}
	if url != nil || title != nil || desc != nil {
		t.Fatalf("expected no external details for recordWithMedia video embed")
	}
}

func TestExtractEmbedInfo_External(t *testing.T) {
	svc := &federationService{}
	rec := map[string]any{
		"embed": map[string]any{
			"$type": "app.bsky.embed.external",
			"external": map[string]any{
				"uri":         "https://example.com/v",
				"title":       "Title",
				"description": "Desc",
			},
		},
	}
	et, url, title, desc := svc.extractEmbedInfo(rec)
	if et == nil || *et != "external" {
		t.Fatalf("expected embedType=external, got %v", deref(et))
	}
	if url == nil || *url != "https://example.com/v" {
		t.Fatalf("unexpected url: %v", deref(url))
	}
	if title == nil || *title != "Title" {
		t.Fatalf("unexpected title: %v", deref(title))
	}
	if desc == nil || *desc != "Desc" {
		t.Fatalf("unexpected desc: %v", deref(desc))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
