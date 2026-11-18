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

	"github.com/vidra/vidra-core/internal/httpsig"
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
	// Outbox is read only by CrawlOutbox, and is not cached with the actor.
	Outbox    string `json:"outbox"`
	Followers string `json:"followers"`
	// AttributedTo names the ACCOUNT that owns a Group actor. PeerTube requires
	// it on every channel actor and vidra emits it, so on the fediverse vidra
	// actually federates with, a channel's owner is stated in its own document.
	// It is the edge a block of an account travels down to reach that account's
	// channels (0142); the rehearsal measured what its absence costs — a viewer
	// blocking @name@domain saw nothing change, because the videos are
	// attributed to the Group and the block named the Person.
	AttributedTo json.RawMessage `json:"attributedTo"`
	// AlsoKnownAs lists the actor's other identities. Read only to verify an
	// inbound Move: the target must name the actor that moved to it.
	AlsoKnownAs json.RawMessage `json:"alsoKnownAs"`
	PublicKey   struct {
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
	ra, err := s.resolveActor(ctx, actorURL, true)
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
// It is bounded twice: only a request that already failed against a CACHED key
// reaches it, so an actor this instance has never seen cannot trigger a fetch
// through this path; and a cached actor is re-fetched at most once per
// actorFetchCooldown, however many failed signatures name it (G13).
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
	return s.resolveActor(ctx, actorURL, false)
}

// resolveActor is resolveRemoteActor with the caller named. inbound marks the
// inbound-signature path (ResolveKey), the only one an unauthenticated request
// drives, and the only one the negative cache applies to: a failure there must
// not stop a local follow, search, delivery or instance follow from fetching
// the same actor a moment later.
func (s *Service) resolveActor(ctx context.Context, actorURL string, inbound bool) (sqlcgen.RemoteActor, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	if _, err := guard.ValidateURL(actorURL); err != nil {
		return sqlcgen.RemoteActor{}, fmt.Errorf("federation: unsafe actor URL: %w", err)
	}
	if ra, err := s.repo.GetRemoteActor(ctx, actorURL); err == nil {
		return ra, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.RemoteActor{}, err
	}
	// A URL that failed to resolve is not fetched again until the cooldown
	// passes: an unseen keyId otherwise costs one outbound GET per request.
	if inbound && s.actorFetches.recentlyFailed(actorURL) {
		return sqlcgen.RemoteActor{}, errActorRecentlyFailed
	}
	ra, err := s.fetchMissingActor(ctx, actorURL, inbound)
	// A request the caller abandoned is not the actor failing, a blocked host
	// or a failed blocklist read fetched nothing, and neither did a spent
	// budget; none of them is remembered.
	if err != nil && inbound && ctx.Err() == nil && !errors.Is(err, ErrActorHostBlocked) &&
		!errors.Is(err, errBlocklistLookup) && !errors.Is(err, errInboundFetchBudget) {
		s.actorFetches.recordFailure(actorURL)
	}
	return ra, err
}

// admitInboundFetch admits one inbound-signature-path actor fetch against the
// per-host budget and the global in-flight cap (actorFetchGate.admitInbound).
// Whether the actor is followed is asked only when a limit would refuse it,
// and the lookup is bounded: a slow answer counts as "not followed".
func (s *Service) admitInboundFetch(ctx context.Context, actorURL string) (func(), error) {
	u, err := url.Parse(actorURL)
	if err != nil {
		return nil, err
	}
	host, ok := NormalizeFollowHost(u.Host)
	if !ok {
		return nil, errInboundFetchBudget
	}
	return s.actorFetches.admitInbound(host, s.followedLookup(ctx, actorURL))
}

// followedLookup is the bounded "is this actor followed?" question the fetch
// limits ask before refusing; a slow or failed answer counts as not followed.
func (s *Service) followedLookup(ctx context.Context, actorURL string) func() bool {
	return func() bool {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), followedLookupTimeout)
		defer cancel()
		ok, err := s.hasAcceptedFollowEdge(fctx, actorURL)
		return err == nil && ok
	}
}

