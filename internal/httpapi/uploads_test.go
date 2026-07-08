package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ---------------------------------------------------------------------------
// in-memory fakes for upload.Repository and videoimport.Repository
// ---------------------------------------------------------------------------

type uploadFakeRepo struct {
	sessions map[uuid.UUID]sqlcgen.UploadSession
	chunks   map[uuid.UUID]map[int32]int64
}

func newUploadFakeRepo() *uploadFakeRepo {
	return &uploadFakeRepo{
		sessions: map[uuid.UUID]sqlcgen.UploadSession{},
		chunks:   map[uuid.UUID]map[int32]int64{},
	}
}

func (r *uploadFakeRepo) CreateUploadSession(_ context.Context, arg sqlcgen.CreateUploadSessionParams) (sqlcgen.UploadSession, error) {
	s := sqlcgen.UploadSession{
		ID:              uuid.New(),
		VideoID:         arg.VideoID,
		UserID:          arg.UserID,
		Filename:        arg.Filename,
		TotalSize:       arg.TotalSize,
		ChunkSize:       arg.ChunkSize,
		State:           "active",
		ExpiresAt:       arg.ExpiresAt,
		FileFingerprint: arg.FileFingerprint,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	r.sessions[s.ID] = s
	return s, nil
}

func (r *uploadFakeRepo) GetUploadSession(_ context.Context, id uuid.UUID) (sqlcgen.UploadSession, error) {
	s, ok := r.sessions[id]
	if !ok {
		return sqlcgen.UploadSession{}, pgx.ErrNoRows
	}
	return s, nil
}

func (r *uploadFakeRepo) UpsertUploadChunk(_ context.Context, arg sqlcgen.UpsertUploadChunkParams) error {
	if r.chunks[arg.UploadID] == nil {
		r.chunks[arg.UploadID] = map[int32]int64{}
	}
	r.chunks[arg.UploadID][arg.N] = arg.SizeBytes
	return nil
}

func (r *uploadFakeRepo) ListUploadChunks(_ context.Context, uploadID uuid.UUID) ([]sqlcgen.ListUploadChunksRow, error) {
	rows := make([]sqlcgen.ListUploadChunksRow, 0, len(r.chunks[uploadID]))
	for n, size := range r.chunks[uploadID] {
		rows = append(rows, sqlcgen.ListUploadChunksRow{N: n, SizeBytes: size})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].N < rows[j].N })
	return rows, nil
}

func (r *uploadFakeRepo) CountActiveUploadSessionsForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, s := range r.sessions {
		if s.UserID == userID && s.State == "active" && s.ExpiresAt.After(time.Now()) {
			n++
		}
	}
	return n, nil
}

