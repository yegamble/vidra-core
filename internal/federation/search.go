package federation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Remote-URI search (config-parity W13, gap-matrix §10): a URI- or
// handle-shaped search query is resolved to remote content through the SAME
// machinery the follow flow uses — WebFinger (webFingerRemote), actor
// resolution + cache (resolveRemoteActor), and object fetch (fetchVideoObject)
// — so every outbound request runs through the SSRF-guarded fetcher
// (urlsafety.Guard; never a raw http.Get). The caller (the search handler)
// supplies a tight deadline and rate-limits per caller; any failure here is a
// silent local-only degradation, never a search error.
//
// Deviations, documented: a LOCAL URL/handle is not resolved to the local
// object (PeerTube does; vidra's local search runs alongside anyway), and a
// search-resolved remote video is stored via the ingestion upsert WITHOUT the
// inbox's accepted-follow anti-spam gate — that gate exists to refuse
// unsolicited PUSHES, while this fetch is explicitly requested by an allowed,
// rate-limited local caller (mirroring FollowRemoteChannel's actor fetch).

// SearchQueryKind classifies a search query's shape.
type SearchQueryKind int

const (
	// SearchQueryNone is ordinary text — no remote resolution is attempted.
	SearchQueryNone SearchQueryKind = iota
	// SearchQueryURI is an absolute http(s) URL.
	SearchQueryURI
	// SearchQueryHandle is a fediverse handle: user@host or @user@host.
	SearchQueryHandle
)

// Search result kinds for resolved actors (the item's `type` on the wire).
const (
	SearchResultVideo   = "video"
	SearchResultChannel = "channel" // Group actors
	SearchResultAccount = "account" // Person (and other non-Group) actors
)

// ErrNotSearchTarget means the query is not URI/handle-shaped; callers should
// classify first (ClassifySearchQuery) and skip resolution entirely.
var ErrNotSearchTarget = errors.New("federation: query is not a remote search target")

// ClassifySearchQuery reports whether a search query is URI-shaped (an
// absolute http(s) URL), handle-shaped (user@host / @user@host, where the host
// part has a dot or an explicit :port — bare single-label "hosts" are ordinary
// text far more often than fediverse handles), or plain text.
func ClassifySearchQuery(q string) SearchQueryKind {
	q = strings.TrimSpace(q)
	if q == "" {
		return SearchQueryNone
	}
	if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
		if strings.ContainsAny(q, " \t") {
			return SearchQueryNone
		}
		if u, err := url.Parse(q); err == nil && u.Host != "" {
			return SearchQueryURI
		}
		return SearchQueryNone
	}
	name, domain, ok := strings.Cut(strings.TrimPrefix(q, "@"), "@")
	if !ok || name == "" || domain == "" {
		return SearchQueryNone
	}
	if strings.ContainsAny(name, "/ \t") || strings.ContainsAny(domain, "/@ \t") {
		return SearchQueryNone
	}
	if !strings.Contains(domain, ".") && !strings.Contains(domain, ":") {
		return SearchQueryNone
	}
	return SearchQueryHandle
}

// SearchResolvedActor is a remote channel/account resolved from a search query.
type SearchResolvedActor struct {
	ActorURL string
	Handle   string // preferredUsername@domain (the followable identity)
	Domain   string
	Kind     string // SearchResultChannel | SearchResultAccount
}

// SearchResolution is the outcome of resolving one URI/handle-shaped query:
// exactly one of VideoID (a stored remote_videos row, served by the
// remote-video read surface) or Actor is set.
type SearchResolution struct {
	VideoID uuid.UUID
	Actor   *SearchResolvedActor
}