// fetchMissingActor fetches and caches an actor that is not cached, sharing
// one fetch among concurrent callers for the same URL. The shared fetch is
// detached from any one caller's request (bounded by remoteFetchTimeout), so
// the first caller hanging up does not fail the rest; a caller that hangs up
// stops waiting.
//
// An inbound-path fetch is also admitted against the per-host budget
// (admitInbound). It shares its flight only with other inbound callers, so a
// spent budget never fails a local user's resolution of the same actor.
func (s *Service) fetchMissingActor(ctx context.Context, actorURL string, inbound bool) (sqlcgen.RemoteActor, error) {
	if s.actorFetches == nil {
		return s.refetchRemoteActor(ctx, actorURL)
	}
	key := actorURL
	if inbound {
		key = "inbound " + actorURL
	}
	ch := s.actorFetches.misses.DoChan(key, func() (v any, err error) {
		// DoChan re-raises a panic on a goroutine of its own, out of reach of
		// any request handler's recover; turn it into this fetch's error.
		defer func() {
			if r := recover(); r != nil {
				v, err = sqlcgen.RemoteActor{}, fmt.Errorf("federation: actor fetch panicked: %v", r)
			}
		}()
		if inbound {
			release, err := s.admitInboundFetch(ctx, actorURL)
			if err != nil {
				return sqlcgen.RemoteActor{}, err
			}
			defer release()
		}
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), remoteFetchTimeout)
		defer cancel()
		return s.refetchRemoteActor(fctx, actorURL)
	})
	select {
	case <-ctx.Done():
		return sqlcgen.RemoteActor{}, ctx.Err()
	case r := <-ch:
		ra, _ := r.Val.(sqlcgen.RemoteActor)
		return ra, r.Err
	}
}

// errActorRecentlyFailed: the actor URL failed to resolve within the cooldown.
var errActorRecentlyFailed = errors.New("federation: actor recently failed to resolve")

// errActorRefreshCooldown: the cached actor's key was re-fetched within the
// cooldown, so it is not fetched again yet.
var errActorRefreshCooldown = errors.New("federation: actor key refreshed recently")

// errBlocklistLookup: the instance blocklist could not be read, so nothing was
// fetched. It is this instance's failure, not the actor's, and is not
// remembered by the negative cache.
var errBlocklistLookup = errors.New("federation: instance blocklist lookup failed")

// ErrActorHostBlocked: the actor lives on an admin-blocked instance, and was
// not fetched. The inbox answers it with the same 202 any activity from a
// blocked instance gets, and nothing else.
var ErrActorHostBlocked = errors.New("federation: actor host is blocked")

// ResolveRemoteActor is resolveRemoteActor for callers outside federation: the
// cached actor, or one fetched now through the SSRF guard and cached. It is how
// the PeerTube importer learns a follower's inbox and key — from the actor's own
// document, never from the source database's copy of it.
func (s *Service) ResolveRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	return s.resolveRemoteActor(ctx, actorURL)
}

// InboxesOnActorHost reports whether an actor's inbox, and its shared inbox
// when it names one, live on the actor's own host. cacheFetchedActor already
// refuses to store any other inbox; the delivery paths that fan out on a
// follower's say-so re-check the row as it is now, because it is rewritten
// whenever the actor's key rotates.
func InboxesOnActorHost(ra sqlcgen.RemoteActor) bool {
	if !sameHost(ra.InboxUrl, ra.ActorUrl) {
		return false
	}
	return ra.SharedInboxUrl == nil || *ra.SharedInboxUrl == "" || sameHost(*ra.SharedInboxUrl, ra.ActorUrl)
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
	// A refresh runs within the same global in-flight cap as inbound misses
	// (a followed actor's is admitted past it). It is taken BEFORE the
	// cooldown is consulted, so a refusal here leaves the cooldown unspent.
	if s.actorFetches != nil {
		release, err := s.actorFetches.admitInflight(s.followedLookup(ctx, actorURL))
		if err != nil {
			return sqlcgen.RemoteActor{}, err
		}
		defer release()
	}
	// One re-fetch per actor per cooldown, however many failed signatures
	// name it. The first failure after a quiet period still re-fetches, which
	// is the rotation case this path exists for.
	if !s.actorFetches.allowRefresh(actorURL) {
		return sqlcgen.RemoteActor{}, errActorRefreshCooldown
	}
	// The slot is spent once allowRefresh says yes, so the fetch it pays for
	// must run to completion whether or not the requester is still there. It
	// is detached from the request and bounded by the fetch timeout instead.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), remoteFetchTimeout)
	defer cancel()
	return s.refetchRemoteActor(fctx, actorURL)
}

