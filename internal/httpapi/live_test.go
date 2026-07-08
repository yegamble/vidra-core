package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// liveFakeRepo is an in-memory live.Repository. It resolves a stream's owning
// channel (owner_id + identity) from the shared channelFakeRepo, mirroring the
// real join in GetLiveStreamByID.
type liveFakeRepo struct {
	channels *channelFakeRepo
	rows     map[uuid.UUID]sqlcgen.CreateLiveStreamRow
	hashes   map[uuid.UUID]string
}

func newLiveFakeRepo(channels *channelFakeRepo) *liveFakeRepo {
	return &liveFakeRepo{channels: channels, rows: map[uuid.UUID]sqlcgen.CreateLiveStreamRow{}, hashes: map[uuid.UUID]string{}}
}

func (f *liveFakeRepo) channelByID(id uuid.UUID) (sqlcgen.Channel, bool) {
	for _, ch := range f.channels.byHandle {
		if ch.ID == id {
			return ch, true
		}
	}
	return sqlcgen.Channel{}, false
}

func (f *liveFakeRepo) CreateLiveStream(_ context.Context, a sqlcgen.CreateLiveStreamParams) (sqlcgen.CreateLiveStreamRow, error) {
	now := time.Now()
	row := sqlcgen.CreateLiveStreamRow{
		ID: uuid.New(), ChannelID: a.ChannelID, Title: a.Title, Description: a.Description,
		Privacy: a.Privacy, State: "offline", Permanent: a.Permanent, ReplayEnabled: a.ReplayEnabled,
		CreatedAt: now, UpdatedAt: now,
	}
	f.rows[row.ID] = row
	f.hashes[row.ID] = a.StreamKeyHash
	return row, nil
}

func (f *liveFakeRepo) GetLiveStreamByID(_ context.Context, id uuid.UUID) (sqlcgen.GetLiveStreamByIDRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return sqlcgen.GetLiveStreamByIDRow{}, errors.New("not found")
	}
	ch, _ := f.channelByID(r.ChannelID)
	return sqlcgen.GetLiveStreamByIDRow{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
		StartedAt: r.StartedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		OwnerID: ch.OwnerID, ChannelHandle: ch.Handle, ChannelDisplayName: ch.DisplayName,
	}, nil
}

func (f *liveFakeRepo) ListLiveStreamsByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListLiveStreamsByChannelRow, error) {
	var out []sqlcgen.ListLiveStreamsByChannelRow
	for _, r := range f.rows {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListLiveStreamsByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
				StartedAt: r.StartedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
	}
	return out, nil
}

// ListLivePublicStreams mirrors the SQL: currently-live PUBLIC streams only,
// most-recently-started first, with limit/offset paging.
func (f *liveFakeRepo) ListLivePublicStreams(_ context.Context, a sqlcgen.ListLivePublicStreamsParams) ([]sqlcgen.ListLivePublicStreamsRow, error) {
	var rows []sqlcgen.CreateLiveStreamRow
	for _, r := range f.rows {
		if r.State == "live" && r.Privacy == "public" {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartedAt.Time.After(rows[j].StartedAt.Time) })
	lo := int(a.Offset)
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + int(a.Limit)
	if hi > len(rows) {
		hi = len(rows)
	}
	out := make([]sqlcgen.ListLivePublicStreamsRow, 0, hi-lo)
	for _, r := range rows[lo:hi] {
		ch, _ := f.channelByID(r.ChannelID)
		out = append(out, sqlcgen.ListLivePublicStreamsRow{
			ID: r.ID, Title: r.Title, Description: r.Description, StartedAt: r.StartedAt,
			ChannelHandle: ch.Handle, ChannelDisplayName: ch.DisplayName,
		})
	}
	return out, nil
}

func (f *liveFakeRepo) UpdateLiveStreamKey(_ context.Context, a sqlcgen.UpdateLiveStreamKeyParams) error {
	f.hashes[a.ID] = a.StreamKeyHash
	return nil
}

