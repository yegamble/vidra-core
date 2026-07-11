package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- in-memory fakes for the W1 stores ---

type instanceDocsFakeRepo struct {
	rows map[string]sqlcgen.InstanceDocument
}

func newInstanceDocsFakeRepo() *instanceDocsFakeRepo {
	return &instanceDocsFakeRepo{rows: map[string]sqlcgen.InstanceDocument{}}
}

func (f *instanceDocsFakeRepo) ListInstanceDocuments(context.Context) ([]sqlcgen.InstanceDocument, error) {
	out := make([]sqlcgen.InstanceDocument, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *instanceDocsFakeRepo) UpsertInstanceDocument(_ context.Context, arg sqlcgen.UpsertInstanceDocumentParams) (sqlcgen.InstanceDocument, error) {
	row := sqlcgen.InstanceDocument{Name: arg.Name, Body: arg.Body, Sha256: arg.Sha256,
		UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now()}
	f.rows[arg.Name] = row
	return row, nil
}

func (f *instanceDocsFakeRepo) DeleteInstanceDocument(_ context.Context, name string) (int64, error) {
	if _, ok := f.rows[name]; !ok {
		return 0, nil
	}
	delete(f.rows, name)
	return 1, nil
}

type instanceImageFakeRepo struct {
	rows map[string]sqlcgen.InstanceImage
}

func newInstanceImageFakeRepo() *instanceImageFakeRepo {
	return &instanceImageFakeRepo{rows: map[string]sqlcgen.InstanceImage{}}
}

func (f *instanceImageFakeRepo) ListInstanceImages(context.Context) ([]sqlcgen.InstanceImage, error) {
	out := make([]sqlcgen.InstanceImage, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

func (f *instanceImageFakeRepo) UpsertInstanceImage(_ context.Context, a sqlcgen.UpsertInstanceImageParams) (sqlcgen.InstanceImage, error) {
	row := sqlcgen.InstanceImage{Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.rows[a.Kind] = row
	return row, nil
}

func (f *instanceImageFakeRepo) GetInstanceImage(_ context.Context, kind string) (sqlcgen.InstanceImage, error) {
	row, ok := f.rows[kind]
	if !ok {
		return sqlcgen.InstanceImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *instanceImageFakeRepo) DeleteInstanceImage(_ context.Context, kind string) (int64, error) {
	if _, ok := f.rows[kind]; !ok {
		return 0, nil
	}
	delete(f.rows, kind)
	return 1, nil
}

// instanceConfigServer wires auth + the settings overlay + the W1 stores
// (instance documents, instance branding images) over in-memory fakes with a
// real local blob backend. The FIRST registered account is the admin.
func instanceConfigServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	settingssvc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := settingssvc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	docsvc := instancedocs.NewService(newInstanceDocsFakeRepo())
	if err := docsvc.Load(context.Background()); err != nil {
		t.Fatalf("docs load: %v", err)
	}
	imagesvc := profileimage.NewService(newImageFakeRepo(), blobs,
		profileimage.WithInstanceImages(newInstanceImageFakeRepo()))
	if err := imagesvc.LoadInstanceImages(context.Background()); err != nil {
		t.Fatalf("instance images load: %v", err)
	}
	return New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithSettingsService(settingssvc),
		WithInstanceDocumentsService(docsvc),
		WithProfileImageService(imagesvc),
		WithMediaStorage(blobs),
	)
}

func getInstanceRec(srv *Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil))
	return rec
}

// TestInstanceConfigBlocksDefaults proves the six additive blocks are present
// with their contract fallback content on a fresh instance, and that the
// response carries the W1 caching headers (ETag + s-maxage) with a working
// If-None-Match short-circuit.
func TestInstanceConfigBlocksDefaults(t *testing.T) {
	srv := instanceConfigServer(t)
	rec := getInstanceRec(srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /instance = %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "s-maxage=60" {
		t.Errorf("Cache-Control = %q, want s-maxage=60", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on GET /instance")
	}

	var body instanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// branding: everything unset -> "" URLs + is_fallback.
	for name, ref := range map[string]instanceAssetRef{
		"avatar": body.Branding.Avatar, "banner": body.Branding.Banner,
		"favicon": body.Branding.Logos.Favicon, "header_wide": body.Branding.Logos.HeaderWide,
		"header_square": body.Branding.Logos.HeaderSquare, "opengraph": body.Branding.Logos.Opengraph,
	} {
		if ref.URL != "" || !ref.IsFallback {
			t.Errorf("branding.%s = %+v, want empty fallback", name, ref)
		}
	}
	if body.Branding.HideInstanceName {
		t.Error("branding.hide_instance_name = true, want false")
	}
	// defaults (contract fallback values).
	d := body.Defaults
	if d.FeedSort != "recent" || d.FeedScope != "local" || d.LandingPage != "home-recent" ||
		d.Theme != "system" || !d.PlayerAutoplay || d.MiniaturePreferAuthorDisplayName {
		t.Errorf("defaults = %+v", d)
	}
	if d.Publish.Privacy != "public" || d.Publish.Licence != 0 ||
		d.Publish.CommentPolicy != "enabled" || d.Publish.DownloadEnabled {
		t.Errorf("defaults.publish = %+v", d.Publish)
	}
	// broadcast / customization / social / homepage zero values.
	if body.Broadcast.Enabled || body.Broadcast.Message != "" || body.Broadcast.Level != "info" || body.Broadcast.Dismissable {
		t.Errorf("broadcast = %+v", body.Broadcast)
	}
	if body.Customization.CSSHash != "" || body.Customization.JSHash != "" || body.Customization.PrimaryColor != "" {
		t.Errorf("customization = %+v", body.Customization)
	}
	if body.Social.TwitterUsername != "" {
		t.Errorf("social = %+v", body.Social)
	}
	if body.Homepage.Enabled || body.Homepage.Hash != "" {
		t.Errorf("homepage = %+v", body.Homepage)
	}

	// Conditional revalidation: matching If-None-Match -> 304, no body.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified || rec2.Body.Len() != 0 {
		t.Errorf("conditional GET = %d body=%d bytes, want 304 empty", rec2.Code, rec2.Body.Len())
	}
}

// TestInstanceConfigBlocksReflectOverrides proves the blocks track the
// settings overlay at runtime and the ETag changes with the content.
func TestInstanceConfigBlocksReflectOverrides(t *testing.T) {
	srv := instanceConfigServer(t)
	adminTok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	before := getInstanceRec(srv)
	etagBefore := before.Header().Get("ETag")

	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", `{
		"broadcast_enabled": true,
		"broadcast_message": "**Maintenance** tonight",
		"broadcast_level": "warning",
		"broadcast_dismissable": true,
		"default_feed_sort": "trending",
		"default_theme": "dark",
		"default_player_autoplay": false,
		"default_video_privacy": "unlisted",
		"default_video_licence": 2,
		"theme_primary_color": "#0f62fe",
		"social_meta_twitter_username": "@vidra",
		"header_hide_instance_name": true
	}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", rec.Code, rec.Body.String())
	}

	after := getInstanceRec(srv)
	var body instanceResponse
	_ = json.Unmarshal(after.Body.Bytes(), &body)
	if !body.Broadcast.Enabled || body.Broadcast.Message != "**Maintenance** tonight" ||
		body.Broadcast.Level != "warning" || !body.Broadcast.Dismissable {
		t.Errorf("broadcast after patch = %+v", body.Broadcast)
	}
	if body.Defaults.FeedSort != "trending" || body.Defaults.Theme != "dark" || body.Defaults.PlayerAutoplay {
		t.Errorf("defaults after patch = %+v", body.Defaults)
	}
	if body.Defaults.Publish.Privacy != "unlisted" || body.Defaults.Publish.Licence != 2 {
		t.Errorf("publish defaults after patch = %+v", body.Defaults.Publish)
	}
	if body.Customization.PrimaryColor != "#0f62fe" {
		t.Errorf("customization.primary_color = %q", body.Customization.PrimaryColor)
	}
	if body.Social.TwitterUsername != "@vidra" {
		t.Errorf("social.twitter_username = %q", body.Social.TwitterUsername)
	}
	if !body.Branding.HideInstanceName {
		t.Error("branding.hide_instance_name = false after enabling")
	}
	if etagAfter := after.Header().Get("ETag"); etagAfter == etagBefore {
		t.Error("ETag unchanged after settings change")
	}
}

