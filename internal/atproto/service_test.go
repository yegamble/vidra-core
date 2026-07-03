package atproto

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func newTestCipher(t *testing.T) *secretbox.Cipher {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("rand: %v", err)
	}
	c, err := secretbox.NewCipher(kek)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestSealPasswordRoundTrip(t *testing.T) {
	cipher := newTestCipher(t)
	s := NewService(newFakeRepo(), WithEnabled(true), WithCipher(cipher), WithPDSClient(newFakePDS()))

	sealed, err := s.sealPassword("app-pass-1234")
	if err != nil {
		t.Fatalf("sealPassword: %v", err)
	}
	if !secretbox.IsSealed(sealed) {
		t.Fatalf("sealed value %q is not enc-prefixed", sealed)
	}
	if strings.Contains(sealed, "app-pass-1234") {
		t.Fatalf("sealed value leaks the plaintext password")
	}
	got, err := s.openPassword(sealed)
	if err != nil {
		t.Fatalf("openPassword: %v", err)
	}
	if got != "app-pass-1234" {
		t.Errorf("round trip = %q, want %q", got, "app-pass-1234")
	}
}

func TestSealPasswordRawWithoutCipher(t *testing.T) {
	s := NewService(newFakeRepo(), WithEnabled(true), WithPDSClient(newFakePDS()))
	sealed, err := s.sealPassword("raw-pass")
	if err != nil {
		t.Fatalf("sealPassword: %v", err)
	}
	if sealed != "raw-pass" {
		t.Errorf("without a cipher the password is stored raw; got %q", sealed)
	}
	got, _ := s.openPassword("raw-pass")
	if got != "raw-pass" {
		t.Errorf("openPassword(raw) = %q, want raw-pass", got)
	}
}

func TestLinkVerifiesAndSeals(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	cipher := newTestCipher(t)
	s := NewService(repo, WithEnabled(true), WithCipher(cipher), WithPDSClient(pds))

	uid := uuid.New()
	acct, err := s.Link(context.Background(), uid, LinkInput{
		Handle:      "@Alice.bsky.social",
		AppPassword: "secret-app-pass",
		AutoPost:    true,
	})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	// createSession was called against the default PDS with the normalized handle
	// and the exact app password.
	if len(pds.createCalls) != 1 {
		t.Fatalf("expected 1 createSession call, got %d", len(pds.createCalls))
	}
	call := pds.createCalls[0]
	if call.PDSURL != DefaultPDSURL {
		t.Errorf("createSession PDS = %q, want %q", call.PDSURL, DefaultPDSURL)
	}
	if call.Identifier != "alice.bsky.social" {
		t.Errorf("createSession identifier = %q, want normalized handle", call.Identifier)
	}
	if call.AppPassword != "secret-app-pass" {
		t.Errorf("createSession app password not passed through")
	}

	// The stored account resolves the DID and seals the password.
	if acct.Did != "did:plc:test" {
		t.Errorf("did = %q, want resolved did:plc:test", acct.Did)
	}
	if !acct.AutoPost {
		t.Errorf("auto_post not stored")
	}
	if !secretbox.IsSealed(acct.AppPasswordSealed) {
		t.Errorf("stored app password is not sealed")
	}
	if strings.Contains(acct.AppPasswordSealed, "secret-app-pass") {
		t.Errorf("stored value leaks the app password")
	}
	opened, _ := s.openPassword(acct.AppPasswordSealed)
	if opened != "secret-app-pass" {
		t.Errorf("sealed password does not reopen to the original")
	}
}

func TestLinkRejectsBadCredentials(t *testing.T) {
	pds := newFakePDS()
	pds.createErr = &XRPCError{Status: 401, Op: nsidCreateSession, ErrName: "AuthenticationRequired"}
	s := NewService(newFakeRepo(), WithEnabled(true), WithPDSClient(pds))

	_, err := s.Link(context.Background(), uuid.New(), LinkInput{Handle: "alice.bsky.social", AppPassword: "wrong"})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLinkPDSUnreachable(t *testing.T) {
	pds := newFakePDS()
	pds.createErr = &XRPCError{Status: 0, Op: nsidCreateSession}
	s := NewService(newFakeRepo(), WithEnabled(true), WithPDSClient(pds))

	_, err := s.Link(context.Background(), uuid.New(), LinkInput{Handle: "alice.bsky.social", AppPassword: "x"})
	if !errors.Is(err, ErrPDSUnreachable) {
		t.Fatalf("err = %v, want ErrPDSUnreachable", err)
	}
}

func TestLinkRejectsInvalidHandleBeforeCallingPDS(t *testing.T) {
	pds := newFakePDS()
	s := NewService(newFakeRepo(), WithEnabled(true), WithPDSClient(pds))

	_, err := s.Link(context.Background(), uuid.New(), LinkInput{Handle: "nodothere", AppPassword: "x"})
	if !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("err = %v, want ErrInvalidHandle", err)
	}
	if len(pds.createCalls) != 0 {
		t.Errorf("PDS must not be contacted for an obviously invalid handle")
	}
}