func (r *uploadFakeRepo) ListActiveUploadSessionsForUser(_ context.Context, arg sqlcgen.ListActiveUploadSessionsForUserParams) ([]sqlcgen.ListActiveUploadSessionsForUserRow, error) {
	var rows []sqlcgen.ListActiveUploadSessionsForUserRow
	for id, s := range r.sessions {
		if s.UserID != arg.UserID || s.State != "active" || !s.ExpiresAt.After(time.Now()) {
			continue
		}
		if arg.Fingerprint != nil && s.FileFingerprint != *arg.Fingerprint {
			continue
		}
		rows = append(rows, sqlcgen.ListActiveUploadSessionsForUserRow{
			ID: id, VideoID: s.VideoID, Filename: s.Filename, TotalSize: s.TotalSize,
			ChunkSize: s.ChunkSize, FileFingerprint: s.FileFingerprint, ExpiresAt: s.ExpiresAt,
			ReceivedChunks: int64(len(r.chunks[id])),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return r.sessions[rows[i].ID].CreatedAt.After(r.sessions[rows[j].ID].CreatedAt)
	})
	return rows, nil
}

func (r *uploadFakeRepo) SetUploadSessionState(_ context.Context, arg sqlcgen.SetUploadSessionStateParams) error {
	if s, ok := r.sessions[arg.ID]; ok {
		s.State = arg.State
		r.sessions[arg.ID] = s
	}
	return nil
}

func (r *uploadFakeRepo) ListSweepableUploadSessions(_ context.Context, limit int32) ([]uuid.UUID, error) {
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

func (r *uploadFakeRepo) DeleteUploadSession(_ context.Context, id uuid.UUID) error {
	delete(r.sessions, id)
	delete(r.chunks, id)
	return nil
}

type importFakeRepo struct {
	jobs  map[uuid.UUID]sqlcgen.ImportJob
	order []uuid.UUID
}

func newImportFakeRepo() *importFakeRepo {
	return &importFakeRepo{jobs: map[uuid.UUID]sqlcgen.ImportJob{}}
}

func (r *importFakeRepo) EnqueueImportJob(_ context.Context, arg sqlcgen.EnqueueImportJobParams) (sqlcgen.ImportJob, error) {
	for _, id := range r.order {
		j := r.jobs[id]
		if j.VideoID == arg.VideoID && (j.State == "pending" || j.State == "running") {
			return sqlcgen.ImportJob{}, pgx.ErrNoRows // single active per video
		}
	}
	resolver := arg.Resolver
	if resolver == "" {
		resolver = "direct"
	}
	j := sqlcgen.ImportJob{
		ID:            uuid.New(),
		VideoID:       arg.VideoID,
		Url:           arg.Url,
		State:         "pending",
		Resolver:      resolver,
		NextAttemptAt: time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
	return j, nil
}

func (r *importFakeRepo) SetImportJobStage(_ context.Context, arg sqlcgen.SetImportJobStageParams) error {
	if j, ok := r.jobs[arg.ID]; ok {
		j.Stage = arg.Stage
		j.UpdatedAt = time.Now()
		r.jobs[arg.ID] = j
	}
	return nil
}

func (r *importFakeRepo) SetImportJobResolver(_ context.Context, arg sqlcgen.SetImportJobResolverParams) error {
	if j, ok := r.jobs[arg.ID]; ok {
		j.Resolver = arg.Resolver
		j.Stage = arg.Stage
		j.UpdatedAt = time.Now()
		r.jobs[arg.ID] = j
	}
	return nil
}

func (r *importFakeRepo) GetLatestImportJobByVideo(_ context.Context, videoID uuid.UUID) (sqlcgen.ImportJob, error) {
	for i := len(r.order) - 1; i >= 0; i-- {
		if j := r.jobs[r.order[i]]; j.VideoID == videoID {
			return j, nil
		}
	}
	return sqlcgen.ImportJob{}, pgx.ErrNoRows
}

func (r *importFakeRepo) ClaimDueImportJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueImportJobsRow, error) {
	var rows []sqlcgen.ClaimDueImportJobsRow
	for _, id := range r.order {
		j := r.jobs[id]
		if j.State == "pending" && !j.NextAttemptAt.After(time.Now()) {
			j.State = "running"
			r.jobs[id] = j
			rows = append(rows, sqlcgen.ClaimDueImportJobsRow{ID: j.ID, VideoID: j.VideoID, Url: j.Url, Attempts: j.Attempts, Resolver: j.Resolver, Stage: j.Stage})
			if int32(len(rows)) >= limit {
				break
			}
		}
	}
	return rows, nil
}

func (r *importFakeRepo) CompleteImportJob(_ context.Context, id uuid.UUID) error {
	if j, ok := r.jobs[id]; ok {
		j.State = "done"
		j.Error = ""
		j.Stage = ""
		j.UpdatedAt = time.Now()
		r.jobs[id] = j
	}
	return nil
}

func (r *importFakeRepo) RescheduleImportJob(_ context.Context, arg sqlcgen.RescheduleImportJobParams) error {
	if j, ok := r.jobs[arg.ID]; ok {
		j.State = "pending"
		j.Attempts++
		j.NextAttemptAt = arg.NextAttemptAt
		j.Error = arg.Error
		j.Stage = ""
		j.UpdatedAt = time.Now()
		r.jobs[arg.ID] = j
	}
	return nil
}

func (r *importFakeRepo) FailImportJob(_ context.Context, arg sqlcgen.FailImportJobParams) error {
	if j, ok := r.jobs[arg.ID]; ok {
		j.State = "failed"
		j.Attempts++
		j.Error = arg.Error
		j.Stage = ""
		j.UpdatedAt = time.Now()
		r.jobs[arg.ID] = j
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP round-trip tests
// ---------------------------------------------------------------------------

// putChunkAuth PUTs raw bytes to a chunk endpoint with a bearer token.
func putChunkAuth(srv *Server, uploadID string, n int, body []byte, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/uploads/"+uploadID+"/chunks/"+strconv.Itoa(n), bytes.NewReader(body))
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// openUploadSession opens a resumable upload and returns the upload id.
func openUploadSession(t *testing.T, srv *Server, videoID, filename string, size int, token string) uploadSessionResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+videoID+"/upload-session",
		`{"size":`+strconv.Itoa(size)+`,"filename":"`+filename+`"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open upload session = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out uploadSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

// TestResumableUploadRoundTrip drives the full protocol over HTTP: open a
// session (16-byte test chunk size), PUT the chunks OUT OF ORDER, re-PUT one
// (idempotent), read the resume/progress status, then complete → published, and
// verify the assembled original is served byte-for-byte.
func TestResumableUploadRoundTrip(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Chunked","privacy":"public"}`)

	c0 := []byte("0123456789ABCDEF") // 16
	c1 := []byte("GHIJKLMNOPQRSTUV") // 16
	c2 := []byte("WXYZ0123")         // 8
	want := append(append(append([]byte{}, c0...), c1...), c2...)

	sess := openUploadSession(t, srv, id, "clip.mp4", len(want), tok)
	if sess.ChunkSize != 16 || sess.TotalChunks != 3 {
		t.Fatalf("session chunk_size=%d total_chunks=%d, want 16/3", sess.ChunkSize, sess.TotalChunks)
	}

	// Out-of-order: last chunk first, then the first, then the middle.
	if rec := putChunkAuth(srv, sess.UploadID, 2, c2, tok); rec.Code != http.StatusOK {
		t.Fatalf("put chunk 2 = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putChunkAuth(srv, sess.UploadID, 0, c0, tok); rec.Code != http.StatusOK {
		t.Fatalf("put chunk 0 = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Idempotent re-PUT of chunk 0 must not double-count.
	if rec := putChunkAuth(srv, sess.UploadID, 0, c0, tok); rec.Code != http.StatusOK {
		t.Fatalf("re-put chunk 0 = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := putChunkAuth(srv, sess.UploadID, 1, c1, tok); rec.Code != http.StatusOK {
		t.Fatalf("put chunk 1 = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Resume contract: GET status lists every received index once and the byte total.
	statusRec := sendJSONAuth(srv, http.MethodGet, "/api/v1/uploads/"+sess.UploadID, "", tok)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("get status = %d", statusRec.Code)
	}
	var st uploadStatusResponse
	_ = json.Unmarshal(statusRec.Body.Bytes(), &st)
	if got := st.ReceivedChunks; len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("received chunks = %v, want [0 1 2]", got)
	}
	if st.BytesReceived != int64(len(want)) || st.State != "active" {
		t.Fatalf("status bytes=%d state=%q, want %d/active", st.BytesReceived, st.State, len(want))
	}

	// Complete → published.
	compRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/uploads/"+sess.UploadID+"/complete", "", tok)
	if compRec.Code != http.StatusCreated {
		t.Fatalf("complete = %d; body=%s", compRec.Code, compRec.Body.String())
	}
	var comp uploadVideoFileResponse
	_ = json.Unmarshal(compRec.Body.Bytes(), &comp)
	if comp.Video.State != "published" {
		t.Errorf("state after complete = %q, want published", comp.Video.State)
	}
	if comp.File.SizeBytes != int64(len(want)) {
		t.Errorf("stored size = %d, want %d", comp.File.SizeBytes, len(want))
	}

	// The assembled original is served byte-for-byte.
	origRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(origRec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+id+"/original", nil))
	if origRec.Code != http.StatusOK {
		t.Fatalf("get original = %d", origRec.Code)
	}
	if got := origRec.Body.Bytes(); string(got) != string(want) {
		t.Errorf("assembled original = %q, want %q", got, want)
	}

	// A second complete on the finished session is 409 (no longer active).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/uploads/"+sess.UploadID+"/complete", "", tok); rec.Code != http.StatusConflict {
		t.Errorf("re-complete = %d, want 409", rec.Code)
	}
}

// TestResumableUploadCompleteMissingChunk: completing before every chunk has
// landed is 422.
func TestResumableUploadCompleteMissingChunk(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	sess := openUploadSession(t, srv, id, "clip.mp4", 40, tok) // 3 chunks
	// Only chunk 0.
	_ = putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789ABCDEF"), tok)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/uploads/"+sess.UploadID+"/complete", "", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("complete with missing chunks = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestResumableUploadChunkErrors: a wrong-sized middle chunk is 422, an
// oversized chunk is 413, and an out-of-range index is 422.
func TestResumableUploadChunkErrors(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)
	sess := openUploadSession(t, srv, id, "clip.mp4", 40, tok) // chunks 16/16/8

	// Chunk 0 must be exactly 16 bytes; 10 is a mismatch → 422.
	if rec := putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789"), tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("short chunk 0 = %d, want 422", rec.Code)
	}
	// Chunk 0 larger than 16 → 413.
	if rec := putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789ABCDEFG"), tok); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized chunk 0 = %d, want 413", rec.Code)
	}
	// Index 3 is out of range (only 0..2) → 422.
	if rec := putChunkAuth(srv, sess.UploadID, 3, []byte("x"), tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("out-of-range index = %d, want 422", rec.Code)
	}
}

// TestResumableUploadCancel: DELETE cancels the session; afterwards status,
// chunk PUT, and complete all reflect that it is gone/inactive.
func TestResumableUploadCancel(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)
	sess := openUploadSession(t, srv, id, "clip.mp4", 40, tok)
	_ = putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789ABCDEF"), tok)

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/uploads/"+sess.UploadID, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", rec.Code)
	}
	// Cancelled session no longer accepts chunks (409).
	if rec := putChunkAuth(srv, sess.UploadID, 1, []byte("GHIJKLMNOPQRSTUV"), tok); rec.Code != http.StatusConflict {
		t.Errorf("put after cancel = %d, want 409", rec.Code)
	}
	// Idempotent re-cancel still 204.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/uploads/"+sess.UploadID, "", tok); rec.Code != http.StatusNoContent {
		t.Errorf("re-cancel = %d, want 204", rec.Code)
	}
}

