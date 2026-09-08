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
	// AttributedTo names the ACCOUNT that owns a Group actor. PeerTube requires
	// it on every channel actor and vidra emits it, so on the fediverse vidra
	// actually federates with, a channel's owner is stated in its own document.
	// It is the edge a block of an account travels down to reach that account's
	// channels (0142); the rehearsal measured what its absence costs — a viewer
	// blocking @name@domain saw nothing change, because the videos are
	// attributed to the Group and the block named the Person.
	AttributedTo json.RawMessage `json:"attributedTo"`
	PublicKey    struct {
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

// ResolveKeyFresh re-fetches the actor document and returns the key it carries
// NOW, bypassing the cached copy. It is the second half of the verifier's
// fetch-once-then-verify: a signature that does not check against the cached key
// is retried once against a freshly fetched one, which is what lets a peer
// rotate its keypair without becoming permanently unverifiable here (A29-F12).
//
// It is bounded by construction: only a request that already failed against a
// CACHED key reaches it, so an actor this instance has never seen cannot trigger
// a fetch through this path, and one failed signature buys exactly one re-fetch.
func (s *Service) ResolveKeyFresh(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	actorURL, _, _ := strings.Cut(keyID, "#")
	ra, err := s.refreshRemoteActorKey(ctx, actorURL)
	if err != nil {
		return nil, err
	}
	return parseRSAPublicKey(ra.PublicKeyPem)
}

// resolveRemoteActor returns the cached remote actor for actorURL, fetching and
// caching it on a miss.
//
// THE GUARD RUNS ON EVERY RESOLUTION, CACHED OR NOT (A29-F12). A29 measured an
// SSRF probe PASSING with the relax off, because resolveRemoteActor returned a
// row that an earlier inbound signature verification had cached before
// urlsafety.Guard ever ran. The cache was doing the fetch's job of deciding
// whether a URL is reachable at all, which is a decision it has no business
// making: the guard's answer is about the ADDRESS, and an address does not
// become safe by having been seen before. Validating first costs one parse and
// one DNS resolution on a path that is about to do a database read anyway.
func (s *Service) resolveRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	if _, err := guard.ValidateURL(actorURL); err != nil {
		return sqlcgen.RemoteActor{}, fmt.Errorf("federation: unsafe actor URL: %w", err)
	}
	if ra, err := s.repo.GetRemoteActor(ctx, actorURL); err == nil {
		return ra, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.RemoteActor{}, err
	}
	return s.refetchRemoteActor(ctx, actorURL)
}

// refreshRemoteActorKey re-fetches a cached actor and replaces its stored
// publicKeyPem — the recovery path for key rotation at a peer (A29-F12).
//
// Vidra caches an actor's key once and never refreshed it, so a peer rotating
// its keypair became a PERMANENT verification failure here: every subsequent
// signed activity from that server was 401, with no path back short of an
// operator deleting the row. It is called at most once per verification failure
// (fetch-once-then-verify) and only when a row already exists, so an unsigned or
// forged request cannot use it to make this instance fetch anything it would not
// have fetched anyway.
func (s *Service) refreshRemoteActorKey(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	if _, err := guard.ValidateURL(actorURL); err != nil {
		return sqlcgen.RemoteActor{}, fmt.Errorf("federation: unsafe actor URL: %w", err)
	}
	if _, err := s.repo.GetRemoteActor(ctx, actorURL); err != nil {
		// Nothing cached: this is not a rotation, it is a first resolution, and
		// the ordinary path already handles it.
		return sqlcgen.RemoteActor{}, err
	}
	return s.refetchRemoteActor(ctx, actorURL)
}

// refetchRemoteActor fetches an actor document and upserts the cache row.
func (s *Service) refetchRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
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
	// Only a SAME-HOST owner is stored. attributedTo is attacker-controlled
	// text, and a Group claiming to be owned by an account on some other server
	// would otherwise let that server's block list reach in — or, worse, let a
	// hostile peer attach its actors to a well-behaved instance's account. Same
	// host is the same authority rule the Announce ingest already applies to
	// attribution.
	owner := firstAttributedTo(fa.AttributedTo)
	if owner != "" && !sameHost(owner, actorURL) {
		owner = ""
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
		AttributedTo:      owner,
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