func (f *liveFakeRepo) UpdateLiveStream(_ context.Context, a sqlcgen.UpdateLiveStreamParams) (sqlcgen.UpdateLiveStreamRow, error) {
	r, ok := f.rows[a.ID]
	if !ok {
		return sqlcgen.UpdateLiveStreamRow{}, errors.New("not found")
	}
	r.Title, r.Description, r.Privacy, r.Permanent, r.ReplayEnabled = a.Title, a.Description, a.Privacy, a.Permanent, a.ReplayEnabled
	r.UpdatedAt = time.Now()
	f.rows[a.ID] = r
	return sqlcgen.UpdateLiveStreamRow{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
		StartedAt: r.StartedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func (f *liveFakeRepo) GetLiveStreamByKeyHash(_ context.Context, h string) (sqlcgen.GetLiveStreamByKeyHashRow, error) {
	for id, hash := range f.hashes {
		if hash == h {
			r := f.rows[id]
			return sqlcgen.GetLiveStreamByKeyHashRow{ID: id, ChannelID: r.ChannelID, Permanent: r.Permanent, State: r.State}, nil
		}
	}
	return sqlcgen.GetLiveStreamByKeyHashRow{}, errors.New("not found")
}

func (f *liveFakeRepo) SetLiveStreamState(_ context.Context, a sqlcgen.SetLiveStreamStateParams) error {
	if r, ok := f.rows[a.ID]; ok {
		switch {
		case a.State == "live" && r.State != "live":
			r.StartedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		case a.State != "live":
			r.StartedAt = pgtype.Timestamptz{}
		}
		r.State = a.State
		f.rows[a.ID] = r
	}
	return nil
}

func (f *liveFakeRepo) DeleteLiveStream(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.rows[id]; ok {
		delete(f.rows, id)
		delete(f.hashes, id)
		return 1, nil
	}
	return 0, nil
}

func createLiveStream(srv *Server, handle, body, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/"+handle+"/live", body, token)
}

// TestLiveStreamLifecycle covers create → list → get → regenerate key → delete.
func TestLiveStreamLifecycle(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Create returns the stream + the key (once) + an (empty) rtmp url.
	rec := createLiveStream(srv, "ada", `{"title":"My Show","permanent":true}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created createLiveStreamResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.StreamKey == "" {
		t.Fatal("create response has no stream_key")
	}
	if created.LiveStream.State != "offline" || !created.LiveStream.Permanent || created.LiveStream.Title != "My Show" {
		t.Fatalf("unexpected live stream: %+v", created.LiveStream)
	}
	id := created.LiveStream.ID

	// The owner list shows it and NEVER leaks the key.
	listRec := getWithAuth(srv, "/api/v1/channels/ada/live", tok)
	if !strings.Contains(listRec.Body.String(), "My Show") || strings.Contains(listRec.Body.String(), "stream_key") {
		t.Fatalf("list leaked key or missing stream: %s", listRec.Body.String())
	}

	// Public get (anon) returns metadata, no key.
	getRec := getWithAuth(srv, "/api/v1/live/"+id, "")
	if getRec.Code != http.StatusOK || strings.Contains(getRec.Body.String(), "stream_key") {
		t.Fatalf("get = %d, body=%s", getRec.Code, getRec.Body.String())
	}

	// Regenerate the key → a new key, different from the first.
	keyRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+id+"/key", "", tok)
	if keyRec.Code != http.StatusOK {
		t.Fatalf("regenerate = %d, want 200", keyRec.Code)
	}
	var rotated liveStreamKeyView
	_ = json.Unmarshal(keyRec.Body.Bytes(), &rotated)
	if rotated.StreamKey == "" || rotated.StreamKey == created.StreamKey {
		t.Errorf("rotated key = %q (old %q), want a new key", rotated.StreamKey, created.StreamKey)
	}

	// Delete → gone.
	if d := sendJSONAuth(srv, http.MethodDelete, "/api/v1/live/"+id, "", tok); d.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", d.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id, ""); g.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", g.Code)
	}
}

func TestLiveStreamAuthzAndPrivacy(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Anonymous create → 401; non-owner create on ada's channel → 403.
	if rec := postTo(srv, "/api/v1/channels/ada/live", `{"title":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon create = %d, want 401", rec.Code)
	}
	if rec := createLiveStream(srv, "ada", `{"title":"x"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner create = %d, want 403", rec.Code)
	}

	// A private stream is 404 to anon/others, 200 to the owner.
	rec := createLiveStream(srv, "ada", `{"title":"secret","privacy":"private"}`, ada)
	var created createLiveStreamResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created.LiveStream.ID
	if g := getWithAuth(srv, "/api/v1/live/"+id, ""); g.Code != http.StatusNotFound {
		t.Errorf("anon get private = %d, want 404", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id, bob); g.Code != http.StatusNotFound {
		t.Errorf("other get private = %d, want 404", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id, ada); g.Code != http.StatusOK {
		t.Errorf("owner get private = %d, want 200", g.Code)
	}

	// Non-owner regenerate/delete → 404 (existence not leaked).
	if k := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+id+"/key", "", bob); k.Code != http.StatusNotFound {
		t.Errorf("non-owner regenerate = %d, want 404", k.Code)
	}
	if d := sendJSONAuth(srv, http.MethodDelete, "/api/v1/live/"+id, "", bob); d.Code != http.StatusNotFound {
		t.Errorf("non-owner delete = %d, want 404", d.Code)
	}
}

func ingestReq(srv *Server, path, body, secret string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	if secret != "" {
		req.Header.Set("X-Ingest-Secret", secret)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestLiveIngestDisabledWithoutSecret: with no LIVE_INGEST_SECRET, the ingest
// hooks are 404 (not enabled) regardless of the body.
func TestLiveIngestDisabledWithoutSecret(t *testing.T) {
	srv := videoServer(t) // testConfig has no ingest secret
	if rec := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"x"}`, "x"); rec.Code != http.StatusNotFound {
		t.Fatalf("ingest start without secret configured = %d, want 404", rec.Code)
	}
}

// TestLiveIngestFlow: with a configured secret, the media server flips a stream
// live/ended by its key; a bad secret is 401 and an unknown key is 404.
func TestLiveIngestFlow(t *testing.T) {
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	rec := createLiveStream(srv, "ada", `{"title":"Show"}`, tok)
	var created createLiveStreamResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created.LiveStream.ID
	key := created.StreamKey

	// Wrong secret → 401.
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "wrong"); r.Code != http.StatusUnauthorized {
		t.Errorf("bad secret = %d, want 401", r.Code)
	}
	// Unknown key → 404.
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"nope"}`, "s3cret"); r.Code != http.StatusNotFound {
		t.Errorf("unknown key = %d, want 404", r.Code)
	}
	// Correct secret + key → 200, stream goes live.
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusOK {
		t.Fatalf("ingest start = %d, want 200; body=%s", r.Code, r.Body.String())
	}
	var live liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, tok).Body.Bytes(), &live)
	if live.State != "live" {
		t.Errorf("state after ingest start = %q, want live", live.State)
	}

	// Stop → the one-shot stream ends.
	if r := ingestReq(srv, "/api/v1/live/ingest/stop", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusNoContent {
		t.Fatalf("ingest stop = %d, want 204", r.Code)
	}
	var ended liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, tok).Body.Bytes(), &ended)
	if ended.State != "ended" {
		t.Errorf("state after ingest stop = %q, want ended", ended.State)
	}
}

// ingestForm posts an nginx-rtmp style form callback (name=<key|id>) with the
// ingest secret header, mirroring the media server. It disables redirect
// following so the 302 rename is observable.
func ingestForm(srv *Server, path, name, secret string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body := url.Values{"name": {name}, "call": {"publish"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	if secret != "" {
		req.Header.Set("X-Ingest-Secret", secret)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestLiveIngestFormRedirectRename: the nginx-rtmp form callback flips the stream
// live AND receives a 302 whose last path segment is the stream ID (the rename
// that keeps the raw key off disk); a post-rename re-invocation with the id is
// allowed (200) without another redirect.
func TestLiveIngestFormRedirectRename(t *testing.T) {
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	var created createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Show"}`, tok).Body.Bytes(), &created)
	id, key := created.LiveStream.ID, created.StreamKey

	r := ingestForm(srv, "/api/v1/live/ingest/start", key, "s3cret")
	if r.Code != http.StatusFound {
		t.Fatalf("form start = %d, want 302; body=%s", r.Code, r.Body.String())
	}
	loc := r.Header().Get("Location")
	if !strings.HasSuffix(loc, "/"+id) {
		t.Errorf("redirect Location = %q, want it to rename to the stream id %q", loc, id)
	}
	// State flipped live.
	var live liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, tok).Body.Bytes(), &live)
	if live.State != "live" {
		t.Errorf("state = %q, want live", live.State)
	}
	// Re-invocation with the renamed id → 200 (allow), no second redirect.
	if r := ingestForm(srv, "/api/v1/live/ingest/start", id, "s3cret"); r.Code != http.StatusOK {
		t.Errorf("re-publish by id = %d, want 200 (no redirect loop)", r.Code)
	}
	// Stop by the id (post-rename media-server path) ends the one-shot stream.
	if r := ingestForm(srv, "/api/v1/live/ingest/stop", id, "s3cret"); r.Code != http.StatusNoContent {
		t.Fatalf("form stop by id = %d, want 204", r.Code)
	}
	var ended liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, tok).Body.Bytes(), &ended)
	if ended.State != "ended" {
		t.Errorf("state after stop = %q, want ended", ended.State)
	}
}