// TestUploadSessionUpfrontValidation: the create-session endpoint rejects a
// non-video extension (415), an oversized declared size (413), and a size over
// the caller's quota (422 quota_exceeded) — all before any chunk is sent.
func TestUploadSessionUpfrontValidation(t *testing.T) {
	// 415: non-video extension.
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":100,"filename":"notavideo.txt"}`, tok); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("txt filename = %d, want 415", rec.Code)
	}
	// 413: bigger than UploadMaxSize (testConfig 64K).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":100000,"filename":"clip.mp4"}`, tok); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize = %d, want 413", rec.Code)
	}
	// 422: missing/invalid fields.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":0,"filename":"clip.mp4"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("zero size = %d, want 422", rec.Code)
	}
}

// TestUploadSessionQuotaUpfront: with a tiny quota, opening a session for a file
// that would not fit is 422 quota_exceeded up front.
func TestUploadSessionQuotaUpfront(t *testing.T) {
	srv := videoServerCfg(t, quotaCfg(10)) // 10-byte quota
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-quota session = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if errorCode(t, rec) != "quota_exceeded" {
		t.Errorf("body = %s, want quota_exceeded code", rec.Body.String())
	}
}

// TestUploadSessionOwnershipAndAuth: anon is 401, a non-owner is 404, and an
// unknown session id is 404 on every session route.
func TestUploadSessionOwnershipAndAuth(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"x"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Anonymous open → 401.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon open = %d, want 401", rec.Code)
	}
	// Non-owner open → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner open = %d, want 404", rec.Code)
	}
	// Owner opens; a different user cannot see/PUT/complete it.
	sess := openUploadSession(t, srv, id, "clip.mp4", 40, ownerTok)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/uploads/"+sess.UploadID, "", otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner status = %d, want 404", rec.Code)
	}
	if rec := putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789ABCDEF"), otherTok); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner put = %d, want 404", rec.Code)
	}
	// Unknown session id → 404.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/uploads/"+uuid.NewString(), "", ownerTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown session status = %d, want 404", rec.Code)
	}
}