// TestInstanceDocumentsAdminFlow covers the admin editor round-trip, the
// public delivery routes with content-type discipline, the size caps, and the
// clear-on-empty semantics — plus the hashes surfacing on GET /instance.
func TestInstanceDocumentsAdminFlow(t *testing.T) {
	srv := instanceConfigServer(t)
	var logBuf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&logBuf, nil))
	adminTok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, _ := registerUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Guards: anon 401, non-admin 403, unknown name 404.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-documents/homepage", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon GET = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/homepage", `{"body":"x"}`, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin PUT = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-documents/nope", "", adminTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown name GET = %d, want 404", rec.Code)
	}

	// Never-set: stable empty admin shape; public routes 404.
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/instance-documents/homepage", "", adminTok)
	var doc instanceDocumentView
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if rec.Code != http.StatusOK || doc.Name != "homepage" || doc.Body != "" || doc.Hash != "" {
		t.Errorf("unset admin GET = %d %+v, want empty homepage doc", rec.Code, doc)
	}
	for _, path := range []string{"/api/v1/instance/homepage", "/api/v1/instance/custom.css", "/api/v1/instance/custom.js"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s while unset = %d, want 404", path, rec.Code)
		}
	}

	// PUT homepage -> public JSON delivery + homepage block flips on.
	rec = sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/homepage", `{"body":"# Welcome"}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT homepage = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.Body != "# Welcome" || len(doc.Hash) != 64 {
		t.Errorf("PUT homepage view = %+v, want body + sha256 hash", doc)
	}
	pub := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/api/v1/instance/homepage", nil))
	var hp instanceHomepageDocResponse
	_ = json.Unmarshal(pub.Body.Bytes(), &hp)
	if pub.Code != http.StatusOK || hp.Body != "# Welcome" || hp.Hash != doc.Hash {
		t.Errorf("public homepage = %d %+v", pub.Code, hp)
	}

	// The write is audit-enveloped: name + content hash, never the body.
	audits := logBuf.String()
	if !strings.Contains(audits, observability.ActionAdminInstanceDocumentUpdate) ||
		!strings.Contains(audits, "name=homepage,sha256="+doc.Hash) {
		t.Error("document write missing admin.instance_document.update audit with name+hash")
	}
	if strings.Contains(audits, "# Welcome") {
		t.Error("audit log leaked the document body")
	}

	// PUT css/js -> served with strict content types + ETag; hashes on /instance.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/custom_css", `{"body":"body{color:red}"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("PUT css = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/custom_js", `{"body":"console.log(1)"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("PUT js = %d; body=%s", rec.Code, rec.Body.String())
	}
	css := httptest.NewRecorder()
	srv.Handler().ServeHTTP(css, httptest.NewRequest(http.MethodGet, "/api/v1/instance/custom.css", nil))
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") ||
		css.Body.String() != "body{color:red}" {
		t.Errorf("custom.css = %d type=%q body=%q", css.Code, css.Header().Get("Content-Type"), css.Body.String())
	}
	if css.Header().Get("ETag") == "" {
		t.Error("custom.css missing ETag")
	}
	js := httptest.NewRecorder()
	srv.Handler().ServeHTTP(js, httptest.NewRequest(http.MethodGet, "/api/v1/instance/custom.js", nil))
	if js.Code != http.StatusOK || !strings.HasPrefix(js.Header().Get("Content-Type"), "application/javascript") ||
		js.Body.String() != "console.log(1)" {
		t.Errorf("custom.js = %d type=%q body=%q", js.Code, js.Header().Get("Content-Type"), js.Body.String())
	}

	var inst instanceResponse
	_ = json.Unmarshal(getInstanceRec(srv).Body.Bytes(), &inst)
	if !inst.Homepage.Enabled || inst.Homepage.Hash != doc.Hash {
		t.Errorf("homepage block = %+v, want enabled + hash %s", inst.Homepage, doc.Hash)
	}
	if len(inst.Customization.CSSHash) != 64 || len(inst.Customization.JSHash) != 64 {
		t.Errorf("customization hashes = %+v, want sha256 hex", inst.Customization)
	}

	// Size cap: homepage over 100KB -> 422.
	over := `{"body":"` + strings.Repeat("a", (100<<10)+1) + `"}`
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/homepage", over, adminTok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("over-cap PUT = %d, want 422", rec.Code)
	}
	// Malformed body (missing "body" field) -> 400.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/homepage", `{}`, adminTok); rec.Code != http.StatusBadRequest {
		t.Errorf("missing body field PUT = %d, want 400", rec.Code)
	}

	// Clear with an empty body: public 404s, homepage block flips off.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/admin/instance-documents/homepage", `{"body":""}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("clear PUT = %d", rec.Code)
	}
	pub = httptest.NewRecorder()
	srv.Handler().ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/api/v1/instance/homepage", nil))
	if pub.Code != http.StatusNotFound {
		t.Errorf("public homepage after clear = %d, want 404", pub.Code)
	}
	_ = json.Unmarshal(getInstanceRec(srv).Body.Bytes(), &inst)
	if inst.Homepage.Enabled || inst.Homepage.Hash != "" {
		t.Errorf("homepage block after clear = %+v", inst.Homepage)
	}
}

