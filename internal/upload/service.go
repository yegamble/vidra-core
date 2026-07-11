// Package upload implements chunked/resumable video uploads (fix_plan P6.1). A
// session (upload_sessions, migration 0059) is opened for a video the caller
// owns after the size/extension/quota are validated up front; the client PUTs
// fixed-size chunks (ChunkSize bytes each, the last shorter), whose bytes land
// in the blob backend at uploads/<session>/<n> — so the S3 backend works
// unchanged — and whose arrival is recorded in upload_chunks (the resume /
// progress contract, no Redis needed). Completion assembles the chunks in order
// into a single reader the caller feeds through the existing
// video.AttachOriginal → Process pipeline, identical to a direct upload. A
// sweeper deletes expired (24h) and cancelled sessions' chunk blobs — the
// failed-upload cleanup — leaving orphan draft videos alone.
//
// It is HTTP-agnostic and testable with a fake repository + storage backend.
package upload

import (
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ChunkSize is the fixed chunk size clients upload in (8 MiB), reported by the
// create-session response. Every chunk but the last is exactly this size.
const ChunkSize int32 = 8 * 1024 * 1024

// SessionTTL is how long a session stays open before the sweeper collects it
// (and its chunk blobs). An upload must complete within this window.
const SessionTTL = 24 * time.Hour

// Session states persisted on upload_sessions.
const (
	StateActive    = "active"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no session matches the id, or it is not owned by the
	// caller (reported as 404 so a session's existence is not leaked).
	ErrNotFound = errors.New("upload: session not found")
	// ErrNotActive means the session has already completed or been cancelled and
	// no longer accepts chunks or completion (409).
	ErrNotActive = errors.New("upload: session not active")
	// ErrChunkRange means the chunk index is outside [0, total_chunks) (422).
	ErrChunkRange = errors.New("upload: chunk index out of range")
	// ErrChunkSize means the chunk's byte length does not match the size the
	// protocol requires for that index (422).
	ErrChunkSize = errors.New("upload: chunk size mismatch")
	// ErrChunkTooLarge means the chunk body exceeded the size required for its
	// index (413).
	ErrChunkTooLarge = errors.New("upload: chunk too large")
	// ErrIncomplete means completion was requested before every chunk landed at
	// its required size (422).
	ErrIncomplete = errors.New("upload: missing or mismatched chunks")
	// ErrStorageUnavailable means no blob backend is configured.
	ErrStorageUnavailable = errors.New("upload: storage backend not configured")
	// ErrTooManyActiveSessions means the caller already holds the maximum number
	// of concurrent active upload sessions (UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER,
	// UPLOAD-10). The HTTP layer maps it to 429 with a stable code a batch client
	// queues on; cancelling or completing an in-flight session frees a slot.
	ErrTooManyActiveSessions = errors.New("upload: too many active sessions")
)

// Repository is the data access the upload service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateUploadSession(ctx context.Context, arg sqlcgen.CreateUploadSessionParams) (sqlcgen.UploadSession, error)
	CountActiveUploadSessionsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	GetUploadSession(ctx context.Context, id uuid.UUID) (sqlcgen.UploadSession, error)
	UpsertUploadChunk(ctx context.Context, arg sqlcgen.UpsertUploadChunkParams) error
	ListUploadChunks(ctx context.Context, uploadID uuid.UUID) ([]sqlcgen.ListUploadChunksRow, error)
	ListActiveUploadSessionsForUser(ctx context.Context, arg sqlcgen.ListActiveUploadSessionsForUserParams) ([]sqlcgen.ListActiveUploadSessionsForUserRow, error)
	SetUploadSessionState(ctx context.Context, arg sqlcgen.SetUploadSessionStateParams) error
	ListSweepableUploadSessions(ctx context.Context, limit int32) ([]uuid.UUID, error)
	DeleteUploadSession(ctx context.Context, id uuid.UUID) error
}