// TestListMyUploadsResumeContract drives GET /api/v1/me/uploads (UPLOAD-03): an
// authenticated caller sees only their own ACTIVE sessions, with the persisted
// file_fingerprint and the received-chunk count; the ?fingerprint= filter
// narrows the list; anon is 401.
func TestListMyUploadsResumeContract(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	// Anonymous → 401.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}

	// Empty for a caller with no sessions (never null).
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var empty activeUploadsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &empty)
	if empty.Uploads == nil || len(empty.Uploads) != 0 {
		t.Errorf("empty list = %+v, want non-null empty slice", empty.Uploads)
	}

	// Open a session WITH a fingerprint (40 bytes → 3 chunks of 16/16/8) and land
	// one chunk.
	openRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":40,"filename":"clip.mp4","file_fingerprint":"fp-1"}`, tok)
	if openRec.Code != http.StatusCreated {
		t.Fatalf("open w/ fingerprint = %d; body=%s", openRec.Code, openRec.Body.String())
	}
	var sess uploadSessionResponse
	_ = json.Unmarshal(openRec.Body.Bytes(), &sess)
	if r := putChunkAuth(srv, sess.UploadID, 0, []byte("0123456789ABCDEF"), tok); r.Code != http.StatusOK {
		t.Fatalf("put chunk 0 = %d", r.Code)
	}

	// The resume list shows the session with its fingerprint + received-chunk count.
	rec = sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list activeUploadsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Uploads) != 1 {
		t.Fatalf("list len = %d, want 1 (body=%s)", len(list.Uploads), rec.Body.String())
	}
	got := list.Uploads[0]
	if got.UploadID != sess.UploadID || got.VideoID != id {
		t.Errorf("entry ids = %s/%s, want %s/%s", got.UploadID, got.VideoID, sess.UploadID, id)
	}
	if got.FileFingerprint != "fp-1" || got.ReceivedChunks != 1 || got.TotalChunks != 3 || got.Size != 40 {
		t.Errorf("entry = %+v, want fp-1 / 1 of 3 / size 40", got)
	}

	// ?fingerprint=fp-1 matches; a non-matching fingerprint returns empty.
	if r := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads?fingerprint=fp-1", "", tok); r.Code != http.StatusOK {
		t.Fatalf("filtered list = %d", r.Code)
	} else {
		var f activeUploadsResponse
		_ = json.Unmarshal(r.Body.Bytes(), &f)
		if len(f.Uploads) != 1 || f.Uploads[0].UploadID != sess.UploadID {
			t.Errorf("fingerprint filter = %+v, want the fp-1 session", f.Uploads)
		}
	}
	if r := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads?fingerprint=nope", "", tok); r.Code == http.StatusOK {
		var f activeUploadsResponse
		_ = json.Unmarshal(r.Body.Bytes(), &f)
		if len(f.Uploads) != 0 {
			t.Errorf("non-matching fingerprint = %+v, want empty", f.Uploads)
		}
	} else {
		t.Errorf("non-matching filter = %d, want 200", r.Code)
	}

	// A different user never sees the owner's session.
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if r := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/uploads", "", otherTok); r.Code != http.StatusOK {
		t.Fatalf("other-user list = %d", r.Code)
	} else {
		var f activeUploadsResponse
		_ = json.Unmarshal(r.Body.Bytes(), &f)
		if len(f.Uploads) != 0 {
			t.Errorf("other-user list = %+v, want empty (owner isolation)", f.Uploads)
		}
	}
}

