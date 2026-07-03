package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory upload.Repository.
type fakeRepo struct {
	sessions map[uuid.UUID]sqlcgen.UploadSession
	chunks   map[uuid.UUID]map[int32]int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{sessions: map[uuid.UUID]sqlcgen.UploadSession{}, chunks: map[uuid.UUID]map[int32]int64{}}
}

func (r *fakeRepo) CreateUploadSession(_ context.Context, arg sqlcgen.CreateUploadSessionParams) (sqlcgen.UploadSession, error) {
	s := sqlcgen.UploadSession{
		ID: uuid.New(), VideoID: arg.VideoID, UserID: arg.UserID, Filename: arg.Filename,
		TotalSize: arg.TotalSize, ChunkSize: arg.ChunkSize, State: "active", ExpiresAt: arg.ExpiresAt,
	}
	r.sessions[s.ID] = s
	return s, nil
}
func (r *fakeRepo) GetUploadSession(_ context.Context, id uuid.UUID) (sqlcgen.UploadSession, error) {
	s, ok := r.sessions[id]
	if !ok {
		return sqlcgen.UploadSession{}, errors.New("no rows")
	}
	return s, nil
}
func (r *fakeRepo) UpsertUploadChunk(_ context.Context, arg sqlcgen.UpsertUploadChunkParams) error {
	if r.chunks[arg.UploadID] == nil {
		r.chunks[arg.UploadID] = map[int32]int64{}
	}
	r.chunks[arg.UploadID][arg.N] = arg.SizeBytes
	return nil
}
func (r *fakeRepo) ListUploadChunks(_ context.Context, id uuid.UUID) ([]sqlcgen.ListUploadChunksRow, error) {
	rows := make([]sqlcgen.ListUploadChunksRow, 0, len(r.chunks[id]))
	for n, s := range r.chunks[id] {
		rows = append(rows, sqlcgen.ListUploadChunksRow{N: n, SizeBytes: s})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].N < rows[j].N })
	return rows, nil
}
func (r *fakeRepo) SetUploadSessionState(_ context.Context, arg sqlcgen.SetUploadSessionStateParams) error {
	if s, ok := r.sessions[arg.ID]; ok {
		s.State = arg.State
		r.sessions[arg.ID] = s
	}
	return nil
}
func (r *fakeRepo) ListSweepableUploadSessions(_ context.Context, limit int32) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for id, s := range r.sessions {
		if s.State == "cancelled" || !s.ExpiresAt.After(time.Now()) {
			ids = append(ids, id)
		}
	}
	if int32(len(ids)) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}
func (r *fakeRepo) DeleteUploadSession(_ context.Context, id uuid.UUID) error {
	delete(r.sessions, id)
	delete(r.chunks, id)
	return nil
}

func newTestService(t *testing.T, chunkSize int32) (*Service, *fakeRepo, storage.Backend) {
	t.Helper()
	repo := newFakeRepo()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	return NewService(repo, blobs, WithChunkSize(chunkSize)), repo, blobs
}

// putAll uploads the given chunk payloads in the given index order.
func putAll(t *testing.T, s *Service, id, user uuid.UUID, chunks map[int][]byte, order []int) {
	t.Helper()
	for _, n := range order {
		if _, err := s.PutChunk(context.Background(), id, user, n, bytes.NewReader(chunks[n])); err != nil {
			t.Fatalf("put chunk %d: %v", n, err)
		}
	}
}

