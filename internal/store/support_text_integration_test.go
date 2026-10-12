//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestSupportTextOnRealPG holds migration 0160's SQL contract where only the
// database can: the update queries read NULL as "leave it" and ” as "clear it"
// (an unconditional COALESCE could never clear), the detail row carries the
// video's text, and the 1000-character CHECK stands behind the API's cap.
func TestSupportTextOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var owner uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"support-"+suffix, "support-"+suffix+"@example.test").Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })

	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{OwnerID: owner, Handle: "supch-" + suffix, DisplayName: "S"})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if ch.Support != nil {
		t.Fatalf("new channel support = %q, want NULL", *ch.Support)
	}
	text := "Support me on Liberapay"
	ch, err = q.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{ID: ch.ID, Support: &text})
	if err != nil || ch.Support == nil || *ch.Support != text {
		t.Fatalf("set channel support: %v %v", ch.Support, err)
	}
	// An update that does not mention support leaves it.
	name := "Renamed"
	if ch, err = q.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{ID: ch.ID, DisplayName: &name}); err != nil || ch.Support == nil {
		t.Fatalf("support after an unrelated update = %v (%v), want kept", ch.Support, err)
	}
	empty := ""
	if ch, err = q.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{ID: ch.ID, Support: &empty}); err != nil || ch.Support != nil {
		t.Fatalf("support after clearing = %v (%v), want NULL", ch.Support, err)
	}

	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "v", Privacy: "private", CommentsPolicy: "enabled", DownloadEnabled: true, Support: &text,
	})
	if err != nil || v.Support == nil || *v.Support != text {
		t.Fatalf("create video with support: %v %v", v.Support, err)
	}
	row, err := q.GetVideoByID(ctx, v.ID)
	if err != nil || row.Support == nil || *row.Support != text {
		t.Fatalf("GetVideoByID support = %v (%v), want %q", row.Support, err, text)
	}
	title := "v2"
	if v, err = q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{ID: v.ID, Title: &title}); err != nil || v.Support == nil {
		t.Fatalf("video support after an unrelated update = %v (%v), want kept", v.Support, err)
	}
	if v, err = q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{ID: v.ID, Support: &empty}); err != nil || v.Support != nil {
		t.Fatalf("video support after clearing = %v (%v), want NULL", v.Support, err)
	}

	// The column CHECK is the backstop for the API cap: 1000 characters fit
	// (counted as characters, not bytes), 1001 do not.
	fits := strings.Repeat("é", 1000)
	if _, err := q.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{ID: ch.ID, Support: &fits}); err != nil {
		t.Fatalf("1000 characters refused: %v", err)
	}
	over := strings.Repeat("x", 1001)
	if _, err := q.UpdateChannel(ctx, sqlcgen.UpdateChannelParams{ID: ch.ID, Support: &over}); err == nil {
		t.Fatal("1001 characters accepted; the channels_support_len CHECK is missing")
	}
	if _, err := q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{ID: v.ID, Support: &over}); err == nil {
		t.Fatal("1001 characters accepted on a video; the videos_support_len CHECK is missing")
	}
}
