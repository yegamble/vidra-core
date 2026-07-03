package atproto

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// errNoRows is the fake repository's not-found error.
var errNoRows = errors.New("atproto fake: not found")

// fakeRepo is an in-memory Repository for unit tests.
type fakeRepo struct {
	mu       sync.Mutex
	accounts map[uuid.UUID]sqlcgen.AtprotoAccount
	posts    map[uuid.UUID]*sqlcgen.AtprotoPost // keyed by post id
	byVideo  map[uuid.UUID]uuid.UUID            // video id -> post id (dedupe)
	videos   map[uuid.UUID]sqlcgen.GetATProtoPostVideoRow

	// captured for assertions
	lastUpsert *sqlcgen.UpsertATProtoAccountParams
	touched    []uuid.UUID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		accounts: map[uuid.UUID]sqlcgen.AtprotoAccount{},
		posts:    map[uuid.UUID]*sqlcgen.AtprotoPost{},
		byVideo:  map[uuid.UUID]uuid.UUID{},
		videos:   map[uuid.UUID]sqlcgen.GetATProtoPostVideoRow{},
	}
}

func (r *fakeRepo) UpsertATProtoAccount(_ context.Context, arg sqlcgen.UpsertATProtoAccountParams) (sqlcgen.AtprotoAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := arg
	r.lastUpsert = &cp
	existing, ok := r.accounts[arg.UserID]
	acct := sqlcgen.AtprotoAccount{
		UserID:            arg.UserID,
		Handle:            arg.Handle,
		Did:               arg.Did,
		PdsUrl:            arg.PdsUrl,
		AppPasswordSealed: arg.AppPasswordSealed,
		AutoPost:          arg.AutoPost,
		CreatedAt:         time.Now(),
	}
	if ok {
		acct.CreatedAt = existing.CreatedAt
		acct.LastPostedAt = existing.LastPostedAt
	}
	r.accounts[arg.UserID] = acct
	return acct, nil
}

func (r *fakeRepo) GetATProtoAccount(_ context.Context, userID uuid.UUID) (sqlcgen.AtprotoAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[userID]
	if !ok {
		return sqlcgen.AtprotoAccount{}, errNoRows
	}
	return a, nil
}

func (r *fakeRepo) DeleteATProtoAccount(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.accounts, userID)
	return nil
}

func (r *fakeRepo) TouchATProtoAccountPosted(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = append(r.touched, userID)
	if a, ok := r.accounts[userID]; ok {
		a.LastPostedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		r.accounts[userID] = a
	}
	return nil
}

func (r *fakeRepo) EnqueueATProtoPost(_ context.Context, videoID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byVideo[videoID]; ok {
		return nil // dedupe (ON CONFLICT DO NOTHING)
	}
	id := uuid.New()
	r.posts[id] = &sqlcgen.AtprotoPost{
		ID:            id,
		VideoID:       videoID,
		State:         "pending",
		NextAttemptAt: time.Now().Add(-time.Second),
	}
	r.byVideo[videoID] = id
	return nil
}

func (r *fakeRepo) ClaimDueATProtoPosts(_ context.Context, limit int32) ([]sqlcgen.ClaimDueATProtoPostsRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sqlcgen.ClaimDueATProtoPostsRow
	for _, p := range r.posts {
		if p.State != "pending" || p.NextAttemptAt.After(time.Now()) {
			continue
		}
		out = append(out, sqlcgen.ClaimDueATProtoPostsRow{ID: p.ID, VideoID: p.VideoID, Attempts: p.Attempts})
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeRepo) MarkATProtoPostDone(_ context.Context, arg sqlcgen.MarkATProtoPostDoneParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.posts[arg.ID]; ok {
		p.State = "posted"
		p.PostUri = arg.PostUri
	}
	return nil
}

func (r *fakeRepo) RescheduleATProtoPost(_ context.Context, arg sqlcgen.RescheduleATProtoPostParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.posts[arg.ID]; ok {
		p.Attempts++
		p.NextAttemptAt = arg.NextAttemptAt
		p.Error = arg.Error
	}
	return nil
}

func (r *fakeRepo) FailATProtoPost(_ context.Context, arg sqlcgen.FailATProtoPostParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.posts[arg.ID]; ok {
		p.State = "failed"
		p.Attempts++
		p.Error = arg.Error
	}
	return nil
}

func (r *fakeRepo) GetATProtoPostVideo(_ context.Context, id uuid.UUID) (sqlcgen.GetATProtoPostVideoRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.videos[id]
	if !ok {
		return sqlcgen.GetATProtoPostVideoRow{}, errNoRows
	}
	return v, nil
}

// postForVideo returns the fake's post row for a video id (test helper).
func (r *fakeRepo) postForVideo(videoID uuid.UUID) *sqlcgen.AtprotoPost {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byVideo[videoID]
	if !ok {
		return nil
	}
	return r.posts[id]
}

// createSessionCall captures one CreateSession invocation.
type createSessionCall struct {
	PDSURL      string
	Identifier  string
	AppPassword string
}

// fakePDS is an in-memory PDSClient.
type fakePDS struct {
	mu sync.Mutex

	createErr    error
	createRecErr error
	uploadErr    error
	session      Session
	recordURI    string
	blob         Blob

	createCalls []createSessionCall
	recordCalls []map[string]any
	uploadCalls int
}

func newFakePDS() *fakePDS {
	return &fakePDS{
		session:   Session{DID: "did:plc:test", Handle: "alice.bsky.social", AccessJWT: "jwt-token"},
		recordURI: "at://did:plc:test/app.bsky.feed.post/abc123",
		blob:      Blob(`{"$type":"blob","ref":{"$link":"bafyfake"},"mimeType":"image/jpeg","size":10}`),
	}
}

func (f *fakePDS) CreateSession(_ context.Context, pdsURL, identifier, appPassword string) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, createSessionCall{PDSURL: pdsURL, Identifier: identifier, AppPassword: appPassword})
	if f.createErr != nil {
		return Session{}, f.createErr
	}
	return f.session, nil
}

func (f *fakePDS) UploadBlob(_ context.Context, _ string, _ Session, _ string, _ []byte) (Blob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadCalls++
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	return f.blob, nil
}

func (f *fakePDS) CreateRecord(_ context.Context, _ string, _ Session, _ string, record map[string]any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordCalls = append(f.recordCalls, record)
	if f.createRecErr != nil {
		return "", f.createRecErr
	}
	return f.recordURI, nil
}

// fakeThumbs is an in-memory Thumbnails source.
type fakeThumbs struct {
	data []byte
	mime string
	ok   bool
	err  error
}

func (f fakeThumbs) ReadThumbnail(_ context.Context, _ uuid.UUID) ([]byte, string, bool, error) {
	return f.data, f.mime, f.ok, f.err
}