// TestChunkRoundTripOutOfOrderAndResume covers out-of-order PUTs, an idempotent
// re-PUT, resume across a fresh Service (simulating a restart) sharing the repo
// and blobs, and byte-exact assembly on complete.
func TestChunkRoundTripOutOfOrderAndResume(t *testing.T) {
	ctx := context.Background()
	svc, repo, blobs := newTestService(t, 4)
	user, video := uuid.New(), uuid.New()

	c := map[int][]byte{0: []byte("AAAA"), 1: []byte("BBBB"), 2: []byte("CC")} // 4+4+2 = 10
	want := []byte("AAAABBBBCC")

	sess, err := svc.CreateSession(ctx, video, user, "clip.mp4", int64(len(want)))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Out of order, with a re-PUT of chunk 0.
	putAll(t, svc, sess.ID, user, c, []int{2, 0, 0, 1})

	st, err := svc.StatusFor(ctx, sess.ID, user)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.TotalChunks != 3 || len(st.ReceivedChunks) != 3 || st.BytesReceived != int64(len(want)) {
		t.Fatalf("status = %+v, want 3 chunks / %d bytes", st, len(want))
	}

	// "Restart": a brand-new Service over the same repo + blobs resumes the same
	// session and completes it.
	svc2 := NewService(repo, blobs, WithChunkSize(4))
	sess2, reader, err := svc2.PrepareComplete(ctx, sess.ID, user)
	if err != nil {
		t.Fatalf("prepare complete: %v", err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != string(want) {
		t.Fatalf("assembled = %q, want %q", got, want)
	}
	if sess2.Filename != "clip.mp4" {
		t.Errorf("session filename = %q", sess2.Filename)
	}
	if err := svc2.MarkCompleted(ctx, sess.ID); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	if repo.sessions[sess.ID].State != StateCompleted {
		t.Errorf("state = %q, want completed", repo.sessions[sess.ID].State)
	}
	// Chunk blobs are dropped on completion.
	if ex, _ := blobs.Exists(ctx, chunkKey(sess.ID, 0)); ex {
		t.Errorf("chunk blob still present after completion")
	}
}

// TestChunkSizeValidation covers the exact-size gate: a short chunk is
// ErrChunkSize, an oversized one ErrChunkTooLarge, an out-of-range index
// ErrChunkRange, and completion before all chunks land is ErrIncomplete.
func TestChunkSizeValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, 4)
	user, video := uuid.New(), uuid.New()
	sess, _ := svc.CreateSession(ctx, video, user, "clip.mp4", 10) // chunks 4/4/2

	if _, err := svc.PutChunk(ctx, sess.ID, user, 0, bytes.NewReader([]byte("AA"))); !errors.Is(err, ErrChunkSize) {
		t.Errorf("short chunk err = %v, want ErrChunkSize", err)
	}
	if _, err := svc.PutChunk(ctx, sess.ID, user, 0, bytes.NewReader([]byte("AAAAA"))); !errors.Is(err, ErrChunkTooLarge) {
		t.Errorf("oversized chunk err = %v, want ErrChunkTooLarge", err)
	}
	if _, err := svc.PutChunk(ctx, sess.ID, user, 9, bytes.NewReader([]byte("AAAA"))); !errors.Is(err, ErrChunkRange) {
		t.Errorf("out-of-range err = %v, want ErrChunkRange", err)
	}
	// Only chunk 0 lands → complete is ErrIncomplete.
	if _, err := svc.PutChunk(ctx, sess.ID, user, 0, bytes.NewReader([]byte("AAAA"))); err != nil {
		t.Fatalf("put chunk 0: %v", err)
	}
	if _, _, err := svc.PrepareComplete(ctx, sess.ID, user); !errors.Is(err, ErrIncomplete) {
		t.Errorf("prepare complete err = %v, want ErrIncomplete", err)
	}
}

// TestOwnershipIsolation: a non-owner cannot see, PUT to, or complete a session.
func TestOwnershipIsolation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, 4)
	owner, other, video := uuid.New(), uuid.New(), uuid.New()
	sess, _ := svc.CreateSession(ctx, video, owner, "clip.mp4", 4)

	if _, err := svc.StatusFor(ctx, sess.ID, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-owner status err = %v, want ErrNotFound", err)
	}
	if _, err := svc.PutChunk(ctx, sess.ID, other, 0, bytes.NewReader([]byte("AAAA"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-owner put err = %v, want ErrNotFound", err)
	}
	if _, _, err := svc.PrepareComplete(ctx, sess.ID, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-owner complete err = %v, want ErrNotFound", err)
	}
}

// TestCancelAndSweep: cancelling drops the chunk blobs and blocks further PUTs;
// the sweeper removes cancelled and expired sessions (and their blobs).
func TestCancelAndSweep(t *testing.T) {
	ctx := context.Background()
	svc, repo, blobs := newTestService(t, 4)
	user, video := uuid.New(), uuid.New()

	// A cancelled session.
	cancelled, _ := svc.CreateSession(ctx, video, user, "clip.mp4", 8)
	putAll(t, svc, cancelled.ID, user, map[int][]byte{0: []byte("AAAA")}, []int{0})
	if err := svc.Cancel(ctx, cancelled.ID, user); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if ex, _ := blobs.Exists(ctx, chunkKey(cancelled.ID, 0)); ex {
		t.Errorf("chunk blob present after cancel")
	}
	if _, err := svc.PutChunk(ctx, cancelled.ID, user, 1, bytes.NewReader([]byte("BBBB"))); !errors.Is(err, ErrNotActive) {
		t.Errorf("put after cancel err = %v, want ErrNotActive", err)
	}
	// Cancel is idempotent.
	if err := svc.Cancel(ctx, cancelled.ID, user); err != nil {
		t.Errorf("re-cancel: %v", err)
	}

	// An expired (but never cancelled) session: force expiry into the past.
	expired, _ := svc.CreateSession(ctx, video, user, "clip.mp4", 4)
	s := repo.sessions[expired.ID]
	s.ExpiresAt = time.Now().Add(-time.Hour)
	repo.sessions[expired.ID] = s

	n, err := svc.Sweep(ctx, 50)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 {
		t.Errorf("swept %d sessions, want 2 (cancelled + expired)", n)
	}
	if _, ok := repo.sessions[cancelled.ID]; ok {
		t.Errorf("cancelled session row still present after sweep")
	}
	if _, ok := repo.sessions[expired.ID]; ok {
		t.Errorf("expired session row still present after sweep")
	}
}

// TestExpiryComputedFromClock: the session expiry is now()+TTL from the injected
// clock.
func TestExpiryComputedFromClock(t *testing.T) {
	fixed := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	repo := newFakeRepo()
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithClock(func() time.Time { return fixed }))
	sess, err := svc.CreateSession(context.Background(), uuid.New(), uuid.New(), "clip.mp4", 4)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if want := fixed.Add(SessionTTL); !sess.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", sess.ExpiresAt, want)
	}
	if sess.ChunkSize != ChunkSize {
		t.Errorf("default chunk size = %d, want %d", sess.ChunkSize, ChunkSize)
	}
}