// refetchRemoteActor fetches an actor document and upserts the cache row.
//
// The instance blocklist is consulted HERE, before the fetch, not only after
// signature verification: verification is what fetches, so a check after it
// still let a blocked host be contacted on every inbound request naming it.
func (s *Service) refetchRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	u, err := url.Parse(actorURL)
	if err != nil {
		return sqlcgen.RemoteActor{}, fmt.Errorf("federation: parse actor URL: %w", err)
	}
	if blocked, err := s.hostBlocked(ctx, u); err != nil {
		return sqlcgen.RemoteActor{}, fmt.Errorf("%w: %w", errBlocklistLookup, err)
	} else if blocked {
		return sqlcgen.RemoteActor{}, ErrActorHostBlocked
	}
	fa, err := s.fetchActor(ctx, actorURL)
	if err != nil {
		return sqlcgen.RemoteActor{}, err
	}
	return s.cacheFetchedActor(ctx, actorURL, fa)
}

// cacheFetchedActor upserts the cache row for an actor document just fetched.
func (s *Service) cacheFetchedActor(ctx context.Context, actorURL string, fa fetchedActor) (sqlcgen.RemoteActor, error) {
	// Only SAME-HOST inboxes are stored. Every Accept, Reject, follow-back and
	// channel fan-out this instance signs is POSTed to them, and the document
	// naming them is attacker-controlled text: an actor on one server naming
	// another server's inbox would turn this instance into a signed-POST
	// relay at a host that never federated with it. A dropped inbox reads as
	// "no inbox", which every delivery path already treats as nowhere to send.
	// The key is kept: verifying the actor does not depend on where it receives.
	inbox := fa.Inbox
	if !sameHost(inbox, actorURL) {
		inbox = ""
	}
	var sharedInbox *string
	if fa.Endpoints.SharedInbox != "" && sameHost(fa.Endpoints.SharedInbox, actorURL) {
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
		InboxUrl:          inbox,
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
	client := s.remoteClient(guard)
	req, err := http.NewRequestWithContext(withSameHostRedirects(ctx), http.MethodGet, target.String(), nil)
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
	// The document is cached under actorURL, so it must speak for that host.
	// Redirects cannot leave the host (withSameHostRedirects), which makes the
	// requested host and the answering host one and the same.
	if !sameHost(fa.ID, target.String()) {
		return fetchedActor{}, errors.New("federation: actor id is not on the fetched host")
	}
	if strings.TrimSpace(fa.PublicKey.PublicKeyPem) == "" {
		return fetchedActor{}, errors.New("federation: remote actor has no public key")
	}
	// A key outside policy is refused here, before the document is cached, so
	// it never becomes an actor key anything else trusts.
	if _, err := parseRSAPublicKey(fa.PublicKey.PublicKeyPem); errors.Is(err, httpsig.ErrKeyPolicy) {
		return fetchedActor{}, err
	}
	return fa, nil
}

// parseRSAPublicKey parses a PEM SPKI public key and asserts it is RSA and
// within httpsig's key policy — checked at parse, before any verification.
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
	if err := httpsig.CheckKeyPolicy(rp); err != nil {
		return nil, fmt.Errorf("federation: %w", err)
	}
	return rp, nil
}
