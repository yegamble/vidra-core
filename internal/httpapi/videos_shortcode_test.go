package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/video"
)

func getByCode(srv *Server, code, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/by-code/"+code, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func getByLegacyUUID(srv *Server, id, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/by-legacy-uuid/"+id, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// shortCodeOf reads the code off the detail view, which is where the frontend
// gets it too.
func shortCodeOf(t *testing.T, srv *Server, id, token string) string {
	t.Helper()
	rec := getVideo(srv, id, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get video for code = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.ShortCode == "" {
		t.Fatal("detail view carries no short_code")
	}
	return v.ShortCode
}

// A created video gets a code that satisfies the same contract migration 0126
// enforces in the database, without anyone asking for one.
func TestCreateVideoMintsAValidShortCode(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"Clip","privacy":"public"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !video.ValidShortCode(v.ShortCode) {
		t.Fatalf("create response short_code = %q, which is not a valid code", v.ShortCode)
	}
}

// Naming a video by its code must return exactly what naming it by its uuid
// returns — same body, byte for byte. This is the property that makes the two
// routes impossible to drift apart.
func TestGetVideoByShortCodeMatchesGetByID(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Public","privacy":"public"}`)
	code := shortCodeOf(t, srv, id, tok)

	byID := getVideo(srv, id, "")
	byCode := getByCode(srv, code, "")
	if byCode.Code != http.StatusOK {
		t.Fatalf("anon get by code = %d, want 200; body=%s", byCode.Code, byCode.Body.String())
	}
	if byCode.Body.String() != byID.Body.String() {
		t.Fatalf("by-code body differs from by-id\n by-code: %s\n by-id:   %s", byCode.Body.String(), byID.Body.String())
	}
}

// A malformed code must be 404, never 400: a distinguishable "bad shape" reply
// would let a caller tell a well-formed unknown code from a badly-formed one,
// and for an unlisted video the code is the only thing protecting it.
func TestGetVideoByShortCodeMalformedIs404NeverBadRequest(t *testing.T) {
	srv := videoServer(t)
	bad := []struct{ name, code string }{
		{"too short", "abc"},
		{"too long", "abcdefghijkl"},
		{"ten chars", "abcdefghij"},
		{"contains 0", "abcdefghij0"},
		{"contains O", "abcdefghijO"},
		{"contains I", "abcdefghijI"},
		{"contains l", "abcdefghijl"},
		{"punctuation", "abcdefghi-j"},
		{"a uuid", uuid.New().String()},
		{"non-ascii", "abcdefghijé"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			rec := getByCode(srv, tc.code, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("code %q = %d, want 404", tc.code, rec.Code)
			}
		})
	}
	// Well-formed but unknown is the SAME answer as malformed.
	if rec := getByCode(srv, "abcdefghijk", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown well-formed code = %d, want 404", rec.Code)
	}
}

// The code is another way to NAME a video, never a way to widen access to it.
func TestGetVideoByShortCodePreservesPrivacy(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret","privacy":"private"}`)
	code := shortCodeOf(t, srv, id, ownerTok)

	if rec := getByCode(srv, code, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner by code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getByCode(srv, code, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("anon by code = %d, want 404", rec.Code)
	}
	if rec := getByCode(srv, code, otherTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner by code = %d, want 404", rec.Code)
	}
}

// An imported PeerTube video's OLD links decode to the SOURCE uuid, which is
// not this video's id. Resolving it is the whole point of videos.peertube_uuid.
func TestGetVideoByLegacyUUIDResolvesImportedPeerTubeVideo(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	sourceUUID := uuid.New()
	if repo.peertubeUUIDs == nil {
		repo.peertubeUUIDs = map[uuid.UUID]uuid.UUID{}
	}
	repo.peertubeUUIDs[uuid.MustParse(id)] = sourceUUID

	rec := getByLegacyUUID(srv, sourceUUID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("by legacy peertube uuid = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.ID != id {
		t.Fatalf("resolved to %s, want %s", v.ID, id)
	}
}

// /videos/watch/{uuid} also carries THIS instance's own ids, from the AP `url`
// form remote servers still hold.
func TestGetVideoByLegacyUUIDResolvesOwnID(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Public","privacy":"public"}`)

	if rec := getByLegacyUUID(srv, id, ""); rec.Code != http.StatusOK {
		t.Fatalf("by legacy own id = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetVideoByLegacyUUIDUnknownAndMalformedAre404(t *testing.T) {
	srv := videoServer(t)
	if rec := getByLegacyUUID(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown legacy uuid = %d, want 404", rec.Code)
	}
	if rec := getByLegacyUUID(srv, "not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("malformed legacy uuid = %d, want 404", rec.Code)
	}
	if rec := getByLegacyUUID(srv, uuid.Nil.String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("nil legacy uuid = %d, want 404", rec.Code)
	}
}

// oEmbed must resolve the NEW canonical short form. This is the branch whose
// absence fails silently and asymmetrically: the link still works in a browser
// (the frontend redirects it itself) while every CMS and chat unfurl of the
// same link renders a dead card.
func TestOEmbedResolvesStoredShortCode(t *testing.T) {
	srv, repo, ada := distributionServer(t)
	id := seedVideo(repo, ada, "Clip", "d", "public", "published", 5)
	code := repo.videos[id].ShortCode

	base := "https://videos.example"
	cases := []struct {
		name string
		url  string
		want int
	}{
		{"stored short code resolves", base + "/v/" + code, http.StatusOK},
		{"unknown stored code is 404", base + "/v/abcdefghijk", http.StatusNotFound},
		{"foreign host is 404", "https://evil.example/v/" + code, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, "/services/oembed?format=json&url="+url.QueryEscape(tc.url))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// The short form and the canonical form must unfurl identically — the
	// existing derived-sid tests assert the same property, and it is the reason
	// oEmbed parses short URLs at all.
	byCode := get(t, srv, "/services/oembed?format=json&url="+url.QueryEscape(base+"/v/"+code))
	byID := get(t, srv, "/services/oembed?format=json&url="+url.QueryEscape(base+"/videos/"+id.String()))
	if byCode.Body.String() != byID.Body.String() {
		t.Fatalf("short-code unfurl differs from canonical\n code: %s\n uuid: %s", byCode.Body.String(), byID.Body.String())
	}
}
