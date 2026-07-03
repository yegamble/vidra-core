//go:build integration

// Integration tests for the moderation batch (product-decisions §11/§12/§13):
// the upload quarantine pipeline, watched-word video tagging, and the
// block-hides-content filters. Same harness contract as integration_test.go
// (live PostgreSQL via DATABASE_URL, migrations applied).
package store

import (
	"context"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestQuarantinePipelinePersists proves the §11 storage pieces against a real
// PostgreSQL: the widened state CHECK accepts 'quarantined', the gate query
// reflects role + users.bypass_quarantine (including the AdminUpdateUser
// COALESCE path), and the moderation queue lists exactly the quarantined
// videos with their channel + owner.
func TestQuarantinePipelinePersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID := seedOwnerChannel(ctx, t, st, "quar")
	videoID := seedPublishedVideo(ctx, t, st, channelID, "held probe")

	// The gate: a plain role=user owner without bypass requires quarantine.
	requires, err := q.UploadRequiresQuarantine(ctx, videoID)
	if err != nil || !requires {
		t.Fatalf("UploadRequiresQuarantine(plain user) = (%v, %v), want true", requires, err)
	}

	// Granting bypass_quarantine through the admin edit flips the gate off.
	bypass := true
	if u, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, BypassQuarantine: &bypass}); err != nil || !u.BypassQuarantine {
		t.Fatalf("AdminUpdateUser bypass=true = (%+v, %v)", u, err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); requires {
		t.Fatalf("gate still on for a bypassed account")
	}
	// An unrelated admin edit leaves the flag alone (COALESCE path).
	role := "user"
	if u, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, Role: &role}); err != nil || !u.BypassQuarantine {
		t.Fatalf("unrelated edit flipped bypass_quarantine: (%+v, %v)", u, err)
	}
	// Revoking re-arms the gate; a privileged role disarms it regardless.
	revoked := false
	if _, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, BypassQuarantine: &revoked}); err != nil {
		t.Fatalf("revoke bypass: %v", err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); !requires {
		t.Fatalf("gate off after revoking bypass")
	}
	moderator := "moderator"
	if _, err := q.AdminUpdateUser(ctx, sqlcgen.AdminUpdateUserParams{ID: userID, Role: &moderator}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if requires, _ = q.UploadRequiresQuarantine(ctx, videoID); requires {
		t.Fatalf("gate on for a moderator")
	}

	// The widened CHECK accepts 'quarantined'; the queue lists it with context.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "quarantined"}); err != nil {
		t.Fatalf("SetVideoState quarantined: %v", err)
	}
	queue, err := q.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 500})
	if err != nil {
		t.Fatalf("ListQuarantinedVideos: %v", err)
	}
	found := false
	for _, r := range queue {
		if r.ID == videoID {
			found = true
			if r.State != "quarantined" || r.OwnerUsername == "" || r.ChannelHandle == "" {
				t.Fatalf("queue row missing context: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("quarantined video missing from the queue")
	}

	// Quarantined videos are absent from the public discovery queries.
	feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{Sort: "recent", ResultLimit: 500})
	if err != nil {
		t.Fatalf("ListPublicVideosSorted: %v", err)
	}
	for _, r := range feed {
		if r.ID == videoID {
			t.Fatalf("quarantined video leaked into the public feed")
		}
	}

	// Releasing it (the approve transition's write) empties the queue again.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: videoID, State: "published"}); err != nil {
		t.Fatalf("SetVideoState published: %v", err)
	}
	queue, _ = q.ListQuarantinedVideos(ctx, sqlcgen.ListQuarantinedVideosParams{ResultLimit: 500})
	for _, r := range queue {
		if r.ID == videoID {
			t.Fatalf("published video still in the quarantine queue")
		}
	}
}
