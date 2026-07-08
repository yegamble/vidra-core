// Package live implements live-stream metadata, stream-key management, and the
// RTMP ingest/replay boundary for vidra-core: a channel owner creates a live
// stream and receives a private stream key (returned once, stored only as a
// SHA-256 hash). The media server flips a stream live/offline through the ingest
// hooks, the api serves live HLS by stream ID, and — when replay is enabled — a
// finished session's recording is republished as a normal on-demand video.
//
// The metadata lifecycle (create / get / list / edit / delete), key
// generation/rotation, and the replay orchestration are all HTTP-agnostic and
// testable without a server or an RTMP stack (the video pipeline and the
// recording store are injected interfaces, faked in tests).
package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no live stream matches the lookup.
	ErrNotFound = errors.New("live: not found")
)

// streamKeyBytes is the entropy of a raw stream key (256 bits).
const streamKeyBytes = 32

// Repository is the data access the live service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateLiveStream(ctx context.Context, arg sqlcgen.CreateLiveStreamParams) (sqlcgen.CreateLiveStreamRow, error)
	GetLiveStreamByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetLiveStreamByIDRow, error)
	ListLiveStreamsByChannel(ctx context.Context, channelID uuid.UUID) ([]sqlcgen.ListLiveStreamsByChannelRow, error)
	UpdateLiveStreamKey(ctx context.Context, arg sqlcgen.UpdateLiveStreamKeyParams) error
	UpdateLiveStream(ctx context.Context, arg sqlcgen.UpdateLiveStreamParams) (sqlcgen.UpdateLiveStreamRow, error)
	DeleteLiveStream(ctx context.Context, id uuid.UUID) (int64, error)
	GetLiveStreamByKeyHash(ctx context.Context, streamKeyHash string) (sqlcgen.GetLiveStreamByKeyHashRow, error)
	SetLiveStreamState(ctx context.Context, arg sqlcgen.SetLiveStreamStateParams) error
	ListLivePublicStreams(ctx context.Context, arg sqlcgen.ListLivePublicStreamsParams) ([]sqlcgen.ListLivePublicStreamsRow, error)
}

// VideoPipeline is the on-demand ingest seam the replay conversion drives: create
// a draft on the stream's channel, store the recorded session as its original,
// and finalise through the shared publish pipeline (scan/probe/thumbnail/
// transcode). *video.Service satisfies it; tests inject a fake.
type VideoPipeline interface {
	CreateDraft(ctx context.Context, channelID uuid.UUID, in video.CreateInput) (sqlcgen.Video, error)
	AttachOriginal(ctx context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error)
	Process(ctx context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error)
}

// Auditor records the best-effort replay outcome to the durable audit trail
// (ids/result only — never the stream key). *audit.Service satisfies it.
type Auditor interface {
	Record(ctx context.Context, ev audit.Event) error
}

// Live-stream states.
const (
	StateOffline = "offline"
	StateLive    = "live"
	StateEnded   = "ended"
)

