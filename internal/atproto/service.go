// Package atproto implements the Vidra ATProto / Bluesky extension (P10.2), v1:
// OUTBOUND cross-posting only. A creator links a Bluesky account (handle + app
// password) and, when auto_post is on, Vidra announces their newly published
// PUBLIC videos on Bluesky. There is no PDS hosting and no inbound firehose —
// those are out of scope until a future spec.
//
// This package ALSO provides the protocol client for ATProto identity login
// (resolve.go, oauth_client.go, dpop.go): handle→DID→PDS resolution with
// bidirectional verification, auth-server discovery, and the public-client
// PAR/PKCE/DPoP OAuth handshake. That client is HTTP-agnostic and SSRF-guarded;
// the login ladder that turns a verified DID into a Vidra session lives in
// internal/auth (ATProtoOAuthService) and the transport in internal/httpapi.
// Identity login is gated independently by ATPROTO_LOGIN_ENABLED and, unlike
// cross-posting, keeps NO tokens: the PDS tokens are discarded once the returned
// DID is verified.
//
// The feature is gated by ATPROTO_ENABLED (default false) and is independent of
// ActivityPub. The linked app password is sealed at rest with secretbox
// (internal/secretbox) under ATPROTO_KEY_KEK (falling back to FEDERATION_KEY_KEK);
// it is NEVER returned by the API, logged, span-tagged, or exported. Links verify
// credentials against the PDS through an SSRF-guarded, https+public client, and
// auto-posts drain from a durable queue (migration 0066) on a ticker, mirroring
// the federation delivery worker's retry/backoff/dead-letter shape.
package atproto

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// DefaultPDSURL is the PDS used when a creator does not supply one.
const DefaultPDSURL = "https://bsky.social"

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrDisabled means the ATProto extension is off on this instance
	// (ATPROTO_ENABLED=false); link/status/unlink answer 503.
	ErrDisabled = errors.New("atproto: extension is disabled")
	// ErrNotLinked means the caller has no linked Bluesky account (status → 404).
	ErrNotLinked = errors.New("atproto: no linked account")
	// ErrInvalidHandle means the supplied handle is malformed (→ 422).
	ErrInvalidHandle = errors.New("atproto: invalid handle")
	// ErrMissingCredentials means handle or app password was empty (→ 422).
	ErrMissingCredentials = errors.New("atproto: handle and app password are required")
	// ErrInvalidPDS means the PDS URL is not an https, public, fetchable origin
	// (→ 422).
	ErrInvalidPDS = errors.New("atproto: PDS URL must be an https public origin")
	// ErrInvalidCredentials means the PDS rejected the handle/app-password pair
	// (→ 422). The underlying PDS response body is never surfaced.
	ErrInvalidCredentials = errors.New("atproto: the PDS rejected these credentials")
	// ErrPDSUnreachable means the PDS could not be reached or errored (→ 502).
	ErrPDSUnreachable = errors.New("atproto: could not reach the PDS")
)

// Repository is the data access the service needs. *sqlcgen.Queries satisfies it
// directly; tests substitute an in-memory fake.
type Repository interface {
	UpsertATProtoAccount(ctx context.Context, arg sqlcgen.UpsertATProtoAccountParams) (sqlcgen.AtprotoAccount, error)
	GetATProtoAccount(ctx context.Context, userID uuid.UUID) (sqlcgen.AtprotoAccount, error)
	DeleteATProtoAccount(ctx context.Context, userID uuid.UUID) error
	TouchATProtoAccountPosted(ctx context.Context, userID uuid.UUID) error
	EnqueueATProtoPost(ctx context.Context, videoID uuid.UUID) error
	ClaimDueATProtoPosts(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueATProtoPostsRow, error)
	MarkATProtoPostDone(ctx context.Context, arg sqlcgen.MarkATProtoPostDoneParams) error
	RescheduleATProtoPost(ctx context.Context, arg sqlcgen.RescheduleATProtoPostParams) error
	FailATProtoPost(ctx context.Context, arg sqlcgen.FailATProtoPostParams) error
	GetATProtoPostVideo(ctx context.Context, id uuid.UUID) (sqlcgen.GetATProtoPostVideoRow, error)
}

