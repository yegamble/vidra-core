package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
		Privacy: a.Privacy, State: "offline", Permanent: a.Permanent, CreatedAt: now, UpdatedAt: now,
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
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		OwnerID: ch.OwnerID, ChannelHandle: ch.Handle, ChannelDisplayName: ch.DisplayName,
	}, nil
}

func (f *liveFakeRepo) ListLiveStreamsByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListLiveStreamsByChannelRow, error) {
	var out []sqlcgen.ListLiveStreamsByChannelRow
	for _, r := range f.rows {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListLiveStreamsByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
	}
	return out, nil
}

func (f *liveFakeRepo) UpdateLiveStreamKey(_ context.Context, a sqlcgen.UpdateLiveStreamKeyParams) error {
	f.hashes[a.ID] = a.StreamKeyHash
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
