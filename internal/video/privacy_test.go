package video

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// PrivacyAdmits is the single read-side privacy rule. The internal tier is the
// one this table exists for: any signed-in account, never an anonymous viewer.
// An unknown value must fail CLOSED (owner only) — a tier this build does not
// know about is never treated as public.
func TestPrivacyAdmits(t *testing.T) {
	for _, tc := range []struct {
		privacy         string
		authed, isOwner bool
		want            bool
	}{
		{"public", false, false, true},
		{"unlisted", false, false, true},
		// password's own gate (owner/staff/playback token) is applied by the HTTP
		// layer; the privacy rule alone admits it.
		{PrivacyPassword, false, false, true},

		{PrivacyInternal, false, false, false},
		{PrivacyInternal, true, false, true},
		{PrivacyInternal, true, true, true},

		{"private", false, false, false},
		{"private", true, false, false},
		{"private", true, true, true},

		{"members-only-v2", true, false, false},
		{"members-only-v2", true, true, true},
		{"", true, false, false},
	} {
		if got := PrivacyAdmits(tc.privacy, tc.authed, tc.isOwner); got != tc.want {
			t.Errorf("PrivacyAdmits(%q, authed=%v, owner=%v) = %v, want %v", tc.privacy, tc.authed, tc.isOwner, got, tc.want)
		}
	}
}

// public → internal withdraws the video from the fediverse: the update hook (the
// federation retraction seam) must hear that the video WAS federated, exactly as
// it does for public → private, so it sends the Delete.
func TestUpdatePublicToInternalReportsItWasFederated(t *testing.T) {
	owner := uuid.New()
	var calls []bool
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	svc := NewService(newFakeRepo(owner), blobs,
		WithUpdateHook(func(_ context.Context, _ uuid.UUID, wasFederated bool) { calls = append(calls, wasFederated) }))
	id := publishWithOriginal(t, svc, owner, "bytes")
	internal := PrivacyInternal
	if _, err := svc.Update(context.Background(), owner, id, UpdateInput{Privacy: &internal}); err != nil {
		t.Fatalf("Update to internal: %v", err)
	}
	if _, err := svc.Update(context.Background(), owner, id, UpdateInput{Privacy: &internal}); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if len(calls) != 2 || !calls[0] || calls[1] {
		t.Fatalf("wasFederated per update = %v, want [true false] (retract once, then nothing left to retract)", calls)
	}
}