// Service holds the live-stream application logic.
type Service struct {
	repo       Repository
	pipeline   VideoPipeline
	recordings RecordingStore
	auditor    Auditor
	logger     *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithReplayPipeline wires the on-demand video pipeline used to republish a
// recorded session as a VOD. Without it (and a recording store) replay is a no-op.
func WithReplayPipeline(p VideoPipeline) Option {
	return func(s *Service) { s.pipeline = p }
}

// WithRecordingStore wires access to finished session recordings written by the
// media server. Without it (and a pipeline) replay is a no-op.
func WithRecordingStore(r RecordingStore) Option {
	return func(s *Service) { s.recordings = r }
}

// WithAuditor wires the audit trail for replay outcomes.
func WithAuditor(a Auditor) Option {
	return func(s *Service) { s.auditor = a }
}

// WithLogger overrides the logger used for replay diagnostics.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// NewService builds the live service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, logger: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Stream is a live stream's metadata. OwnerID/ChannelHandle/ChannelDisplayName
// are populated on Get (from the channel join); zero on Create/List.
type Stream struct {
	ID            uuid.UUID
	ChannelID     uuid.UUID
	Title         string
	Description   string
	Privacy       string
	State         string
	Permanent     bool
	ReplayEnabled bool
	// StartedAt is when the current live session began (stamped on the transition
	// into "live", cleared when it ends). Nil when the stream is not currently
	// live / has never gone live.
	StartedAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	OwnerID            uuid.UUID
	ChannelHandle      string
	ChannelDisplayName string
}

// LiveCard is one entry of the public "Live now" listing: the minimal, truthful
// projection of a currently-live public stream for a discovery rail. It carries
// only public, currently-available fields — no privacy/state (all entries are
// public+live), no stream key, no viewer count (no server-side counter exists
// yet — see the W4 live-completion dependency). StartedAt is the current
// session's start; nil only for a stream that went live before started_at
// tracking existed.
type LiveCard struct {
	ID                 uuid.UUID
	Title              string
	Description        string
	ChannelHandle      string
	ChannelDisplayName string
	StartedAt          *time.Time
}

// timePtr converts a nullable timestamptz to *time.Time (nil when NULL).
func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// CreateInput is the metadata for a new live stream.
type CreateInput struct {
	Title         string
	Description   string
	Privacy       string
	Permanent     bool
	ReplayEnabled bool
}

// UpdateInput is the editable metadata of an existing live stream.
type UpdateInput struct {
	Title         string
	Description   string
	Privacy       string
	Permanent     bool
	ReplayEnabled bool
}

// Create makes a live stream for a channel and returns it plus the raw stream key
// (shown to the caller exactly once — only its hash is stored). The caller has
// already authorised ownership of channelID.
func (s *Service) Create(ctx context.Context, channelID uuid.UUID, in CreateInput) (Stream, string, error) {
	rawKey, hash, err := generateStreamKey()
	if err != nil {
		return Stream{}, "", err
	}
	privacy := in.Privacy
	if privacy == "" {
		privacy = "public"
	}
	row, err := s.repo.CreateLiveStream(ctx, sqlcgen.CreateLiveStreamParams{
		ChannelID:     channelID,
		Title:         in.Title,
		Description:   in.Description,
		Privacy:       privacy,
		Permanent:     in.Permanent,
		ReplayEnabled: in.ReplayEnabled,
		StreamKeyHash: hash,
	})
	if err != nil {
		return Stream{}, "", err
	}
	return Stream{
		ID: row.ID, ChannelID: row.ChannelID, Title: row.Title, Description: row.Description,
		Privacy: row.Privacy, State: row.State, Permanent: row.Permanent, ReplayEnabled: row.ReplayEnabled,
		StartedAt: timePtr(row.StartedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, rawKey, nil
}

// Update edits a live stream's mutable metadata (title/description/privacy/
// permanent/replay_enabled). The caller has already authorised ownership. It
// never touches the stream key or the live state. Privacy defaults to "public"
// when empty.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Stream, error) {
	privacy := in.Privacy
	if privacy == "" {
		privacy = "public"
	}
	row, err := s.repo.UpdateLiveStream(ctx, sqlcgen.UpdateLiveStreamParams{
		ID:            id,
		Title:         in.Title,
		Description:   in.Description,
		Privacy:       privacy,
		Permanent:     in.Permanent,
		ReplayEnabled: in.ReplayEnabled,
	})
	if err != nil {
		return Stream{}, ErrNotFound
	}
	return Stream{
		ID: row.ID, ChannelID: row.ChannelID, Title: row.Title, Description: row.Description,
		Privacy: row.Privacy, State: row.State, Permanent: row.Permanent, ReplayEnabled: row.ReplayEnabled,
		StartedAt: timePtr(row.StartedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// Get returns a live stream with its owning channel's id and identity. Miss →
// ErrNotFound. Never returns the stream key.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Stream, error) {
	r, err := s.repo.GetLiveStreamByID(ctx, id)
	if err != nil {
		return Stream{}, ErrNotFound
	}
	return Stream{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
		StartedAt: timePtr(r.StartedAt), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		OwnerID: r.OwnerID, ChannelHandle: r.ChannelHandle, ChannelDisplayName: r.ChannelDisplayName,
	}, nil
}

// ListByChannel returns a channel's live streams, newest first.
func (s *Service) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]Stream, error) {
	rows, err := s.repo.ListLiveStreamsByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	out := make([]Stream, 0, len(rows))
	for _, r := range rows {
		out = append(out, Stream{
			ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
			Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
			StartedAt: timePtr(r.StartedAt), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// ListLivePublic returns the public "Live now" listing: currently-live PUBLIC
// streams across all channels, most-recently-started first, paginated. Unlisted
// and private streams (and any not currently live) never appear. limit/offset are
// assumed already clamped by the caller.
func (s *Service) ListLivePublic(ctx context.Context, limit, offset int) ([]LiveCard, error) {
	rows, err := s.repo.ListLivePublicStreams(ctx, sqlcgen.ListLivePublicStreamsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, err
	}
	out := make([]LiveCard, 0, len(rows))
	for _, r := range rows {
		out = append(out, LiveCard{
			ID: r.ID, Title: r.Title, Description: r.Description,
			ChannelHandle: r.ChannelHandle, ChannelDisplayName: r.ChannelDisplayName,
			StartedAt: timePtr(r.StartedAt),
		})
	}
	return out, nil
}

// RegenerateKey rotates a stream's key and returns the new raw key (once). The
// caller has already authorised ownership.
func (s *Service) RegenerateKey(ctx context.Context, id uuid.UUID) (string, error) {
	rawKey, hash, err := generateStreamKey()
	if err != nil {
		return "", err
	}
	if err := s.repo.UpdateLiveStreamKey(ctx, sqlcgen.UpdateLiveStreamKeyParams{
		ID:            id,
		StreamKeyHash: hash,
	}); err != nil {
		return "", err
	}
	return rawKey, nil
}

// Delete removes a live stream (idempotent: deleting a missing one is a no-op).
// The caller has already authorised ownership.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.repo.DeleteLiveStream(ctx, id)
	return err
}

// StartIngest authenticates a publisher by the raw stream key it presents (the
// RTMP ingest boundary): it looks the stream up by key hash and flips it to
// "live". An unknown key → ErrNotFound (deny the publish). The HTTP layer gates
// this behind the ingest shared secret.
func (s *Service) StartIngest(ctx context.Context, rawKey string) (uuid.UUID, error) {
	id, _, err := s.StartByIdentity(ctx, rawKey)
	return id, err
}

// StartByIdentity flips a stream live from EITHER its raw key (the first
// on-publish, before the session is renamed) OR its stream ID (an idempotent
// re-invocation after the on-publish redirect renamed the RTMP session to the
// id). It returns the stream id and whether the identity was the raw key
// (needsRename=true) — the HTTP layer issues the rename redirect only then, so a
// post-rename re-invocation is simply allowed without redirecting again (avoiding
// a redirect loop). An unmatched identity → ErrNotFound (deny the publish).
func (s *Service) StartByIdentity(ctx context.Context, ident string) (id uuid.UUID, needsRename bool, err error) {
	// A UUID identity is a post-rename re-invocation: already bound, just ensure
	// live and allow (no further rename).
	if parsed, perr := uuid.Parse(ident); perr == nil {
		if _, gerr := s.repo.GetLiveStreamByID(ctx, parsed); gerr == nil {
			if serr := s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: parsed, State: StateLive}); serr != nil {
				return uuid.Nil, false, serr
			}
			return parsed, false, nil
		}
	}
	row, err := s.repo.GetLiveStreamByKeyHash(ctx, hashStreamKey(ident))
	if err != nil {
		return uuid.Nil, false, ErrNotFound
	}
	if err := s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: row.ID, State: StateLive}); err != nil {
		return uuid.Nil, false, err
	}
	return row.ID, true, nil
}

// StopIngest ends a publish session for the stream identified by the raw key: a
// permanent stream returns to "offline" (reusable), a one-shot stream becomes
// "ended". An unknown key → ErrNotFound.
func (s *Service) StopIngest(ctx context.Context, rawKey string) (uuid.UUID, error) {
	st, err := s.StopByIdentity(ctx, rawKey)
	if err != nil {
		return uuid.Nil, err
	}
	return st.ID, nil
}

// StopByIdentity ends a publish session identified by EITHER the stream's raw key
// (the harness/OBS path, before the media server renames the RTMP session) OR the
// stream ID (the media-server path, after the on-publish redirect renames the
// session to the id so the raw key never touches the filesystem). It flips the
// state (permanent → offline, one-shot → ended) and returns the full stream so
// the caller can decide whether to trigger a replay. An unmatched identity →
// ErrNotFound.
func (s *Service) StopByIdentity(ctx context.Context, ident string) (Stream, error) {
	var st Stream
	// A UUID identity is the post-rename media-server path.
	if id, perr := uuid.Parse(ident); perr == nil {
		if got, gerr := s.Get(ctx, id); gerr == nil {
			st = got
		}
	}
	// Otherwise resolve the raw key by its hash (the pre-rename / harness path).
	if st.ID == uuid.Nil {
		row, err := s.repo.GetLiveStreamByKeyHash(ctx, hashStreamKey(ident))
		if err != nil {
			return Stream{}, ErrNotFound
		}
		got, gerr := s.Get(ctx, row.ID)
		if gerr != nil {
			return Stream{}, ErrNotFound
		}
		st = got
	}
	next := StateEnded
	if st.Permanent {
		next = StateOffline
	}
	if err := s.repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: st.ID, State: next}); err != nil {
		return Stream{}, err
	}
	st.State = next
	return st, nil
}

