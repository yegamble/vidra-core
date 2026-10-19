package instancesettings

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVASTPlayerSettings(t *testing.T) {
	const enabled, tag = "player_vast_enabled", "player_vast_tag_url"
	repo := newFakeRepo()
	svc := NewService(repo, testDefaults())
	for _, entry := range svc.Snapshot() {
		if entry.Key != enabled && entry.Key != tag {
			continue
		}
		if entry.Page != PageVOD || entry.Section != "advertising" || entry.Overridden {
			t.Errorf("unexpected advertising metadata: %+v", entry)
		}
	}
	if kind, ok := KindOf(enabled); !ok || kind != KindBool {
		t.Fatal("VAST toggle missing or not boolean")
	}
	if kind, ok := KindOf(tag); !ok || kind != KindString {
		t.Fatal("VAST tag missing or not string")
	}
	if svc.Bool(enabled) || svc.String(tag) != "" {
		t.Fatal("VAST must default off and unconfigured")
	}
	const prefix = "https://ads.example.test/"
	for _, value := range []string{"", "http://ads.example.test/vast", prefix + "vast?cb=[CACHEBUSTING]", prefix + strings.Repeat("a", 2000-len(prefix))} {
		if err := Validate(tag, value); err != nil {
			t.Errorf("valid tag rejected: %v", err)
		}
	}
	for _, value := range []string{" ", "/vast", "//ads.example.test/vast", "javascript:alert(1)", "https://:443/vast", "https://ads.example.test/#", prefix + "#fragment", "https://user:pass@ads.example.test/vast", prefix + "a b", prefix + "a\u00a0b", " " + prefix, prefix + "\t", prefix + strings.Repeat("a", 2001-len(prefix))} {
		if err := Validate(tag, value); err == nil {
			t.Errorf("invalid tag accepted: %q", value)
		}
	}
	ctx, actor := context.Background(), uuid.New()
	if err := svc.Apply(ctx, map[string]Update{enabled: {Value: "true"}, tag: {Value: prefix}}, actor); err != nil {
		t.Fatal(err)
	}
	reloaded := NewService(repo, testDefaults())
	if err := reloaded.Load(ctx); err != nil || !reloaded.Bool(enabled) || reloaded.String(tag) != prefix {
		t.Fatalf("settings did not survive reload: %v", err)
	}
	if err := reloaded.Apply(ctx, map[string]Update{enabled: {Delete: true}, tag: {Delete: true}}, actor); err != nil || reloaded.Bool(enabled) || reloaded.String(tag) != "" {
		t.Fatalf("reset did not restore disabled defaults: %v", err)
	}
}

func TestVASTPlacementSettings(t *testing.T) {
	const mid, offset, post = "player_vast_midroll_tag_url", "player_vast_midroll_offset_seconds", "player_vast_postroll_tag_url"
	svc := NewService(newFakeRepo(), testDefaults())
	if svc.String(mid) != "" || svc.String(post) != "" || svc.Int(offset) != 300 {
		t.Fatal("placements must default unconfigured with a five-minute midroll offset")
	}
	for _, key := range []string{mid, offset, post} {
		kind, ok := KindOf(key)
		if !ok || (key == offset && kind != KindInt) || (key != offset && kind != KindString) {
			t.Errorf("incorrect placement setting kind: %s", key)
		}
	}
	for _, entry := range svc.Snapshot() {
		if entry.Key == mid || entry.Key == offset || entry.Key == post {
			if entry.Page != PageVOD || entry.Section != "advertising" {
				t.Errorf("unexpected placement metadata: %+v", entry)
			}
		}
	}
	for _, key := range []string{mid, post} {
		for _, value := range []string{"", "https://ads.example.test/vast"} {
			if err := Validate(key, value); err != nil {
				t.Error(err)
			}
		}
		if err := Validate(key, "https://user:pass@ads.example.test/vast"); err == nil {
			t.Errorf("placement accepted invalid tag: %s", key)
		}
	}
	for _, value := range []string{"1", "300", "86400"} {
		if err := Validate(offset, value); err != nil {
			t.Error(err)
		}
	}
	for _, value := range []string{"0", "86401", "2.5", "bad"} {
		if err := Validate(offset, value); err == nil {
			t.Errorf("invalid offset accepted: %s", value)
		}
	}
}
