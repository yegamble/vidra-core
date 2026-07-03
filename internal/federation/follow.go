package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Outbound remote-channel follows (.ralph/specs/federation-remote-content.md §3):
// a LOCAL user follows a REMOTE channel. The UI submits `name@domain` (or a full
// actor URL) → WebFinger on the remote domain (SSRF-guarded) → resolve + cache
// the actor → insert a pending remote_channel_follows row → enqueue a signed
// `Follow` AS THE USER'S ACCOUNT ACTOR via the delivery queue. An inbound
// Accept{Follow} (matched by follow_activity_url, signed by the followed actor)
// flips the row to accepted; a Reject deletes it. Unfollow enqueues Undo{Follow}
// and deletes the row immediately (local intent wins).

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrBadFollowTarget means the submitted handle/actor URL is syntactically
	// invalid (422).
	ErrBadFollowTarget = errors.New("federation: invalid follow target")
	// ErrLocalFollowTarget means the target actor lives on THIS instance — local
	// channels are followed via POST /channels/{handle}/follow, not here (422).
	ErrLocalFollowTarget = errors.New("federation: target is a local actor")
	// ErrRemoteUnresolvable means WebFinger or the actor fetch failed, so the
	// target could not be resolved to a followable remote actor (422).
	ErrRemoteUnresolvable = errors.New("federation: could not resolve the remote actor")
	// ErrFollowNotFound means no such remote follow exists for this user (404).
	ErrFollowNotFound = errors.New("federation: remote follow not found")
)

// maxWebFingerBytes bounds a WebFinger JRD response.
const maxWebFingerBytes = 64 << 10 // 64 KiB

// RemoteFollow is the read model of one outbound remote-channel follow.
type RemoteFollow struct {
	ID        uuid.UUID
	ActorURL  string
	Handle    string // preferred_username@domain (the followable identity)
	Domain    string
	State     string // pending | accepted
	CreatedAt time.Time
}

// FollowRemoteChannel follows a remote channel as the given local user. Target
// is either a `name@domain` handle (leading @ tolerated) or a full actor URL.
// Idempotent: re-following returns the existing row without enqueueing a second
// Follow. The outbound Follow is signed as the user's ACCOUNT actor, whose
// keypair is minted here (before enqueue) so delivery never signs with a
// missing key.
func (s *Service) FollowRemoteChannel(ctx context.Context, userID uuid.UUID, target string) (RemoteFollow, error) {
	u, err := s.repo.GetUserActorByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RemoteFollow{}, ErrNotFound
		}
		return RemoteFollow{}, err
	}

	actorURL, err := s.resolveFollowTarget(ctx, target)
	if err != nil {
		return RemoteFollow{}, err
	}
	if strings.EqualFold(hostOf(actorURL), s.domain()) {
		return RemoteFollow{}, ErrLocalFollowTarget
	}
	ra, err := s.resolveRemoteActor(ctx, actorURL)
	if err != nil {
		return RemoteFollow{}, fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
	}

	// Mint the account keypair up front: the queued Follow is signed as this
	// account at send time.
	if _, err := s.ensureAccountKey(ctx, userID); err != nil {
		return RemoteFollow{}, err
	}

	accountActor := s.baseURL + "/accounts/" + u.Username
	followID := accountActor + "/activities/follow/" + uuid.NewString()
	row, err := s.repo.UpsertRemoteChannelFollow(ctx, sqlcgen.UpsertRemoteChannelFollowParams{
		UserID:            userID,
		RemoteActorUrl:    ra.ActorUrl,
		FollowActivityUrl: followID,
	})
	if err != nil {
		return RemoteFollow{}, err
	}
	// A fresh insert kept our minted activity id; an existing row kept its own
	// (idempotent re-follow → no second Follow is sent).
	if row.FollowActivityUrl == followID {
		inbox := ra.InboxUrl
		if ra.SharedInboxUrl != nil && *ra.SharedInboxUrl != "" {
			inbox = *ra.SharedInboxUrl
		}
		if inbox == "" {
			return RemoteFollow{}, ErrRemoteUnresolvable
		}
		payload, err := buildFollow(followID, accountActor, ra.ActorUrl)
		if err != nil {
			return RemoteFollow{}, err
		}
		if err := s.enqueueAccountDelivery(ctx, userID, u.Username, inbox, payload); err != nil {
			return RemoteFollow{}, err
		}
	}
	return RemoteFollow{
		ID:        row.ID,
		ActorURL:  row.RemoteActorUrl,
		Handle:    ra.PreferredUsername + "@" + ra.Domain,
		Domain:    ra.Domain,
		State:     row.State,
		CreatedAt: row.CreatedAt,
	}, nil
}