// Thumbnails optionally provides a video's poster image for the Bluesky embed.
// *video.Service (via a thin adapter) satisfies it; nil means no thumbnail is
// attached. Implementations bound the size themselves; the worker additionally
// enforces the ≤1 MiB blob cap.
type Thumbnails interface {
	// ReadThumbnail returns a video's poster image bytes and mime type, or
	// ok=false when none exists.
	ReadThumbnail(ctx context.Context, videoID uuid.UUID) (data []byte, mime string, ok bool, err error)
}

// Service holds the ATProto cross-posting logic. It is HTTP-agnostic and testable
// with a fake repository + a fake PDS client.
type Service struct {
	repo         Repository
	cipher       *secretbox.Cipher // nil in dev → passwords stored raw
	pds          PDSClient
	thumbs       Thumbnails
	enabled      bool
	allowPrivate bool   // dev/test: relax the PDS SSRF guard (loopback/http)
	baseURL      string // public origin used to build watch URLs (no trailing slash)
	logger       *slog.Logger
	now          func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithCipher wires the secretbox cipher that seals app passwords at rest. Without
// it (dev, no KEK), passwords are stored raw — cmd/api warns loudly.
func WithCipher(c *secretbox.Cipher) Option {
	return func(s *Service) { s.cipher = c }
}

// WithEnabled sets whether the extension is on (ATPROTO_ENABLED). When false,
// Link/Status/Unlink return ErrDisabled, EnqueueForVideo is a no-op, and
// DrainPosts does nothing.
func WithEnabled(enabled bool) Option {
	return func(s *Service) { s.enabled = enabled }
}

// WithBaseURL sets the public origin used to build the watch URL embedded in the
// Bluesky post (e.g. https://videos.example). Trailing slashes are trimmed.
func WithBaseURL(u string) Option {
	return func(s *Service) { s.baseURL = strings.TrimRight(u, "/") }
}

// WithPDSClient overrides the PDS XRPC client (tests inject a fake; the link
// handler test uses the real client against an httptest server).
func WithPDSClient(c PDSClient) Option {
	return func(s *Service) {
		if c != nil {
			s.pds = c
		}
	}
}

// WithThumbnails wires the optional poster-image source for the post embed.
func WithThumbnails(t Thumbnails) Option {
	return func(s *Service) { s.thumbs = t }
}

// WithAllowPrivateFetch relaxes the PDS SSRF guard so the default HTTP client may
// reach loopback/private origins over http. DEVELOPMENT/TEST-ONLY (mirrors the
// federation/import knob); NEVER enable it in production. Ignored when a PDS
// client is supplied via WithPDSClient.
func WithAllowPrivateFetch(allow bool) Option {
	return func(s *Service) { s.allowPrivate = allow }
}

// WithLogger overrides the logger used for best-effort/unexpected errors.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithClock overrides the time source (tests use it for deterministic post
// timestamps and backoff). The default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// NewService builds the ATProto service. When no PDS client is supplied it
// defaults to the SSRF-guarded HTTP client (production wiring); tests pass a fake.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{
		repo:   repo,
		logger: slog.Default(),
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.pds == nil {
		s.pds = NewHTTPClient(WithClientAllowPrivate(s.allowPrivate))
	}
	return s
}

// Enabled reports whether the ATProto extension is turned on.
func (s *Service) Enabled() bool { return s.enabled }

// LinkInput is the validated PUT /me/atproto body.
type LinkInput struct {
	Handle      string
	AppPassword string
	PDSURL      string // optional; defaults to DefaultPDSURL
	AutoPost    bool
}

// Link verifies a creator's Bluesky credentials against their PDS and stores the
// linked account (app password sealed). It creates a session via
// com.atproto.server.createSession to both prove the credentials and resolve the
// DID, then upserts the row. Re-linking overwrites the existing link.
func (s *Service) Link(ctx context.Context, userID uuid.UUID, in LinkInput) (sqlcgen.AtprotoAccount, error) {
	if !s.enabled {
		return sqlcgen.AtprotoAccount{}, ErrDisabled
	}
	handle := normalizeHandle(in.Handle)
	appPassword := strings.TrimSpace(in.AppPassword)
	if handle == "" || appPassword == "" {
		return sqlcgen.AtprotoAccount{}, ErrMissingCredentials
	}
	if !validHandle(handle) {
		return sqlcgen.AtprotoAccount{}, ErrInvalidHandle
	}
	pdsURL := strings.TrimRight(strings.TrimSpace(in.PDSURL), "/")
	if pdsURL == "" {
		pdsURL = DefaultPDSURL
	}

	sess, err := s.pds.CreateSession(ctx, pdsURL, handle, appPassword)
	if err != nil {
		return sqlcgen.AtprotoAccount{}, mapLinkError(err)
	}
	if strings.TrimSpace(sess.DID) == "" {
		return sqlcgen.AtprotoAccount{}, ErrPDSUnreachable
	}

	sealed, err := s.sealPassword(appPassword)
	if err != nil {
		return sqlcgen.AtprotoAccount{}, err
	}
	// Prefer the canonical handle the PDS reports, falling back to the input.
	storedHandle := handle
	if h := normalizeHandle(sess.Handle); h != "" {
		storedHandle = h
	}
	return s.repo.UpsertATProtoAccount(ctx, sqlcgen.UpsertATProtoAccountParams{
		UserID:            userID,
		Handle:            storedHandle,
		Did:               strings.TrimSpace(sess.DID),
		PdsUrl:            pdsURL,
		AppPasswordSealed: sealed,
		AutoPost:          in.AutoPost,
	})
}

