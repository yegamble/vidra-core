package video

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestIsWebVTT(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"exact", "WEBVTT", true},
		{"newline", "WEBVTT\n\n00:00.000 --> 00:01.000\nhi", true},
		{"crlf", "WEBVTT\r\n", true},
		{"space header", "WEBVTT - some header\n", true},
		{"tab header", "WEBVTT\tx", true},
		{"utf8 bom", "\xEF\xBB\xBFWEBVTT\n", true},
		{"no signature", "not a caption", false},
		{"glued suffix", "WEBVTTX", false},
		{"empty", "", false},
		{"lowercase", "webvtt\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWebVTT([]byte(tc.in)); got != tc.want {
				t.Errorf("isWebVTT(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLangPattern(t *testing.T) {
	valid := []string{"en", "es", "pt-BR", "zh-Hans", "en-US-x-1"}
	invalid := []string{"", "e", "1", "en_US", "not a lang!", "toolonglang"}
	for _, v := range valid {
		if !langPattern.MatchString(v) {
			t.Errorf("langPattern rejected valid %q", v)
		}
	}
	for _, v := range invalid {
		if langPattern.MatchString(v) {
			t.Errorf("langPattern accepted invalid %q", v)
		}
	}
}

type captionChange struct {
	key     string
	deleted bool
}

// TestCaptionChangeHookFiresOnStoreReplaceAndDelete: the mirror pins a caption
// under its stable key, so it has to hear about every change to that key.
func TestCaptionChangeHookFiresOnStoreReplaceAndDelete(t *testing.T) {
	owner := uuid.New()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var got []captionChange
	svc := NewService(newFakeRepo(owner), blobs, WithCaptionChangeHook(func(_ context.Context, _ uuid.UUID, key string, deleted bool) {
		got = append(got, captionChange{key, deleted})
	}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	add := func(body string) {
		t.Helper()
		if _, err := svc.AddCaption(ctx, owner, v.ID, CaptionInput{Language: "en", Reader: strings.NewReader(body)}); err != nil {
			t.Fatalf("AddCaption: %v", err)
		}
	}
	add("WEBVTT\n\nfirst")
	add("WEBVTT\n\nsecond") // replace: same key, new bytes
	if err := svc.DeleteCaption(ctx, owner, v.ID, "en"); err != nil {
		t.Fatalf("DeleteCaption: %v", err)
	}
	if err := svc.DeleteCaption(ctx, owner, v.ID, "en"); err != nil { // idempotent: nothing left to tell anyone about
		t.Fatalf("DeleteCaption again: %v", err)
	}
	key := captionKey(v.ID, "en")
	want := []captionChange{{key, false}, {key, false}, {key, true}}
	if len(got) != len(want) {
		t.Fatalf("hook calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hook calls = %v, want %v", got, want)
		}
	}
}
