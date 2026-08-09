package federation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func newActorRepo() fakeRepo {
	uid := uuid.New()
	cid := uuid.New()
	return fakeRepo{
		usersByName: map[string]sqlcgen.GetUserActorByUsernameRow{
			"ada": {ID: uid, Username: "ada", DisplayName: "Ada L.", Bio: "math"},
		},
		usersByID: map[uuid.UUID]sqlcgen.GetUserActorByIDRow{
			uid: {ID: uid, Username: "ada", DisplayName: "Ada L.", Bio: "math"},
		},
		// The channel handle ("films") deliberately differs from the owner
		// account username ("ada") so tests catch a Group actor that attributes
		// itself to the channel handle instead of the owner account.
		channels: map[string]sqlcgen.Channel{
			"films": {ID: cid, OwnerID: uid, Handle: "films", DisplayName: "Films", Description: "clips"},
		},
		acctKeys: map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		chanKeys: map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
	}
}

func TestAccountActorMintsAndBuilds(t *testing.T) {
	repo := newActorRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	actor, err := svc.AccountActor(context.Background(), "ada")
	if err != nil {
		t.Fatalf("AccountActor: %v", err)
	}
	if actor.Type != "Person" {
		t.Errorf("type = %q, want Person", actor.Type)
	}
	if actor.ID != "https://videos.example/accounts/ada" {
		t.Errorf("id = %q", actor.ID)
	}
	if actor.PreferredUsername != "ada" || actor.Name != "Ada L." {
		t.Errorf("preferredUsername/name = %q/%q", actor.PreferredUsername, actor.Name)
	}
	if actor.Inbox != actor.ID+"/inbox" || actor.PublicKey.ID != actor.ID+"#main-key" {
		t.Errorf("inbox/keyID = %q/%q", actor.Inbox, actor.PublicKey.ID)
	}
	if !strings.Contains(actor.PublicKey.PublicKeyPem, "BEGIN PUBLIC KEY") {
		t.Errorf("publicKeyPem not a PEM public key: %q", actor.PublicKey.PublicKeyPem)
	}
	// The private key is stored (raw, no cipher) and never surfaces in the actor.
	uid := repo.usersByName["ada"].ID
	stored := repo.acctKeys[uid]
	if !strings.Contains(stored.PrivateKeyPem, "BEGIN PRIVATE KEY") {
		t.Errorf("private key not stored: %q", stored.PrivateKeyPem)
	}
	if strings.Contains(actor.PublicKey.PublicKeyPem, "PRIVATE") {
		t.Error("actor document leaked a private key")
	}
}

func TestAccountActorKeyMintedOnce(t *testing.T) {
	repo := newActorRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	a1, err := svc.AccountActor(context.Background(), "ada")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	a2, err := svc.AccountActor(context.Background(), "ada")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a1.PublicKey.PublicKeyPem != a2.PublicKey.PublicKeyPem {
		t.Error("public key changed between calls — key was re-minted, not reused")
	}
}

func TestAccountActorSealsPrivateKeyWithCipher(t *testing.T) {
	repo := newActorRepo()
	cipher, err := secretbox.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithCipher(cipher))
	if _, err := svc.AccountActor(context.Background(), "ada"); err != nil {
		t.Fatalf("AccountActor: %v", err)
	}
	stored := repo.acctKeys[repo.usersByName["ada"].ID].PrivateKeyPem
	if !secretbox.IsSealed(stored) {
		t.Errorf("stored private key not sealed: %q", stored)
	}
	if opened, err := cipher.Open(stored); err != nil || !strings.Contains(string(opened), "BEGIN PRIVATE KEY") {
		t.Errorf("sealed key does not open to the PEM: err=%v", err)
	}
}

func TestChannelActorMints(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	actor, err := svc.ChannelActor(context.Background(), "films")
	if err != nil {
		t.Fatalf("ChannelActor: %v", err)
	}
	if actor.Type != "Group" || actor.ID != "https://videos.example/video-channels/films" {
		t.Errorf("type/id = %q/%q", actor.Type, actor.ID)
	}
}