// replayTitleSuffix distinguishes a republished session from the live stream it
// came from (the live view is transient; the VOD persists on the channel).
const replayTitleSuffix = " (replay)"

// RunReplay best-effort republishes a stream's finished recording as a normal
// on-demand video (P12): it opens the recorded session, creates a draft on the
// stream's channel titled from the stream (privacy inherited), stores the
// recording as the draft's original, and finalises through the shared publish
// pipeline (scan/probe/thumbnail/transcode) — so the replay appears as a normal
// published video. It is a no-op when replay is disabled for the stream, when no
// pipeline/recording store is wired, or when no recording exists yet. Every
// failure is logged + audited and swallowed: a replay must NEVER break the
// ingest-stop hook. Intended to be called detached (its own context) after the
// stop hook has already responded.
func (s *Service) RunReplay(ctx context.Context, streamID uuid.UUID) {
	if s.pipeline == nil || s.recordings == nil {
		return // replay not configured on this instance
	}
	st, err := s.Get(ctx, streamID)
	if err != nil {
		return // stream vanished
	}
	if !st.ReplayEnabled {
		return // opt-in only
	}

	rc, filename, err := s.recordings.OpenRecording(streamID)
	if errors.Is(err, ErrNoRecording) {
		// A live session with replay on but no recording on disk (e.g. the media
		// server did not record, or the publish never produced a file). Not an
		// error worth auditing — just note it.
		s.logger.InfoContext(ctx, "live replay: no recording for finished session", "stream_id", streamID.String())
		return
	}
	if err != nil {
		s.replayFailed(ctx, streamID, "open_recording", err)
		return
	}
	defer func() { _ = rc.Close() }()

	title := strings.TrimSpace(st.Title)
	if title == "" {
		title = "Live replay"
	} else {
		title += replayTitleSuffix
	}
	draft, err := s.pipeline.CreateDraft(ctx, st.ChannelID, video.CreateInput{
		Title:       title,
		Description: st.Description,
		Privacy:     st.Privacy,
	})
	if err != nil {
		s.replayFailed(ctx, streamID, "create_draft", err)
		return
	}
	_, file, err := s.pipeline.AttachOriginal(ctx, st.OwnerID, draft.ID, video.UploadInput{
		Filename: filename,
		Reader:   rc,
	})
	if err != nil {
		s.replayFailed(ctx, streamID, "attach_original", err)
		return
	}
	if _, err := s.pipeline.Process(ctx, draft.ID, file.StorageKey); err != nil {
		s.replayFailed(ctx, streamID, "process", err)
		return
	}
	s.logger.InfoContext(ctx, "live replay published", "stream_id", streamID.String(), "video_id", draft.ID.String())
	s.audit(ctx, observability.ResultSuccess, "stream="+streamID.String()+" video="+draft.ID.String())
}

