package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/peertubearchive"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type ptChannels struct{ made []string }

func (f *ptChannels) Create(_ context.Context, o uuid.UUID, in channel.CreateInput) (sqlcgen.Channel, error) {
	f.made = append(f.made, in.Handle)
	return sqlcgen.Channel{OwnerID: o, Handle: in.Handle}, nil
}
func (f *ptChannels) GetByHandle(context.Context, string) (sqlcgen.Channel, error) {
	return sqlcgen.Channel{}, channel.ErrNotFound
}

type ptPlaylists struct{ made int }

func (f *ptPlaylists) Create(context.Context, uuid.UUID, playlist.CreateInput) (sqlcgen.Playlist, error) {
	f.made++
	return sqlcgen.Playlist{}, nil
}
func (f *ptPlaylists) ListOwn(context.Context, uuid.UUID, int32, int32) ([]sqlcgen.ListPlaylistsByOwnerRow, int64, error) {
	return nil, 0, nil
}

func ptZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return b.Bytes()
}

func ptUpload(env *accountEnv, token string, raw []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if raw != nil {
		fw, _ := mw.CreateFormFile("archive", "export.zip")
		_, _ = fw.Write(raw)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/import/peertube", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPeerTubeArchiveImport(t *testing.T) {
	chans, pls := &ptChannels{}, &ptPlaylists{}
	env := newAccountEnv(t, WithPeerTubeArchiveImporter(&peertubearchive.Importer{Channels: chans, Playlists: pls}))
	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	good := ptZip(t, map[string]string{
		"peertube/channels.json":        `{"channels":[{"name":"ada_chan","displayName":"Ada"}]}`,
		"peertube/video-playlists.json": `{"videoPlaylists":[{"displayName":"Faves","privacy":3,"type":1,"elements":[{}]}]}`,
		"peertube/videos.json":          `{"videos":[{},{}]}`,
	})

	if rec := ptUpload(env, "", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon = %d, want 401", rec.Code)
	}
	rec := ptUpload(env, token, good)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d body=%s", rec.Code, rec.Body.String())
	}
	var sum peertubearchive.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.ChannelsCreated != 1 || sum.PlaylistsCreated != 1 || sum.PlaylistItemsSkipped != 1 || sum.SkippedSections["videos"] != 2 {
		t.Fatalf("summary: %+v", sum)
	}

	for name, raw := range map[string][]byte{
		"missing field": nil,
		"not a zip":     []byte("hello"),
		"zip slip":      ptZip(t, map[string]string{"peertube/channels.json": `{"channels":[]}`, "../../evil": "x"}),
	} {
		if rec := ptUpload(env, token, raw); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, rec.Code)
		}
	}
	if len(chans.made) != 1 {
		t.Fatalf("a refused archive created channels: %v", chans.made)
	}

	env.setSetting(t, instancesettings.KeyUserImportEnabled, "false")
	if rec := ptUpload(env, token, good); rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Errorf("disabled = %d code=%q, want 403 feature_disabled", rec.Code, errorCode(t, rec))
	}
}