// TestChannelActorAttributedToOwnerAccount is the interop fix (PeerTube 8.2):
// PeerTube's sanitizeAndCheckActorObject rejects any Group whose attributedTo is
// empty, then findOwner FETCHES each attributedTo entry and requires it to be a
// same-host Person. The entry must point at the OWNER ACCOUNT's username
// (/accounts/ada), NOT the channel handle (/video-channels/films) — they differ.
func TestChannelActorAttributedToOwnerAccount(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	actor, err := svc.ChannelActor(context.Background(), "films")
	if err != nil {
		t.Fatalf("ChannelActor: %v", err)
	}
	if len(actor.AttributedTo) != 1 {
		t.Fatalf("attributedTo = %+v, want exactly one entry", actor.AttributedTo)
	}
	got := actor.AttributedTo[0]
	if got.Type != "Person" {
		t.Errorf("attributedTo[0].type = %q, want Person", got.Type)
	}
	if got.ID != "https://videos.example/accounts/ada" {
		t.Errorf("attributedTo[0].id = %q, want the owner account URL /accounts/ada (not the channel handle)", got.ID)
	}
}

// TestActorURLAndEndpoints asserts both actor types now carry url + the
// endpoints.sharedInbox block (PeerTube parity), and that the Group-only
// attributedTo never appears on a Person actor.
func TestActorURLAndEndpoints(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))

	person, err := svc.AccountActor(context.Background(), "ada")
	if err != nil {
		t.Fatalf("AccountActor: %v", err)
	}
	if person.URL != person.ID {
		t.Errorf("person url = %q, want %q", person.URL, person.ID)
	}
	if person.Endpoints == nil || person.Endpoints.SharedInbox != person.ID+"/inbox" {
		t.Errorf("person endpoints = %+v, want sharedInbox %q", person.Endpoints, person.ID+"/inbox")
	}
	if person.AttributedTo != nil {
		t.Errorf("person attributedTo = %+v, want nil (attributedTo is Group-only)", person.AttributedTo)
	}

	group, err := svc.ChannelActor(context.Background(), "films")
	if err != nil {
		t.Fatalf("ChannelActor: %v", err)
	}
	if group.URL != group.ID {
		t.Errorf("group url = %q, want %q", group.URL, group.ID)
	}
	if group.Endpoints == nil || group.Endpoints.SharedInbox != group.ID+"/inbox" {
		t.Errorf("group endpoints = %+v, want sharedInbox %q", group.Endpoints, group.ID+"/inbox")
	}
}

func TestActorNotFound(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	if _, err := svc.AccountActor(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AccountActor unknown: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.ChannelActor(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ChannelActor unknown: err = %v, want ErrNotFound", err)
	}
}

func TestWebFinger(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))

	jrd, err := svc.WebFinger(context.Background(), "acct:ada@videos.example")
	if err != nil {
		t.Fatalf("WebFinger account: %v", err)
	}
	if jrd.Subject != "acct:ada@videos.example" || len(jrd.Links) != 1 {
		t.Fatalf("jrd = %+v", jrd)
	}
	if jrd.Links[0].Href != "https://videos.example/accounts/ada" || jrd.Links[0].Rel != "self" {
		t.Errorf("link = %+v", jrd.Links[0])
	}

	chJRD, err := svc.WebFinger(context.Background(), "acct:films@videos.example")
	if err != nil {
		t.Fatalf("WebFinger channel: %v", err)
	}
	if chJRD.Links[0].Href != "https://videos.example/video-channels/films" {
		t.Errorf("channel link = %+v", chJRD.Links[0])
	}
}

func TestWebFingerRejects(t *testing.T) {
	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	// Wrong domain → not found (we only answer for our own).
	if _, err := svc.WebFinger(context.Background(), "acct:ada@other.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign domain: err = %v, want ErrNotFound", err)
	}
	// Unknown local name → not found.
	if _, err := svc.WebFinger(context.Background(), "acct:ghost@videos.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown name: err = %v, want ErrNotFound", err)
	}
	// Malformed resource → bad resource.
	for _, bad := range []string{"ada", "acct:@videos.example", "acct:ada"} {
		if _, err := svc.WebFinger(context.Background(), bad); !errors.Is(err, ErrBadResource) {
			t.Errorf("resource %q: err = %v, want ErrBadResource", bad, err)
		}
	}
}