// replayFailed logs and audits a replay-stage failure without leaking the error
// detail into the audit reason (kept to a stable stage label) or the stream key.
func (s *Service) replayFailed(ctx context.Context, streamID uuid.UUID, stage string, cause error) {
	s.logger.WarnContext(ctx, "live replay failed", "stream_id", streamID.String(), "stage", stage, "error", cause)
	s.audit(ctx, observability.ResultFailure, "stream="+streamID.String()+" stage="+stage)
}

// audit records a replay outcome when an auditor is wired (best-effort).
func (s *Service) audit(ctx context.Context, result, reason string) {
	if s.auditor == nil {
		return
	}
	_ = s.auditor.Record(ctx, audit.Event{
		Action: observability.ActionLiveReplay,
		Result: result,
		Reason: reason,
	})
}

// generateStreamKey returns a new high-entropy opaque stream key and its storage
// hash. The raw key goes to the streamer (OBS) exactly once; only the hash is
// persisted. A fast hash (SHA-256) is correct for a high-entropy random token.
func generateStreamKey() (raw, hash string, err error) {
	b := make([]byte, streamKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashStreamKey(raw), nil
}

// hashStreamKey returns the hex SHA-256 of a raw stream key — the stored form and
// the ingest-lookup key.
func hashStreamKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
