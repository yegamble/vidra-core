package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrNotFound means no local actor matches the lookup (unknown/inactive account
// or channel, or a WebFinger for a domain we don't serve). ErrBadResource means a
// WebFinger `resource` was syntactically invalid.
var (
	ErrNotFound    = errors.New("federation: actor not found")
	ErrBadResource = errors.New("federation: bad webfinger resource")
)

// apContext is the JSON-LD @context every actor document carries. The security
// vocabulary is required for the publicKey block that HTTP-signature verification
// relies on.
var apContext = []string{
	"https://www.w3.org/ns/activitystreams",
	"https://w3id.org/security/v1",
}

// Actor is an ActivityPub actor document (Person for accounts, Group for
// channels). Only public data — never the private key.
type Actor struct {
	Context           []string         `json:"@context"`
	ID                string           `json:"id"`
	Type              string           `json:"type"`
	PreferredUsername string           `json:"preferredUsername"`
	Name              string           `json:"name,omitempty"`
	Summary           string           `json:"summary,omitempty"`
	URL               string           `json:"url,omitempty"`
	Inbox             string           `json:"inbox"`
	Outbox            string           `json:"outbox"`
	Followers         string           `json:"followers"`
	Following         string           `json:"following"`
	Endpoints         *Endpoints       `json:"endpoints,omitempty"`
	AttributedTo      []AttributedItem `json:"attributedTo,omitempty"`
	PublicKey         PublicKey        `json:"publicKey"`
}

// PublicKey is the actor's HTTP-signature public key block.
type PublicKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

// Endpoints is the actor's shared-inbox endpoint block. PeerTube reads
// endpoints.sharedInbox for fan-out delivery; we mirror it for parity.
type Endpoints struct {
	SharedInbox string `json:"sharedInbox,omitempty"`
}

// AttributedItem is one entry of a Group actor's attributedTo array: the owning
// account. PeerTube's actor validator (sanitizeAndCheckActorObject) rejects a
// Group whose attributedTo is empty, then findOwner fetches each entry and
// requires it to be a same-host Person.
type AttributedItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// JRD is a WebFinger JSON Resource Descriptor (RFC 7033).
type JRD struct {
	Subject string    `json:"subject"`
	Links   []JRDLink `json:"links"`
}

// JRDLink is a single WebFinger link relation.
type JRDLink struct {
	Rel  string `json:"rel"`
	Type string `json:"type"`
	Href string `json:"href"`
}

// AccountActor returns the ActivityPub Person document for a local account,
// minting its keypair on first request. Unknown/inactive username → ErrNotFound.
func (s *Service) AccountActor(ctx context.Context, username string) (*Actor, error) {
	u, err := s.repo.GetUserActorByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	pub, err := s.ensureAccountKey(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	base := s.baseURL + "/accounts/" + u.Username
	return buildActor(base, "Person", u.Username, u.DisplayName, u.Bio, pub), nil
}

// ChannelActor returns the ActivityPub Group document for a local channel,
// minting its keypair on first request. Unknown handle → ErrNotFound.
//
// The handle may be the channel's CURRENT one or a handle it was renamed away
// from — the actor id a peer already holds is exactly the latter, so refusing it
// would be refusing the address every existing follower uses.
func (s *Service) ChannelActor(ctx context.Context, handle string) (*Actor, error) {
	ch, err := s.resolveLocalChannel(ctx, strings.TrimSpace(handle))
	if err != nil {
		return nil, err
	}
	pub, err := s.ensureChannelKey(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	// PeerTube's actor validator rejects a Group with an empty attributedTo, then
	// findOwner FETCHES each entry and requires a same-host Person. Attribute the
	// Group to its OWNER ACCOUNT (…/accounts/<owner-username>) — which can differ
	// from the channel handle — whose Person actor is already served, validates,
	// and is same-host.
	owner, err := s.repo.GetUserActorByID(ctx, ch.OwnerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	base := s.baseURL + "/video-channels/" + s.channelActorHandle(ctx, ch)
	actor := buildActor(base, "Group", ch.Handle, ch.DisplayName, ch.Description, pub)
	actor.AttributedTo = []AttributedItem{{
		Type: "Person",
		ID:   s.baseURL + "/accounts/" + owner.Username,
	}}
	return actor, nil
}

// resolveLocalChannel finds a local channel by its current handle, falling back
// to a handle it was renamed away from (migration 0142's alias). Unknown →
// ErrNotFound.
//
// Alias resolution here is NOT bounded by expires_at. The alias table serves two
// different promises with two different lifetimes: a human redirect, which
// expires, and the ActivityPub identity, which does not — a peer holding
// `…/video-channels/ownera` in its follow rows must keep resolving it for as
// long as that follow exists.
func (s *Service) resolveLocalChannel(ctx context.Context, handle string) (sqlcgen.Channel, error) {
	ch, err := s.repo.GetChannelByHandle(ctx, handle)
	if err == nil {
		return ch, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.Channel{}, err
	}
	alias, aerr := s.repo.GetChannelHandleAlias(ctx, handle)
	if aerr != nil {
		if errors.Is(aerr, pgx.ErrNoRows) {
			return sqlcgen.Channel{}, ErrNotFound
		}
		return sqlcgen.Channel{}, aerr
	}
	ch, err = s.repo.GetChannelByHandle(ctx, alias.CurrentHandle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcgen.Channel{}, ErrNotFound
		}
		return sqlcgen.Channel{}, err
	}
	return ch, nil
}

// channelActorHandle is the handle a channel's ActivityPub id is built from: the
// frozen one when the channel was renamed out of a namespace collision, its own
// otherwise.
//
// This is the whole of "the actor id must not change". A rename moves
// preferredUsername, the profile URL and the human 301; the id, the inbox, the
// outbox, the followers and following collections and the key id all stay where
// the peers that already federated with this channel expect them. A lookup
// failure falls back to the live handle rather than failing the document: an
// actor served under a slightly wrong id is recoverable, and no actor at all is
// a follower silently losing a creator.
func (s *Service) channelActorHandle(ctx context.Context, ch sqlcgen.Channel) string {
	frozen, err := s.repo.GetChannelActorAlias(ctx, ch.ID)
	if err != nil || frozen == "" {
		return ch.Handle
	}
	return frozen
}

// buildActor assembles an Actor from its base URL and public fields. url and the
// endpoints.sharedInbox block are emitted for both Person and Group actors,
// mirroring PeerTube's actor shape.
func buildActor(base, typ, preferredUsername, name, summary, publicKeyPEM string) *Actor {
	return &Actor{
		Context:           apContext,
		ID:                base,
		Type:              typ,
		PreferredUsername: preferredUsername,
		Name:              name,
		Summary:           summary,
		URL:               base,
		Inbox:             base + "/inbox",
		Outbox:            base + "/outbox",
		Followers:         base + "/followers",
		Following:         base + "/following",
		Endpoints:         &Endpoints{SharedInbox: base + "/inbox"},
		PublicKey: PublicKey{
			ID:           base + "#main-key",
			Owner:        base,
			PublicKeyPem: publicKeyPEM,
		},
	}
}

// WebFinger resolves an `acct:name@domain` resource to its actor URL(s). It only
// answers for its own domain (else ErrNotFound). A malformed resource →
// ErrBadResource.
//
// IT RETURNS BOTH LINKS WHEN BOTH EXIST. Before migration 0142 an account and a
// channel could hold the same name, and this function resolved the account
// first and stopped — so on such an instance every channel-scoped feature keyed
// on the handle silently addressed the Person: a remote Follow of the handle was
// dropped with no Reject, and a remote viewer's block of `@name@domain` stored
// the Person url while the videos are attributed to the Group, hiding nothing.
// 0142 stops NEW collisions and renames the existing ones, but a renamed
// channel's old name still resolves — through its alias — to a Group, and peers
// that already federated with it still ask for it by that name.
//
// So the answer names both actors and lets the peer pick BY TYPE, which is what
// the `rel`/`type` pair is for. The Person, when there is one, is the `self`
// link, because `self` is singular by RFC 7033 and an account is the identity a
// bare `acct:` most often means; the Group is an `alternate` link carrying the
// same activity+json type. A peer that only understands `self` gets the same
// answer it got before this change.
func (s *Service) WebFinger(ctx context.Context, resource string) (*JRD, error) {
	acct := strings.TrimPrefix(resource, "acct:")
	name, domain, ok := strings.Cut(acct, "@")
	if !ok || name == "" || domain == "" {
		return nil, ErrBadResource
	}
	if !strings.EqualFold(domain, s.domain()) {
		return nil, ErrNotFound
	}
	var accountURL, channelURL string
	if u, err := s.repo.GetUserActorByUsername(ctx, name); err == nil {
		accountURL = s.baseURL + "/accounts/" + u.Username
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	switch ch, err := s.resolveLocalChannel(ctx, name); {
	case err == nil:
		channelURL = s.baseURL + "/video-channels/" + s.channelActorHandle(ctx, ch)
	case errors.Is(err, ErrNotFound):
		// no channel by that name — the account-only answer below
	default:
		return nil, err
	}
	if accountURL == "" && channelURL == "" {
		return nil, ErrNotFound
	}
	return jrd(resource, accountURL, channelURL), nil
}

// jrd renders the JRD for one resource. Either URL may be empty; when only the
// channel exists it takes the `self` slot, so a channel-only name answers
// exactly as it always did.
func jrd(subject, accountURL, channelURL string) *JRD {
	out := &JRD{Subject: subject}
	if accountURL != "" {
		out.Links = append(out.Links, JRDLink{
			Rel:  "self",
			Type: activityJSONType,
			Href: accountURL,
		})
	}
	if channelURL != "" {
		rel := "alternate"
		if accountURL == "" {
			rel = "self"
		}
		out.Links = append(out.Links, JRDLink{
			Rel:  rel,
			Type: activityJSONType,
			Href: channelURL,
		})
	}
	return out
}

// activityJSONType is the media type every WebFinger actor link advertises.
const activityJSONType = "application/activity+json"

// domain returns the host of the configured base URL (for WebFinger matching).
func (s *Service) domain() string {
	if u, err := url.Parse(s.baseURL); err == nil {
		return u.Host
	}
	return ""
}

// ensureAccountKey returns the account's public key PEM, minting + storing a
// keypair on first use. Concurrent minters are safe: the INSERT is
// ON CONFLICT DO NOTHING and the authoritative row is re-read afterwards.
func (s *Service) ensureAccountKey(ctx context.Context, userID uuid.UUID) (string, error) {
	if row, err := s.repo.GetAccountActorKey(ctx, userID); err == nil {
		return row.PublicKeyPem, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	pub, priv, err := generateActorKeypair()
	if err != nil {
		return "", err
	}
	stored, err := s.storePrivate(priv)
	if err != nil {
		return "", err
	}
	if _, err := s.repo.InsertAccountActorKeyIfAbsent(ctx, sqlcgen.InsertAccountActorKeyIfAbsentParams{
		UserID:        userID,
		PublicKeyPem:  pub,
		PrivateKeyPem: stored,
	}); err != nil {
		return "", err
	}
	row, err := s.repo.GetAccountActorKey(ctx, userID)
	if err != nil {
		return "", err
	}
	return row.PublicKeyPem, nil
}

// ensureChannelKey is ensureAccountKey for channels.
func (s *Service) ensureChannelKey(ctx context.Context, channelID uuid.UUID) (string, error) {
	if row, err := s.repo.GetChannelActorKey(ctx, channelID); err == nil {
		return row.PublicKeyPem, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	pub, priv, err := generateActorKeypair()
	if err != nil {
		return "", err
	}
	stored, err := s.storePrivate(priv)
	if err != nil {
		return "", err
	}
	if _, err := s.repo.InsertChannelActorKeyIfAbsent(ctx, sqlcgen.InsertChannelActorKeyIfAbsentParams{
		ChannelID:     channelID,
		PublicKeyPem:  pub,
		PrivateKeyPem: stored,
	}); err != nil {
		return "", err
	}
	row, err := s.repo.GetChannelActorKey(ctx, channelID)
	if err != nil {
		return "", err
	}
	return row.PublicKeyPem, nil
}

// storePrivate seals the private-key PEM for at-rest storage. With no cipher
// (dev), it stores the raw PEM.
func (s *Service) storePrivate(privatePEM string) (string, error) {
	if s.cipher == nil {
		return privatePEM, nil
	}
	return s.cipher.Seal([]byte(privatePEM))
}

// generateActorKeypair mints an RSA-2048 keypair and returns (publicPEM SPKI,
// privatePEM PKCS#8).
func generateActorKeypair() (publicPEM, privatePEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal public key: %w", err)
	}
	privatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	return publicPEM, privatePEM, nil
}