// Service holds the resumable-upload logic.
type Service struct {
	repo                Repository
	blobs               storage.Backend
	chunkSize           int32
	maxActiveSessions   int
	maxActiveSessionsFn func() int // when set, supersedes maxActiveSessions (live overlay)
	now                 func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock injects a deterministic clock for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithChunkSize overrides the chunk size a new session advertises and validates
// against (default ChunkSize, 8 MiB). Tests use a small value to exercise
// multi-chunk round trips with small fixtures. Must be > 0.
func WithChunkSize(n int32) Option {
	return func(s *Service) {
		if n > 0 {
			s.chunkSize = n
		}
	}
}

// WithMaxActiveSessions caps how many ACTIVE (unfinished, unexpired) upload
// sessions a single user may hold at once — the batch-upload guard (UPLOAD-10,
// UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER). A create past the cap returns
// ErrTooManyActiveSessions. n <= 0 disables the limit (the default), preserving
// the pre-guard behaviour.
func WithMaxActiveSessions(n int) Option {
	return func(s *Service) { s.maxActiveSessions = n }
}

// WithMaxActiveSessionsFunc makes the active-session cap dynamic: f is consulted
// per CreateSession so an admin can retune the overlay at runtime. When set it
// supersedes WithMaxActiveSessions. f returning <= 0 disables the limit.
func WithMaxActiveSessionsFunc(f func() int) Option {
	return func(s *Service) { s.maxActiveSessionsFn = f }
}

// NewService builds the upload service. blobs stores the chunk bytes; it must
// be non-nil in production (the routes only mount when it is).
func NewService(repo Repository, blobs storage.Backend, opts ...Option) *Service {
	s := &Service{repo: repo, blobs: blobs, chunkSize: ChunkSize, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateSession opens a resumable upload for a video. The caller has already
// validated ownership, the filename extension, the size against
// UPLOAD_MAX_SIZE, and the caller's storage quota. size must be > 0.
// fileFingerprint is an OPAQUE client identity for the file (recommended recipe:
// SHA-256 over size + first/last 1 MiB) used for server-side resume (UPLOAD-03);
// it is stored verbatim and never parsed. Pass "" when the client supplies none.
func (s *Service) CreateSession(ctx context.Context, videoID, userID uuid.UUID, filename string, size int64, fileFingerprint string) (sqlcgen.UploadSession, error) {
	// Batch-upload guard (UPLOAD-10): cap concurrent active sessions per user so a
	// client's batch orchestration queues instead of opening unbounded sessions.
	// This is a fairness/backpressure signal, not a security boundary — a small
	// TOCTOU window between the count and the insert is acceptable (the sweeper
	// and per-file quota are the hard limits).
	maxActive := s.maxActiveSessions
	if s.maxActiveSessionsFn != nil {
		maxActive = s.maxActiveSessionsFn()
	}
	if maxActive > 0 {
		active, err := s.repo.CountActiveUploadSessionsForUser(ctx, userID)
		if err != nil {
			return sqlcgen.UploadSession{}, err
		}
		if active >= int64(maxActive) {
			return sqlcgen.UploadSession{}, ErrTooManyActiveSessions
		}
	}
	return s.repo.CreateUploadSession(ctx, sqlcgen.CreateUploadSessionParams{
		VideoID:         videoID,
		UserID:          userID,
		Filename:        filename,
		TotalSize:       size,
		ChunkSize:       s.chunkSize,
		ExpiresAt:       s.now().UTC().Add(SessionTTL),
		FileFingerprint: fileFingerprint,
	})
}

// ActiveUpload is one of the caller's resumable sessions still open for resume
// (UPLOAD-03): the fields the UI needs to reconstruct/continue an upload after a
// refresh or on another device. ReceivedChunks is how many chunk indices have
// landed so far, out of TotalChunks.
type ActiveUpload struct {
	ID              uuid.UUID
	VideoID         uuid.UUID
	Filename        string
	TotalSize       int64
	ChunkSize       int32
	FileFingerprint string
	ExpiresAt       time.Time
	ReceivedChunks  int
	TotalChunks     int
}

// ActiveSessionsForUser returns the caller's ACTIVE (unfinished, unexpired)
// upload sessions with each session's received-chunk count — the server-side
// resume list (UPLOAD-03). A client that lost its localStorage (a refresh, a new
// device) reconstructs its in-progress uploads from here; localStorage is a
// cache, not the source of truth. A non-empty fingerprint narrows the list to
// sessions with that exact file_fingerprint (the "am I already uploading this
// file?" query); an empty fingerprint returns all of the caller's active
// sessions.
func (s *Service) ActiveSessionsForUser(ctx context.Context, userID uuid.UUID, fingerprint string) ([]ActiveUpload, error) {
	var fp *string
	if fingerprint != "" {
		fp = &fingerprint
	}
	rows, err := s.repo.ListActiveUploadSessionsForUser(ctx, sqlcgen.ListActiveUploadSessionsForUserParams{
		UserID:      userID,
		Fingerprint: fp,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ActiveUpload, 0, len(rows))
	for _, r := range rows {
		out = append(out, ActiveUpload{
			ID:              r.ID,
			VideoID:         r.VideoID,
			Filename:        r.Filename,
			TotalSize:       r.TotalSize,
			ChunkSize:       r.ChunkSize,
			FileFingerprint: r.FileFingerprint,
			ExpiresAt:       r.ExpiresAt,
			ReceivedChunks:  int(r.ReceivedChunks),
			TotalChunks:     int(totalChunks(r.TotalSize, r.ChunkSize)),
		})
	}
	return out, nil
}

// session loads a session and enforces ownership, mapping a miss or a
// non-owner to ErrNotFound (so existence is not leaked).
func (s *Service) session(ctx context.Context, uploadID, userID uuid.UUID) (sqlcgen.UploadSession, error) {
	sess, err := s.repo.GetUploadSession(ctx, uploadID)
	if err != nil || sess.UserID != userID {
		return sqlcgen.UploadSession{}, ErrNotFound
	}
	return sess, nil
}

// PutChunk stores chunk index n for a session (idempotent re-PUT). The body is
// bounded: a chunk larger than its required size is ErrChunkTooLarge (413), a
// shorter one ErrChunkSize (422). Returns the updated received-chunk status.
func (s *Service) PutChunk(ctx context.Context, uploadID, userID uuid.UUID, n int, r io.Reader) (Status, error) {
	if s.blobs == nil {
		return Status{}, ErrStorageUnavailable
	}
	sess, err := s.session(ctx, uploadID, userID)
	if err != nil {
		return Status{}, err
	}
	if sess.State != StateActive {
		return Status{}, ErrNotActive
	}
	expected, ok := chunkExpectedSize(sess.TotalSize, sess.ChunkSize, n)
	if !ok {
		return Status{}, ErrChunkRange
	}
	// Bound the body at the required size so a hostile/oversized chunk cannot
	// stream unbounded into storage.
	capped := &cappedReader{r: r, remaining: expected, err: ErrChunkTooLarge}
	key := chunkKey(uploadID, n)
	written, err := s.blobs.Put(ctx, key, capped)
	if err != nil {
		if errors.Is(err, ErrChunkTooLarge) {
			return Status{}, ErrChunkTooLarge
		}
		return Status{}, err
	}
	if written != expected {
		// Wrong length for this index — drop the partial blob so a retry re-PUTs
		// cleanly, and reject.
		_ = s.blobs.Delete(ctx, key)
		return Status{}, ErrChunkSize
	}
	if err := s.repo.UpsertUploadChunk(ctx, sqlcgen.UpsertUploadChunkParams{
		UploadID:  uploadID,
		N:         int32(n),
		SizeBytes: written,
	}); err != nil {
		return Status{}, err
	}
	return s.buildStatus(ctx, sess)
}

// Status is a session's received-chunk view: which chunk indices have landed,
// how many total are expected, and the byte total so far — the resume /
// progress contract (GET /uploads/:id).
type Status struct {
	Session        sqlcgen.UploadSession
	TotalChunks    int
	ReceivedChunks []int
	BytesReceived  int64
}

// StatusFor returns the session's received-chunk status. Ownership-guarded.
func (s *Service) StatusFor(ctx context.Context, uploadID, userID uuid.UUID) (Status, error) {
	sess, err := s.session(ctx, uploadID, userID)
	if err != nil {
		return Status{}, err
	}
	return s.buildStatus(ctx, sess)
}

// buildStatus reads the chunk ledger and packages it as a Status.
func (s *Service) buildStatus(ctx context.Context, sess sqlcgen.UploadSession) (Status, error) {
	rows, err := s.repo.ListUploadChunks(ctx, sess.ID)
	if err != nil {
		return Status{}, err
	}
	received := make([]int, 0, len(rows))
	var bytesReceived int64
	for _, r := range rows {
		received = append(received, int(r.N))
		bytesReceived += r.SizeBytes
	}
	return Status{
		Session:        sess,
		TotalChunks:    int(totalChunks(sess.TotalSize, sess.ChunkSize)),
		ReceivedChunks: received,
		BytesReceived:  bytesReceived,
	}, nil
}

// PrepareComplete validates that every chunk has landed at its required size
// and returns the session plus a reader that streams the chunks in order (the
// caller feeds it to video.AttachOriginal). The caller MUST Close the reader,
// then call MarkCompleted once the pipeline has consumed it. ErrIncomplete when
// a chunk is missing or the wrong size; ErrNotActive when already finished.
func (s *Service) PrepareComplete(ctx context.Context, uploadID, userID uuid.UUID) (sqlcgen.UploadSession, io.ReadCloser, error) {
	if s.blobs == nil {
		return sqlcgen.UploadSession{}, nil, ErrStorageUnavailable
	}
	sess, err := s.session(ctx, uploadID, userID)
	if err != nil {
		return sqlcgen.UploadSession{}, nil, err
	}
	if sess.State != StateActive {
		return sqlcgen.UploadSession{}, nil, ErrNotActive
	}
	rows, err := s.repo.ListUploadChunks(ctx, uploadID)
	if err != nil {
		return sqlcgen.UploadSession{}, nil, err
	}
	sizes := make(map[int]int64, len(rows))
	for _, r := range rows {
		sizes[int(r.N)] = r.SizeBytes
	}
	total := int(totalChunks(sess.TotalSize, sess.ChunkSize))
	for n := 0; n < total; n++ {
		expected, _ := chunkExpectedSize(sess.TotalSize, sess.ChunkSize, n)
		if got, ok := sizes[n]; !ok || got != expected {
			return sqlcgen.UploadSession{}, nil, ErrIncomplete
		}
	}
	return sess, &chunkAssembler{ctx: ctx, blobs: s.blobs, uploadID: uploadID, total: total}, nil
}

// MarkCompleted flips a session to completed and best-effort removes its now
// consumed chunk blobs. Call after the pipeline has read the assembled reader.
func (s *Service) MarkCompleted(ctx context.Context, uploadID uuid.UUID) error {
	if err := s.repo.SetUploadSessionState(ctx, sqlcgen.SetUploadSessionStateParams{
		ID:    uploadID,
		State: StateCompleted,
	}); err != nil {
		return err
	}
	s.deleteChunks(ctx, uploadID)
	return nil
}

// Cancel marks an active session cancelled and best-effort removes its chunk
// blobs (the sweeper is the backstop). Ownership-guarded; idempotent — a
// cancelled or completed session is a no-op success so a client can always
// clean up.
func (s *Service) Cancel(ctx context.Context, uploadID, userID uuid.UUID) error {
	sess, err := s.session(ctx, uploadID, userID)
	if err != nil {
		return err
	}
	if sess.State != StateActive {
		return nil // already finished/cancelled — nothing to do
	}
	if err := s.repo.SetUploadSessionState(ctx, sqlcgen.SetUploadSessionStateParams{
		ID:    uploadID,
		State: StateCancelled,
	}); err != nil {
		return err
	}
	s.deleteChunks(ctx, uploadID)
	return nil
}

// Sweep deletes up to limit expired/cancelled sessions: the chunk blobs first
// (best-effort), then the row (which cascades the chunk ledger). This is the
// failed-upload cleanup — abandoned sessions past their 24h expiry and
// explicitly cancelled ones. Returns the number of sessions removed. Intended
// to run on a ticker by a single worker.
func (s *Service) Sweep(ctx context.Context, limit int) (int, error) {
	ids, err := s.repo.ListSweepableUploadSessions(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		s.deleteChunks(ctx, id)
		if err := s.repo.DeleteUploadSession(ctx, id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// deleteChunks best-effort removes all chunk blobs under a session's prefix.
func (s *Service) deleteChunks(ctx context.Context, uploadID uuid.UUID) {
	pd, ok := s.blobs.(storage.PrefixDeleter)
	if !ok {
		return
	}
	_ = pd.DeletePrefix(ctx, chunkPrefix(uploadID))
}

// chunkPrefix is the blob "directory" holding a session's chunks.
func chunkPrefix(uploadID uuid.UUID) string {
	return "uploads/" + uploadID.String()
}

// chunkKey is the deterministic blob key for chunk index n of a session.
func chunkKey(uploadID uuid.UUID, n int) string {
	return chunkPrefix(uploadID) + "/" + strconv.Itoa(n)
}

// totalChunks is ceil(totalSize / chunkSize) — how many chunks the client sends.
func totalChunks(totalSize int64, chunkSize int32) int64 {
	cs := int64(chunkSize)
	return (totalSize + cs - 1) / cs
}

// chunkExpectedSize returns the exact byte length required for chunk index n of
// an upload of totalSize bytes in chunkSize-byte chunks, and false when n is out
// of range. Every chunk but the last is chunkSize; the last carries the
// remainder (== chunkSize when totalSize divides evenly).
func chunkExpectedSize(totalSize int64, chunkSize int32, n int) (int64, bool) {
	if n < 0 {
		return 0, false
	}
	cs := int64(chunkSize)
	total := totalChunks(totalSize, chunkSize)
	if int64(n) >= total {
		return 0, false
	}
	if int64(n) == total-1 {
		return totalSize - int64(n)*cs, true
	}
	return cs, true
}

// cappedReader bounds how many bytes are read from a chunk body, failing with
// err once the cap is crossed. It reads one byte past the cap to distinguish
// "exactly at the cap" (ok) from "over".
type cappedReader struct {
	r         io.Reader
	remaining int64
	err       error
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		return 0, c.err
	}
	if int64(len(p)) > c.remaining+1 {
		p = p[:c.remaining+1]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		return n, c.err
	}
	return n, err
}

// chunkAssembler streams a session's chunk blobs in index order as one reader,
// lazily opening each from storage so the whole file is never buffered.
type chunkAssembler struct {
	ctx      context.Context
	blobs    storage.Backend
	uploadID uuid.UUID
	total    int
	idx      int
	cur      io.ReadCloser
}

func (a *chunkAssembler) Read(p []byte) (int, error) {
	for {
		if a.cur == nil {
			if a.idx >= a.total {
				return 0, io.EOF
			}
			rc, err := a.blobs.Open(a.ctx, chunkKey(a.uploadID, a.idx))
			if err != nil {
				return 0, err
			}
			a.cur = rc
		}
		n, err := a.cur.Read(p)
		if n > 0 {
			return n, nil // a simultaneous EOF is handled on the next call
		}
		if errors.Is(err, io.EOF) {
			_ = a.cur.Close()
			a.cur = nil
			a.idx++
			continue
		}
		if err != nil {
			return n, err
		}
	}
}

func (a *chunkAssembler) Close() error {
	if a.cur != nil {
		err := a.cur.Close()
		a.cur = nil
		return err
	}
	return nil
}