// UnfollowRemoteChannel removes the user's remote follow by row id: the row is
// deleted immediately (local intent wins) and a signed Undo{Follow} is queued
// to the remote inbox. Unknown/foreign id → ErrFollowNotFound.
func (s *Service) UnfollowRemoteChannel(ctx context.Context, userID, followID uuid.UUID) error {
	u, err := s.repo.GetUserActorByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFollowNotFound
		}
		return err
	}
	row, err := s.repo.GetRemoteChannelFollowByID(ctx, sqlcgen.GetRemoteChannelFollowByIDParams{
		ID:     followID,
		UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFollowNotFound
		}
		return err
	}
	n, err := s.repo.DeleteRemoteChannelFollowByID(ctx, sqlcgen.DeleteRemoteChannelFollowByIDParams{
		ID:     followID,
		UserID: userID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFollowNotFound
	}
	// Best-effort Undo: the local row is already gone; without a known inbox
	// there is nowhere to send the Undo.
	if row.InboxUrl == "" {
		return nil
	}
	accountActor := s.baseURL + "/accounts/" + u.Username
	payload, err := buildUndoFollow(accountActor, row.FollowActivityUrl, row.RemoteActorUrl)
	if err != nil {
		return err
	}
	return s.enqueueAccountDelivery(ctx, userID, u.Username, row.InboxUrl, payload)
}

// ListRemoteFollows returns the user's outbound remote-channel follows, newest
// first. The caller clamps limit/offset.
func (s *Service) ListRemoteFollows(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]RemoteFollow, error) {
	rows, err := s.repo.ListRemoteChannelFollows(ctx, sqlcgen.ListRemoteChannelFollowsParams{
		UserID:       userID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]RemoteFollow, 0, len(rows))
	for _, r := range rows {
		out = append(out, RemoteFollow{
			ID:        r.ID,
			ActorURL:  r.RemoteActorUrl,
			Handle:    r.PreferredUsername + "@" + r.Domain,
			Domain:    r.Domain,
			State:     r.State,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// resolveFollowTarget turns the submitted target (a full actor URL, or a
// name@domain handle resolved via WebFinger) into an actor URL.
func (s *Service) resolveFollowTarget(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ErrBadFollowTarget
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" {
			return "", ErrBadFollowTarget
		}
		return target, nil
	}
	name, domain, ok := strings.Cut(strings.TrimPrefix(target, "@"), "@")
	if !ok || name == "" || domain == "" || strings.ContainsAny(domain, "/@ ") {
		return "", ErrBadFollowTarget
	}
	actorURL, err := s.webFingerRemote(ctx, name, domain)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRemoteUnresolvable, err)
	}
	return actorURL, nil
}

// webFingerRemote resolves acct:name@domain on the REMOTE domain to its actor
// URL (RFC 7033), through the SSRF guard. It speaks https; when private fetches
// are allowed (dev/e2e against loopback origins) a failed https attempt falls
// back to plain http.
func (s *Service) webFingerRemote(ctx context.Context, name, domain string) (string, error) {
	query := "/.well-known/webfinger?resource=" + url.QueryEscape("acct:"+name+"@"+domain)
	actorURL, err := s.fetchWebFinger(ctx, "https://"+domain+query)
	if err != nil && s.allowPrivateFetch {
		if httpURL, herr := s.fetchWebFinger(ctx, "http://"+domain+query); herr == nil {
			return httpURL, nil
		}
	}
	return actorURL, err
}