// TestInstanceAssetsAdminFlow covers the instance branding endpoints: guards,
// upload/serve/delete for the avatar and a typed logo slot, extension
// validation, and the branding block reflecting state.
func TestInstanceAssetsAdminFlow(t *testing.T) {
	srv := instanceConfigServer(t)
	adminTok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, _ := registerUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Guards.
	if rec := uploadImage(srv, "/api/v1/admin/instance-avatar", "a.png", "png-bytes", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon upload = %d, want 401", rec.Code)
	}
	if rec := uploadImage(srv, "/api/v1/admin/instance-avatar", "a.png", "png-bytes", bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin upload = %d, want 403", rec.Code)
	}
	// Type gate + unknown logo slot.
	if rec := uploadImage(srv, "/api/v1/admin/instance-avatar", "a.gif", "gif", adminTok); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("gif upload = %d, want 415", rec.Code)
	}
	if rec := uploadImage(srv, "/api/v1/admin/instance-logo/poster", "a.png", "png", adminTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown logo type upload = %d, want 404", rec.Code)
	}

	// Upload avatar + opengraph logo.
	if rec := uploadImage(srv, "/api/v1/admin/instance-avatar", "a.png", "avatar-bytes", adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("avatar upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadImage(srv, "/api/v1/admin/instance-logo/opengraph", "og.jpg", "og-bytes", adminTok); rec.Code != http.StatusCreated {
		t.Fatalf("logo upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Branding block reflects the uploads.
	var inst instanceResponse
	_ = json.Unmarshal(getInstanceRec(srv).Body.Bytes(), &inst)
	if inst.Branding.Avatar.IsFallback || inst.Branding.Avatar.URL != "/api/v1/instance/avatar" {
		t.Errorf("branding.avatar = %+v", inst.Branding.Avatar)
	}
	if inst.Branding.Logos.Opengraph.IsFallback || inst.Branding.Logos.Opengraph.URL != "/api/v1/instance/logo/opengraph" {
		t.Errorf("branding.logos.opengraph = %+v", inst.Branding.Logos.Opengraph)
	}
	if !inst.Branding.Banner.IsFallback || !inst.Branding.Logos.Favicon.IsFallback {
		t.Errorf("unset slots not fallback: banner=%+v favicon=%+v", inst.Branding.Banner, inst.Branding.Logos.Favicon)
	}

	// Public serving with the derived content type.
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/api/v1/instance/avatar"); rec.Code != http.StatusOK ||
		rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != "avatar-bytes" {
		t.Errorf("GET avatar = %d type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if rec := get("/api/v1/instance/logo/opengraph"); rec.Code != http.StatusOK ||
		rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("GET logo = %d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := get("/api/v1/instance/banner"); rec.Code != http.StatusNotFound {
		t.Errorf("GET unset banner = %d, want 404", rec.Code)
	}
	if rec := get("/api/v1/instance/logo/poster"); rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown logo type = %d, want 404", rec.Code)
	}

	// Delete -> 204, then 404, and the block falls back.
	del := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/instance-avatar", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	srv.Handler().ServeHTTP(del, req)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete avatar = %d; body=%s", del.Code, del.Body.String())
	}
	del2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/instance-avatar", nil)
	req2.Header.Set("Authorization", "Bearer "+adminTok)
	srv.Handler().ServeHTTP(del2, req2)
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", del2.Code)
	}
	_ = json.Unmarshal(getInstanceRec(srv).Body.Bytes(), &inst)
	if !inst.Branding.Avatar.IsFallback || inst.Branding.Avatar.URL != "" {
		t.Errorf("branding.avatar after delete = %+v, want fallback", inst.Branding.Avatar)
	}
}