// TestUploadSessionBatchGuard drives the batch-upload guard over HTTP (UPLOAD-10,
// W2.C3): with UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER=2 a caller can hold two active
// sessions; the third open is 429 with the stable code too_many_active_uploads a
// batch client queues on; cancelling an in-flight session frees a slot; and the
// budget is per-user.
func TestUploadSessionBatchGuard(t *testing.T) {
	cfg := testConfig()
	cfg.UploadMaxActiveSessionsPerUser = 2
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	body := `{"size":40,"filename":"clip.mp4"}`

	// Two opens fit under the cap of 2.
	first := openUploadSession(t, srv, id, "clip.mp4", 40, tok)
	_ = openUploadSession(t, srv, id, "clip.mp4", 40, tok)

	// The third is refused 429 with the stable code (a fairness signal, not 5xx).
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", body, tok)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third open = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "too_many_active_uploads" {
		t.Errorf("error code = %q, want too_many_active_uploads", code)
	}

	// Cancelling an in-flight session frees a slot for the queued upload.
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/uploads/"+first.UploadID, "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", body, tok); r.Code != http.StatusCreated {
		t.Errorf("open after cancel = %d, want 201; body=%s", r.Code, r.Body.String())
	}

	// A different user has their own independent budget.
	otherTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	id2 := createVideo(t, srv, otherTok, "bob", `{"title":"y"}`)
	if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id2+"/upload-session", body, otherTok); r.Code != http.StatusCreated {
		t.Errorf("other-user open = %d, want 201 (per-user budget); body=%s", r.Code, r.Body.String())
	}
}

// TestUploadSessionBatchGuardDisabled: with UPLOAD_MAX_ACTIVE_SESSIONS_PER_USER=0
// the guard is off — a caller can open many concurrent sessions.
func TestUploadSessionBatchGuardDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.UploadMaxActiveSessionsPerUser = 0
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	for i := 0; i < 8; i++ {
		if r := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session", `{"size":40,"filename":"clip.mp4"}`, tok); r.Code != http.StatusCreated {
			t.Fatalf("open %d with guard disabled = %d; body=%s", i, r.Code, r.Body.String())
		}
	}
}

// TestUploadSessionFingerprintTooLong: a fingerprint over 128 chars is rejected
// 422 at create time (the opaque string is bounded).
func TestUploadSessionFingerprintTooLong(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	long := strings.Repeat("a", 129)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/upload-session",
		`{"size":40,"filename":"clip.mp4","file_fingerprint":"`+long+`"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("129-char fingerprint = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}