// liveHLSServer builds a server whose LIVE_HLS_ROOT is a temp dir, returns it +
// the root so tests can seed HLS files, and configures the ingest secret so a
// stream can be flipped live.
func liveHLSServer(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := testConfig()
	cfg.LiveHLSRoot = t.TempDir()
	cfg.LiveIngestSecret = "s3cret"
	return videoServerCfg(t, cfg), cfg.LiveHLSRoot
}

func TestLiveHLSServing(t *testing.T) {
	srv, root := liveHLSServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	var created createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Show"}`, tok).Body.Bytes(), &created)
	id, key := created.LiveStream.ID, created.StreamKey

	// Seed the media server's HLS output for this stream id.
	if err := os.WriteFile(filepath.Join(root, id+".m3u8"), []byte("#EXTM3U\n"+id+"-0.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id+"-0.ts"), []byte("TSDATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Before going live: HLS is 404 (only served while live).
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", ""); g.Code != http.StatusNotFound {
		t.Errorf("hls before live = %d, want 404", g.Code)
	}

	// Flip live via the ingest hook.
	if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusOK {
		t.Fatalf("ingest start = %d", r.Code)
	}

	// The live view now advertises hls_url.
	var lv liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, "").Body.Bytes(), &lv)
	if lv.HLSURL != "/api/v1/live/"+id+"/hls/master.m3u8" {
		t.Errorf("hls_url = %q, want the live playlist path", lv.HLSURL)
	}

	// Master playlist + segment serve from LIVE_HLS_ROOT.
	m := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", "")
	if m.Code != http.StatusOK || !strings.Contains(m.Body.String(), "#EXTM3U") {
		t.Fatalf("master = %d body=%q", m.Code, m.Body.String())
	}
	if ct := m.Header().Get("Content-Type"); !strings.Contains(ct, "mpegurl") {
		t.Errorf("master content-type = %q, want an m3u8 type", ct)
	}
	seg := getWithAuth(srv, "/api/v1/live/"+id+"/hls/"+id+"-0.ts", "")
	if seg.Code != http.StatusOK || seg.Body.String() != "TSDATA" {
		t.Fatalf("segment = %d body=%q", seg.Code, seg.Body.String())
	}

	// A file name that is not this stream's playlist/segment is 404.
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/"+uuid.New().String()+"-0.ts", ""); g.Code != http.StatusNotFound {
		t.Errorf("foreign segment = %d, want 404", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/evil.ts", ""); g.Code != http.StatusNotFound {
		t.Errorf("non-id segment = %d, want 404", g.Code)
	}
}