func TestLinkDisabled(t *testing.T) {
	s := NewService(newFakeRepo(), WithPDSClient(newFakePDS())) // enabled defaults false
	_, err := s.Link(context.Background(), uuid.New(), LinkInput{Handle: "alice.bsky.social", AppPassword: "x"})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestStatusAndUnlink(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, WithEnabled(true), WithPDSClient(newFakePDS()))
	uid := uuid.New()

	if _, err := s.Status(context.Background(), uid); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("Status(unlinked) err = %v, want ErrNotLinked", err)
	}

	repo.accounts[uid] = sqlcgen.AtprotoAccount{UserID: uid, Handle: "alice.bsky.social", Did: "did:plc:test", AutoPost: true}
	got, err := s.Status(context.Background(), uid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Handle != "alice.bsky.social" {
		t.Errorf("status handle = %q", got.Handle)
	}

	if err := s.Unlink(context.Background(), uid); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if _, ok := repo.accounts[uid]; ok {
		t.Errorf("account still present after Unlink")
	}
	// Idempotent: unlinking again is a no-op success.
	if err := s.Unlink(context.Background(), uid); err != nil {
		t.Errorf("second Unlink: %v", err)
	}
}

func TestEnqueueForVideoGating(t *testing.T) {
	uid := uuid.New()
	vid := uuid.New()

	setup := func(privacy string, autoPost, linked bool) *fakeRepo {
		repo := newFakeRepo()
		repo.videos[vid] = sqlcgen.GetATProtoPostVideoRow{ID: vid, Title: "T", Privacy: privacy, OwnerID: uid}
		if linked {
			repo.accounts[uid] = sqlcgen.AtprotoAccount{UserID: uid, Handle: "alice.bsky.social", Did: "did:plc:test", AutoPost: autoPost}
		}
		return repo
	}

	cases := []struct {
		name    string
		privacy string
		auto    bool
		linked  bool
		enqueue bool
	}{
		{"public+auto+linked", "public", true, true, true},
		{"unlisted", "unlisted", true, true, false},
		{"private", "private", true, true, false},
		{"autopost off", "public", false, true, false},
		{"not linked", "public", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := setup(tc.privacy, tc.auto, tc.linked)
			s := NewService(repo, WithEnabled(true), WithPDSClient(newFakePDS()))
			if err := s.EnqueueForVideo(context.Background(), vid); err != nil {
				t.Fatalf("EnqueueForVideo: %v", err)
			}
			got := repo.postForVideo(vid) != nil
			if got != tc.enqueue {
				t.Errorf("enqueued = %v, want %v", got, tc.enqueue)
			}
		})
	}
}

func TestEnqueueForVideoDedupe(t *testing.T) {
	uid := uuid.New()
	vid := uuid.New()
	repo := newFakeRepo()
	repo.videos[vid] = sqlcgen.GetATProtoPostVideoRow{ID: vid, Title: "T", Privacy: "public", OwnerID: uid}
	repo.accounts[uid] = sqlcgen.AtprotoAccount{UserID: uid, AutoPost: true, Handle: "alice.bsky.social"}
	s := NewService(repo, WithEnabled(true), WithPDSClient(newFakePDS()))

	for i := 0; i < 3; i++ {
		if err := s.EnqueueForVideo(context.Background(), vid); err != nil {
			t.Fatalf("EnqueueForVideo: %v", err)
		}
	}
	if n := len(repo.posts); n != 1 {
		t.Errorf("enqueued %d posts, want 1 (video_id dedupe)", n)
	}
}

func TestEnqueueForVideoDisabledNoop(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, WithPDSClient(newFakePDS())) // disabled
	if err := s.EnqueueForVideo(context.Background(), uuid.New()); err != nil {
		t.Fatalf("EnqueueForVideo: %v", err)
	}
	if len(repo.posts) != 0 {
		t.Errorf("disabled service must not enqueue")
	}
}
