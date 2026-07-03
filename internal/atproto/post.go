package atproto

import (
	"strings"
	"time"
)

// maxPostText is the Bluesky post-text limit (300 graphemes). Runes are a close
// enough approximation for truncation — we only ever shorten, never pad.
const maxPostText = 300

// buildPostRecord renders an app.bsky.feed.post record announcing a video: the
// title as the post text (truncated to the Bluesky limit) plus an
// app.bsky.embed.external card linking to the public watch URL. thumb, when
// non-empty, is the blob reference returned by uploadBlob, attached as the card
// image. createdAt is the record timestamp.
//
// It is a pure function (no I/O) so the produced JSON shape is unit-testable.
func buildPostRecord(title, watchURL string, createdAt time.Time, thumb Blob) map[string]any {
	text := truncateRunes(strings.TrimSpace(title), maxPostText)
	external := map[string]any{
		"uri":         watchURL,
		"title":       text,
		"description": "",
	}
	if len(thumb) > 0 {
		external["thumb"] = thumb
	}
	return map[string]any{
		"$type":     feedPostCollection,
		"text":      text,
		"createdAt": createdAt.UTC().Format(time.RFC3339),
		"embed": map[string]any{
			"$type":    "app.bsky.embed.external",
			"external": external,
		},
	}
}

// truncateRunes shortens s to at most max runes.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