func TestLiveHLSDisabledWithoutRoot(t *testing.T) {
	// testConfig has no LIVE_HLS_ROOT: even a live stream's HLS is 404.
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	var created createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Show"}`, tok).Body.Bytes(), &created)
	id, key := created.LiveStream.ID, created.StreamKey
	_ = ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret")

	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", ""); g.Code != http.StatusNotFound {
		t.Errorf("hls without LIVE_HLS_ROOT = %d, want 404", g.Code)
	}
	// And the view omits hls_url.
	var lv liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+id, "").Body.Bytes(), &lv)
	if lv.HLSURL != "" {
		t.Errorf("hls_url = %q, want empty when serving is not configured", lv.HLSURL)
	}
}

func TestLiveHLSPrivacyGate(t *testing.T) {
	srv, root := liveHLSServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	var created createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"secret","privacy":"private"}`, ada).Body.Bytes(), &created)
	id, key := created.LiveStream.ID, created.StreamKey
	_ = os.WriteFile(filepath.Join(root, id+".m3u8"), []byte("#EXTM3U\n"), 0o644)
	_ = ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret")

	// Private live HLS: anon + other → 404, owner → 200.
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", ""); g.Code != http.StatusNotFound {
		t.Errorf("anon private hls = %d, want 404", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", bob); g.Code != http.StatusNotFound {
		t.Errorf("other private hls = %d, want 404", g.Code)
	}
	if g := getWithAuth(srv, "/api/v1/live/"+id+"/hls/master.m3u8", ada); g.Code != http.StatusOK {
		t.Errorf("owner private hls = %d, want 200", g.Code)
	}
}

func TestLiveStreamEdit(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	var created createLiveStreamResponse
	_ = json.Unmarshal(createLiveStream(srv, "ada", `{"title":"Before"}`, ada).Body.Bytes(), &created)
	id := created.LiveStream.ID
	if created.LiveStream.ReplayEnabled {
		t.Fatal("new stream should default replay_enabled=false")
	}

	// Owner edits title + turns on replay.
	patch := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id,
		`{"title":"After","privacy":"unlisted","permanent":true,"replay_enabled":true}`, ada)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200; body=%s", patch.Code, patch.Body.String())
	}
	var edited liveStreamView
	_ = json.Unmarshal(patch.Body.Bytes(), &edited)
	if edited.Title != "After" || edited.Privacy != "unlisted" || !edited.Permanent || !edited.ReplayEnabled {
		t.Fatalf("edited = %+v, want the updated fields", edited)
	}

	// Non-owner / anon / bad body.
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":"x"}`, bob); r.Code != http.StatusNotFound {
		t.Errorf("non-owner patch = %d, want 404", r.Code)
	}
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+id, `{"title":""}`, ada); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty title patch = %d, want 422", r.Code)
	}
}

func TestLiveStreamValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := createLiveStream(srv, "ada", `{"title":""}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty title = %d, want 422", rec.Code)
	}
	if rec := createLiveStream(srv, "ada", `{"title":"ok","privacy":"weird"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad privacy = %d, want 422", rec.Code)
	}
	// Unknown channel → 404.
	if rec := createLiveStream(srv, "ghost", `{"title":"x"}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", rec.Code)
	}
}

// TestListLivePublicStreams: GET /api/v1/live returns only currently-live PUBLIC
// streams (never unlisted/private, never offline), is_live=true, started_at set,
// no stream key; it paginates; and an anon caller can read it.
func TestListLivePublicStreams(t *testing.T) {
	cfg := testConfig()
	cfg.LiveIngestSecret = "s3cret"
	srv := videoServerCfg(t, cfg)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Create four streams (all start offline).
	create := func(title, privacy string) (id, key string) {
		body := `{"title":"` + title + `","privacy":"` + privacy + `"}`
		var out createLiveStreamResponse
		_ = json.Unmarshal(createLiveStream(srv, "ada", body, tok).Body.Bytes(), &out)
		return out.LiveStream.ID, out.StreamKey
	}
	pubAID, pubAKey := create("Public A", "public")
	pubBID, pubBKey := create("Public B", "public")
	_, unlistedKey := create("Unlisted show", "unlisted")
	privID, privKey := create("Private show", "private")

	// Nothing is live yet → empty listing.
	var empty liveStreamPublicListResponse
	rec := getWithAuth(srv, "/api/v1/live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list (none live) = %d, want 200", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &empty)
	if len(empty.LiveStreams) != 0 {
		t.Fatalf("want 0 live streams initially, got %d", len(empty.LiveStreams))
	}

	// Bring all four live via the ingest hook.
	for _, key := range []string{pubAKey, pubBKey, unlistedKey, privKey} {
		if r := ingestReq(srv, "/api/v1/live/ingest/start", `{"stream_key":"`+key+`"}`, "s3cret"); r.Code != http.StatusOK {
			t.Fatalf("ingest start = %d", r.Code)
		}
	}

	// Anon listing shows ONLY the two public live streams.
	var got liveStreamPublicListResponse
	body := getWithAuth(srv, "/api/v1/live", "")
	if body.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", body.Code)
	}
	if strings.Contains(body.Body.String(), "stream_key") {
		t.Fatalf("public listing leaked a stream key: %s", body.Body.String())
	}
	// Private/unlisted titles must NEVER appear.
	if strings.Contains(body.Body.String(), "Unlisted show") || strings.Contains(body.Body.String(), "Private show") {
		t.Fatalf("public listing leaked a non-public stream: %s", body.Body.String())
	}
	_ = json.Unmarshal(body.Body.Bytes(), &got)
	if len(got.LiveStreams) != 2 {
		t.Fatalf("want 2 public live streams, got %d: %+v", len(got.LiveStreams), got.LiveStreams)
	}
	seen := map[string]bool{}
	for _, cd := range got.LiveStreams {
		seen[cd.ID] = true
		if !cd.IsLive {
			t.Errorf("card %s is_live = false, want true", cd.ID)
		}
		if cd.StartedAt == nil {
			t.Errorf("card %s missing started_at", cd.ID)
		}
		if cd.ChannelHandle != "ada" {
			t.Errorf("card %s channel_handle = %q, want ada", cd.ID, cd.ChannelHandle)
		}
	}
	if !seen[pubAID] || !seen[pubBID] {
		t.Errorf("expected both public streams %s,%s; got %+v", pubAID, pubBID, seen)
	}
	if seen[privID] {
		t.Error("private stream appeared in the public listing")
	}

	// Pagination: limit=1 returns exactly one; offset=1 returns the other.
	var p1, p2 liveStreamPublicListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live?limit=1", "").Body.Bytes(), &p1)
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live?limit=1&offset=1", "").Body.Bytes(), &p2)
	if len(p1.LiveStreams) != 1 || len(p2.LiveStreams) != 1 {
		t.Fatalf("pagination sizes = %d,%d, want 1,1", len(p1.LiveStreams), len(p2.LiveStreams))
	}
	if p1.LiveStreams[0].ID == p2.LiveStreams[0].ID {
		t.Errorf("offset did not advance: both pages = %s", p1.LiveStreams[0].ID)
	}
	if p1.Limit != 1 || p2.Offset != 1 {
		t.Errorf("echoed paging = limit %d / offset %d, want 1 / 1", p1.Limit, p2.Offset)
	}

	// The single-stream detail now carries started_at once live.
	var detail liveStreamView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live/"+pubAID, "").Body.Bytes(), &detail)
	if detail.StartedAt == nil {
		t.Error("live detail missing started_at")
	}

	// Ending a stream drops it from the listing.
	if r := ingestReq(srv, "/api/v1/live/ingest/stop", `{"stream_key":"`+pubAKey+`"}`, "s3cret"); r.Code != http.StatusNoContent {
		t.Fatalf("ingest stop = %d", r.Code)
	}
	var after liveStreamPublicListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/live", "").Body.Bytes(), &after)
	if len(after.LiveStreams) != 1 || after.LiveStreams[0].ID != pubBID {
		t.Fatalf("after ending Public A, want only Public B; got %+v", after.LiveStreams)
	}
}

// TestListLivePublicStreamsUngatedRead: the public listing is a read surface —
// like the sibling GET /live/{id}, it is NOT gated on the live feature toggle
// (that guards creation/ingest). With live disabled it still returns 200 with an
// empty list rather than 403/404.
func TestListLivePublicStreamsUngatedRead(t *testing.T) {
	cfg := testConfig()
	cfg.LiveEnabled = false
	srv := videoServerCfg(t, cfg)
	rec := getWithAuth(srv, "/api/v1/live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list with live disabled = %d, want 200 (ungated read)", rec.Code)
	}
	var out liveStreamPublicListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.LiveStreams) != 0 {
		t.Fatalf("want 0 live streams, got %d", len(out.LiveStreams))
	}
}