// fetchWebFinger GETs and parses one WebFinger JRD document, returning the
// rel=self ActivityPub actor URL.
func (s *Service) fetchWebFinger(ctx context.Context, jrdURL string) (string, error) {
	guard := urlsafety.Guard{AllowPrivate: s.allowPrivateFetch}
	target, err := guard.ValidateURL(jrdURL)
	if err != nil {
		return "", fmt.Errorf("federation: unsafe webfinger URL: %w", err)
	}
	client := s.fetchClient
	if client == nil {
		client = guard.NewClient(remoteFetchTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/jrd+json, application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("federation: webfinger fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("federation: webfinger returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFingerBytes))
	if err != nil {
		return "", err
	}
	var jrd JRD
	if err := json.Unmarshal(body, &jrd); err != nil {
		return "", fmt.Errorf("federation: parse webfinger: %w", err)
	}
	for _, l := range jrd.Links {
		if l.Rel == "self" && strings.Contains(l.Type, "activity+json") && l.Href != "" {
			return l.Href, nil
		}
	}
	return "", errors.New("federation: webfinger has no ActivityPub self link")
}

// buildFollow renders the Follow activity the user's account actor sends to the
// remote channel.
func buildFollow(followID, accountActor, remoteActorURL string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       followID,
		"type":     "Follow",
		"actor":    accountActor,
		"object":   remoteActorURL,
	})
}

// buildUndoFollow renders the Undo{Follow} the user's account actor sends when
// unfollowing (wrapping the original Follow by its id).
func buildUndoFollow(accountActor, followActivityURL, remoteActorURL string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       accountActor + "/activities/undo/" + uuid.NewString(),
		"type":     "Undo",
		"actor":    accountActor,
		"object": map[string]any{
			"id":     followActivityURL,
			"type":   "Follow",
			"actor":  accountActor,
			"object": remoteActorURL,
		},
	})
}

// handleAccept processes an inbound Accept{Follow} of one of OUR outbound
// remote-channel follows: matched by the embedded Follow's id (our
// follow_activity_url); the signer must be the followed actor — enforced both
// here (when the Follow names its object) and by the matching UPDATE's
// remote_actor_url predicate. Anything else is accepted-and-ignored.
func (s *Service) handleAccept(ctx context.Context, act inboxActivity, signerActorURL string) error {
	followURL, ok := acceptedFollowID(act, signerActorURL)
	if !ok {
		return nil
	}
	_, err := s.repo.AcceptRemoteChannelFollowByActivity(ctx, sqlcgen.AcceptRemoteChannelFollowByActivityParams{
		FollowActivityUrl: followURL,
		RemoteActorUrl:    signerActorURL,
	})
	return err
}

// handleReject processes an inbound Reject{Follow}: the mirror of handleAccept,
// deleting the pending follow instead of accepting it.
func (s *Service) handleReject(ctx context.Context, act inboxActivity, signerActorURL string) error {
	followURL, ok := acceptedFollowID(act, signerActorURL)
	if !ok {
		return nil
	}
	_, err := s.repo.DeleteRemoteChannelFollowByActivity(ctx, sqlcgen.DeleteRemoteChannelFollowByActivityParams{
		FollowActivityUrl: followURL,
		RemoteActorUrl:    signerActorURL,
	})
	return err
}

// acceptedFollowID extracts the embedded Follow's id from an Accept/Reject and
// applies the cheap authority checks: the activity's actor must be the request
// signer, the object must be a Follow (or a bare id), and — when the Follow
// names its object — that followed actor must be the signer too.
func acceptedFollowID(act inboxActivity, signerActorURL string) (string, bool) {
	if act.Actor != "" && act.Actor != signerActorURL {
		return "", false
	}
	var inner struct {
		ID     string          `json:"id"`
		Type   string          `json:"type"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(act.Object, &inner); err != nil || inner.ID == "" {
		// A bare string id is acceptable too.
		if id := objectID(act.Object); id != "" {
			return id, true
		}
		return "", false
	}
	if inner.Type != "" && inner.Type != "Follow" {
		return "", false
	}
	if followed := objectID(inner.Object); followed != "" && followed != signerActorURL {
		return "", false
	}
	return inner.ID, true
}