// ResolveSearchTarget resolves a URI- or handle-shaped search query to remote
// content. Handles WebFinger to an actor; URIs are fetched as ActivityPub
// objects — a Video object is ingested (owner actor resolved + cached first,
// same authority rules as an Announce: object and owner share the origin) and
// an actor document is resolved through the actor cache. The domain blocklist
// is consulted BEFORE any fetch; local targets are refused (vidra does not
// self-resolve — local search already covers local content). Every fetch is
// SSRF-guarded and bounded by ctx (callers pass a tight deadline).
func (s *Service) ResolveSearchTarget(ctx context.Context, query string) (SearchResolution, error) {
	query = strings.TrimSpace(query)
	switch ClassifySearchQuery(query) {
	case SearchQueryHandle:
		name, domain, _ := strings.Cut(strings.TrimPrefix(query, "@"), "@")
		if strings.EqualFold(domain, s.domain()) {
			return SearchResolution{}, ErrLocalFollowTarget
		}
		if err := s.refuseBlockedDomain(ctx, strings.ToLower(domain)); err != nil {
			return SearchResolution{}, err
		}
		actorURL, err := s.webFingerRemote(ctx, name, domain)
		if err != nil {
			return SearchResolution{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
		}
		return s.searchActorResult(ctx, actorURL)

	case SearchQueryURI:
		host := hostOf(query)
		if host == "" {
			return SearchResolution{}, ErrNotSearchTarget
		}
		if strings.EqualFold(host, s.domain()) {
			return SearchResolution{}, ErrLocalFollowTarget
		}
		if err := s.refuseBlockedDomain(ctx, host); err != nil {
			return SearchResolution{}, err
		}
		obj, err := s.fetchVideoObject(ctx, query)
		if err != nil {
			return SearchResolution{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
		}
		switch obj.Type {
		case "Video":
			// Authority (mirrors handleAnnounce): the object's canonical id must
			// live on the queried origin and its owner actor must share it.
			if obj.ID == "" || !sameHost(obj.ID, query) {
				return SearchResolution{}, ErrRemoteUnresolvable
			}
			owner := firstAttributedTo(obj.AttributedTo)
			if owner == "" || !sameHost(owner, obj.ID) {
				return SearchResolution{}, ErrRemoteUnresolvable
			}
			// Resolve + cache the owner first (the remote_videos FK target).
			if _, err := s.resolveRemoteActor(ctx, owner); err != nil {
				return SearchResolution{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
			}
			row, err := s.storeRemoteVideo(ctx, owner, obj)
			if err != nil {
				return SearchResolution{}, err
			}
			return SearchResolution{VideoID: row.ID}, nil
		case "Person", "Group", "Organization", "Service", "Application":
			// The queried URL answered with an actor document; resolve it via the
			// cache (preferring its canonical same-origin id).
			actorURL := query
			if obj.ID != "" && sameHost(obj.ID, query) {
				actorURL = obj.ID
			}
			return s.searchActorResult(ctx, actorURL)
		}
		return SearchResolution{}, ErrRemoteUnresolvable
	}
	return SearchResolution{}, ErrNotSearchTarget
}

// searchActorResult resolves (and caches) a remote actor and shapes it as a
// search result. Group actors are channels; everything else is an account.
func (s *Service) searchActorResult(ctx context.Context, actorURL string) (SearchResolution, error) {
	if strings.EqualFold(hostOf(actorURL), s.domain()) {
		return SearchResolution{}, ErrLocalFollowTarget
	}
	ra, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return SearchResolution{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
	}
	kind := SearchResultAccount
	if ra.ActorType == "Group" {
		kind = SearchResultChannel
	}
	return SearchResolution{Actor: &SearchResolvedActor{
		ActorURL: ra.ActorUrl,
		Handle:   ra.PreferredUsername + "@" + ra.Domain,
		Domain:   ra.Domain,
		Kind:     kind,
	}}, nil
}

// refuseBlockedDomain refuses resolution toward an admin-blocked domain before
// any outbound fetch happens (defense in depth; the read surfaces filter too).
func (s *Service) refuseBlockedDomain(ctx context.Context, domain string) error {
	blocked, err := s.repo.IsInstanceBlocked(ctx, domain)
	if err != nil {
		return err
	}
	if blocked {
		return ErrRemoteUnresolvable
	}
	return nil
}