// Status returns the caller's linked account (for the status view). ErrNotLinked
// when none exists. The caller (HTTP layer) must never expose the sealed
// password field.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (sqlcgen.AtprotoAccount, error) {
	if !s.enabled {
		return sqlcgen.AtprotoAccount{}, ErrDisabled
	}
	acct, err := s.repo.GetATProtoAccount(ctx, userID)
	if err != nil {
		return sqlcgen.AtprotoAccount{}, ErrNotLinked
	}
	return acct, nil
}

// Unlink removes the caller's linked account. Idempotent: unlinking when nothing
// is linked is a no-op success.
func (s *Service) Unlink(ctx context.Context, userID uuid.UUID) error {
	if !s.enabled {
		return ErrDisabled
	}
	return s.repo.DeleteATProtoAccount(ctx, userID)
}

// EnqueueForVideo is the publish-hook entry point: on the published transition of
// a PUBLIC video whose owner has auto_post on, it enqueues exactly one auto-post
// (video_id is UNIQUE, so a repeat is a no-op). It is best-effort and never
// blocks the publish; the caller logs any error. A no-op when disabled, when the
// video is not public, when the owner has no linked account, or when auto_post is
// off.
func (s *Service) EnqueueForVideo(ctx context.Context, videoID uuid.UUID) error {
	if !s.enabled {
		return nil
	}
	info, err := s.repo.GetATProtoPostVideo(ctx, videoID)
	if err != nil {
		return nil // video gone or not found — nothing to post
	}
	if info.Privacy != "public" {
		return nil
	}
	acct, err := s.repo.GetATProtoAccount(ctx, info.OwnerID)
	if err != nil {
		return nil // owner has not linked a Bluesky account
	}
	if !acct.AutoPost {
		return nil
	}
	return s.repo.EnqueueATProtoPost(ctx, videoID)
}

// sealPassword returns the at-rest form of an app password: sealed when a cipher
// is configured, otherwise the raw value (dev-only).
func (s *Service) sealPassword(plain string) (string, error) {
	if s.cipher == nil {
		return plain, nil
	}
	return s.cipher.Seal([]byte(plain))
}

// openPassword reverses sealPassword: it opens a sealed value, or returns a raw
// (dev) value unchanged.
func (s *Service) openPassword(stored string) (string, error) {
	if secretbox.IsSealed(stored) {
		if s.cipher == nil {
			return "", errors.New("atproto: app password is sealed but no ATPROTO_KEY_KEK is configured")
		}
		raw, err := s.cipher.Open(stored)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return stored, nil
}

// normalizeHandle lowercases, trims, and strips a leading @ from a handle.
func normalizeHandle(h string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "@")
}

// validHandle is a permissive check: a domain-shaped handle (letters, digits,
// dots, dashes; at least one dot; no spaces). It does not resolve the handle —
// createSession is the real proof.
func validHandle(h string) bool {
	if len(h) < 3 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return !strings.HasPrefix(h, ".") && !strings.HasSuffix(h, ".")
}

// mapLinkError maps a PDS client error to a service sentinel: an auth rejection
// (400/401) becomes ErrInvalidCredentials, everything else ErrPDSUnreachable.
func mapLinkError(err error) error {
	if errors.Is(err, ErrInvalidPDS) {
		return ErrInvalidPDS
	}
	var xe *XRPCError
	if errors.As(err, &xe) && (xe.Status == 400 || xe.Status == 401) {
		return ErrInvalidCredentials
	}
	return ErrPDSUnreachable
}
