package federation

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

const (
	remoteFetchTimeout = 10 * time.Second
	// maxActorBytes bounds an actor document so a hostile/huge response can't
	// exhaust memory.
	maxActorBytes = 1 << 20 // 1 MiB
)

// fetchedActor is the subset of an ActivityPub actor document we parse.
type fetchedActor struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	PreferredUsername string `json:"preferredUsername"`
	Inbox             string `json:"inbox"`
	Followers         string `json:"followers"`
	PublicKey         struct {
		PublicKeyPem string `json:"publicKeyPem"`
	} `json:"publicKey"`
	Endpoints struct {
		SharedInbox string `json:"sharedInbox"`
	} `json:"endpoints"`
}

// ResolveKey returns the RSA public key for an HTTP-signature keyId. It strips
// the `#fragment` to get the actor URL, resolves (and caches) that remote actor,
// and parses its publicKeyPem. This is the resolver internal/httpsig.Verifier
// uses for inbound requests (wired in the inbox slice).
func (s *Service) ResolveKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	actorURL, _, _ := strings.Cut(keyID, "#")
	ra, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return nil, err
	}
	return parseRSAPublicKey(ra.PublicKeyPem)
}

// resolveRemoteActor returns the cached remote actor for actorURL, fetching and
// caching it on a miss. (Key rotation / TTL refresh is a later slice; a present
// row is served as-is.)
func (s *Service) resolveRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	if ra, err := s.repo.GetRemoteActor(ctx, actorURL); err == nil {
		return ra, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.RemoteActor{}, err
	}

	fa, err := s.fetchActor(ctx, actorURL)
	if err != nil {
		return sqlcgen.RemoteActor{}, err
	}

	var sharedInbox *string
	if fa.Endpoints.SharedInbox != "" {
		sharedInbox = &fa.Endpoints.SharedInbox
	}
	domain := ""
	if u, e := url.Parse(actorURL); e == nil {
		domain = u.Host
	}
	if err := s.repo.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl:          actorURL,
		ActorType:         fa.Type,
		PreferredUsername: fa.PreferredUsername,
		Domain:            domain,
		InboxUrl:          fa.Inbox,
		SharedInboxUrl:    sharedInbox,
		PublicKeyPem:      fa.PublicKey.PublicKeyPem,
		FollowersUrl:      fa.Followers,
	}); err != nil {
		return sqlcgen.RemoteActor{}, err
	}
	return s.repo.GetRemoteActor(ctx, actorURL)
}

// fetchActor GETs and parses a remote actor document through the SSRF guard.
func (s *Service) fetchActor(ctx context.Context, actorURL string) (fetchedActor, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(actorURL)
	if err != nil {
		return fetchedActor{}, fmt.Errorf("federation: unsafe actor URL: %w", err)
	}
	client := s.fetchClient
	if client == nil {
		client = guard.NewClient(remoteFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fetchedActor{}, err
	}
	req.Header.Set("Accept", "application/activity+json")
	resp, err := client.Do(req)
	if err != nil {
		return fetchedActor{}, fmt.Errorf("federation: fetch actor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fetchedActor{}, fmt.Errorf("federation: actor fetch returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxActorBytes))
	if err != nil {
		return fetchedActor{}, err
	}
	var fa fetchedActor
	if err := json.Unmarshal(body, &fa); err != nil {
		return fetchedActor{}, fmt.Errorf("federation: parse actor: %w", err)
	}
	if strings.TrimSpace(fa.PublicKey.PublicKeyPem) == "" {
		return fetchedActor{}, errors.New("federation: remote actor has no public key")
	}
	return fa, nil
}

// parseRSAPublicKey parses a PEM SPKI public key and asserts it is RSA.
func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("federation: invalid PEM public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("federation: parse public key: %w", err)
	}
	rp, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("federation: public key is not RSA")
	}
	return rp, nil
}
