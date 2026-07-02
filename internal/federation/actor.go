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
	Context           []string  `json:"@context"`
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	PreferredUsername string    `json:"preferredUsername"`
	Name              string    `json:"name,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Inbox             string    `json:"inbox"`
	Outbox            string    `json:"outbox"`
	Followers         string    `json:"followers"`
	Following         string    `json:"following"`
	PublicKey         PublicKey `json:"publicKey"`
}

// PublicKey is the actor's HTTP-signature public key block.
type PublicKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
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
func (s *Service) ChannelActor(ctx context.Context, handle string) (*Actor, error) {
	ch, err := s.repo.GetChannelByHandle(ctx, strings.TrimSpace(handle))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	pub, err := s.ensureChannelKey(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	base := s.baseURL + "/video-channels/" + ch.Handle
	return buildActor(base, "Group", ch.Handle, ch.DisplayName, ch.Description, pub), nil
}

// buildActor assembles an Actor from its base URL and public fields.
func buildActor(base, typ, preferredUsername, name, summary, publicKeyPEM string) *Actor {
	return &Actor{
		Context:           apContext,
		ID:                base,
		Type:              typ,
		PreferredUsername: preferredUsername,
		Name:              name,
		Summary:           summary,
		Inbox:             base + "/inbox",
		Outbox:            base + "/outbox",
		Followers:         base + "/followers",
		Following:         base + "/following",
		PublicKey: PublicKey{
			ID:           base + "#main-key",
			Owner:        base,
			PublicKeyPem: publicKeyPEM,
		},
	}
}

// WebFinger resolves an `acct:name@domain` resource to its actor URL. It only
// answers for its own domain (else ErrNotFound) and tries an account first, then
// a channel. A malformed resource → ErrBadResource.
func (s *Service) WebFinger(ctx context.Context, resource string) (*JRD, error) {
	acct := strings.TrimPrefix(resource, "acct:")
	name, domain, ok := strings.Cut(acct, "@")
	if !ok || name == "" || domain == "" {
		return nil, ErrBadResource
	}
	if !strings.EqualFold(domain, s.domain()) {
		return nil, ErrNotFound
	}
	if u, err := s.repo.GetUserActorByUsername(ctx, name); err == nil {
		return jrd(resource, s.baseURL+"/accounts/"+u.Username), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if ch, err := s.repo.GetChannelByHandle(ctx, name); err == nil {
		return jrd(resource, s.baseURL+"/video-channels/"+ch.Handle), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return nil, ErrNotFound
}

func jrd(subject, actorURL string) *JRD {
	return &JRD{
		Subject: subject,
		Links: []JRDLink{{
			Rel:  "self",
			Type: "application/activity+json",
			Href: actorURL,
		}},
	}
}

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
