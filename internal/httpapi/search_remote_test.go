package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Remote-URI search handler tests (config-parity W13): the auth-state gate
// matrix, per-caller rate limiting of resolution, silent degradation, and the
// /instance search block.

const (
	rsOrigin   = "https://tube.remote.example"
	rsVideoURL = rsOrigin + "/videos/abc"
	rsOwnerURL = rsOrigin + "/accounts/marta"
	rsGroupURL = rsOrigin + "/video-channels/movies"
)

// rsTransport serves canned ActivityPub JSON by exact URL and counts calls.
type rsTransport struct {
	docs  map[string]string
	calls *int
}

func (rt rsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.calls != nil {
		*rt.calls++
	}
	body, ok := rt.docs[req.URL.String()]
	status := http.StatusOK
	if !ok {
		status, body = http.StatusNotFound, "{}"
	}
	h := http.Header{}
	h.Set("Content-Type", "application/activity+json")
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h, Request: req}, nil
}

func rsDocs() map[string]string {
	return map[string]string{
		rsVideoURL: `{
			"id": "` + rsVideoURL + `", "type": "Video", "name": "Remote premiere",
			"content": "from afar", "attributedTo": "` + rsOwnerURL + `",
			"duration": "PT62S",
			"url": [
				{"mediaType": "text/html", "href": "` + rsOrigin + `/w/abc"},
				{"mediaType": "application/x-mpegURL", "href": "` + rsOrigin + `/hls/abc.m3u8"}
			]
		}`,
		rsOwnerURL: `{
			"id": "` + rsOwnerURL + `", "type": "Person", "preferredUsername": "marta",
			"inbox": "` + rsOrigin + `/accounts/marta/inbox", "publicKey": {"publicKeyPem": "PEM"}
		}`,
		rsGroupURL: `{
			"id": "` + rsGroupURL + `", "type": "Group", "preferredUsername": "movies",
			"inbox": "` + rsOrigin + `/video-channels/movies/inbox", "publicKey": {"publicKeyPem": "PEM"}
		}`,
		"https://tube.remote.example/.well-known/webfinger?resource=" + url.QueryEscape("acct:movies@tube.remote.example"): `{
			"links": [{"rel": "self", "type": "application/activity+json", "href": "` + rsGroupURL + `"}]
		}`,
	}
}

// remoteSearchFedRepo backs both the federation service (resolution + storage)
// and the remotevideo read service in these tests: stored remote videos are
// readable by id, and cached actors keep their full identity (the shared
// fakeFedRepo's upsert drops it).
type remoteSearchFedRepo struct {
	fakeFedRepo
	videosByID map[uuid.UUID]sqlcgen.GetRemoteVideoByIDRow
}

func newRemoteSearchFedRepo() *remoteSearchFedRepo {
	return &remoteSearchFedRepo{
		fakeFedRepo: fakeFedRepo{
			acctKeys:     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
			chanKeys:     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
			remoteActors: map[string]sqlcgen.RemoteActor{},
			processed:    map[string]bool{},
			deliveries:   map[string]sqlcgen.EnqueueDeliveryParams{},
		},
		videosByID: map[uuid.UUID]sqlcgen.GetRemoteVideoByIDRow{},
	}
}

func (f *remoteSearchFedRepo) UpsertRemoteActor(_ context.Context, arg sqlcgen.UpsertRemoteActorParams) error {
	f.remoteActors[arg.ActorUrl] = sqlcgen.RemoteActor{
		ActorUrl:          arg.ActorUrl,
		ActorType:         arg.ActorType,
		PreferredUsername: arg.PreferredUsername,
		Domain:            arg.Domain,
		InboxUrl:          arg.InboxUrl,
		SharedInboxUrl:    arg.SharedInboxUrl,
		PublicKeyPem:      arg.PublicKeyPem,
		FollowersUrl:      arg.FollowersUrl,
	}
	return nil
}

func (f *remoteSearchFedRepo) UpsertRemoteVideo(_ context.Context, arg sqlcgen.UpsertRemoteVideoParams) (sqlcgen.UpsertRemoteVideoRow, error) {
	for id, row := range f.videosByID {
		if row.ObjectUrl == arg.ObjectUrl {
			return sqlcgen.UpsertRemoteVideoRow{ID: id, ThumbnailKey: row.ThumbnailKey}, nil
		}
	}
	id := uuid.New()
	domain := ""
	if u, err := url.Parse(arg.RemoteActorUrl); err == nil {
		domain = u.Host
	}
	f.videosByID[id] = sqlcgen.GetRemoteVideoByIDRow{
		ID: id, ObjectUrl: arg.ObjectUrl, RemoteActorUrl: arg.RemoteActorUrl,
		Domain: domain, Title: arg.Title, Description: arg.Description,
		DurationSeconds: arg.DurationSeconds, WatchUrl: arg.WatchUrl, StreamUrl: arg.StreamUrl,
	}
	return sqlcgen.UpsertRemoteVideoRow{ID: id}, nil
}

// GetRemoteVideoByID satisfies remotevideo.Repository over the same rows the
// federation side stores.
func (f *remoteSearchFedRepo) GetRemoteVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetRemoteVideoByIDRow, error) {
	if row, ok := f.videosByID[id]; ok {
		return row, nil
	}
	// Mirror the SQL's not-found: pgx.ErrNoRows via the shared fake's stub.
	return remoteVideoFakeRepo{}.GetRemoteVideoByID(ctx, id)
}

// remoteSearchServer builds the full video test server with federation +
// remote-video services wired for remote-URI search, returning the server and
// the transport call counter. Extra options (e.g. a tight rate limiter) ride
// httpOpts.
func remoteSearchServer(t *testing.T, cfg *config.Config, calls *int, httpOpts ...Option) *Server {
	t.Helper()
	repo := newRemoteSearchFedRepo()
	fedsvc := federation.NewService(repo,
		federation.WithBaseURL("https://videos.example"),
		federation.WithFetchClient(&http.Client{Transport: rsTransport{docs: rsDocs(), calls: calls}}),
	)
	opts := append([]Option{
		WithFederationService(fedsvc),
		WithRemoteVideoService(remotevideo.NewService(repo, nil)),
	}, httpOpts...)
	srv, _, _, _, _ := videoServerFullWith(t, cfg, opts)
	return srv
}

// searchBody GETs /videos/search and decodes the response.
func searchBody(t *testing.T, srv *Server, query, token string) videoSearchResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?"+query, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body videoSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

// patchSearchGates flips the two W13 settings through the admin API.
func patchSearchGates(t *testing.T, srv *Server, adminTok string, users, anonymous bool) {
	t.Helper()
	body := `{"search_remote_uri_users":` + boolLit(users) + `,"search_remote_uri_anonymous":` + boolLit(anonymous) + `}`
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch gates = %d; body=%s", rec.Code, rec.Body.String())
	}
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestRemoteURISearchGateMatrix(t *testing.T) {
	cfg := fedTestConfig()
	srv := remoteSearchServer(t, cfg, nil)
	// The first registered user is the admin in this harness.
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	userTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	q := "q=" + url.QueryEscape(rsVideoURL)

	cases := []struct {
		name         string
		users, anon  bool
		token        string
		wantResolved bool
	}{
		{"defaults: authed resolves", true, false, userTok, true},
		{"defaults: anonymous does not", true, false, "", false},
		{"anon gate on: anonymous resolves", true, true, "", true},
		{"users gate off: authed does not", false, true, userTok, false},
		{"both off", false, false, userTok, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patchSearchGates(t, srv, adminTok, tc.users, tc.anon)
			body := searchBody(t, srv, q, tc.token)
			if got := len(body.Remote) > 0; got != tc.wantResolved {
				t.Fatalf("remote resolved = %v, want %v (remote=%+v)", got, tc.wantResolved, body.Remote)
			}
			if tc.wantResolved {
				hit := body.Remote[0]
				if hit.Type != "video" || hit.Video == nil {
					t.Fatalf("hit = %+v, want a typed video item", hit)
				}
				if !hit.Video.Remote || hit.Video.Domain != "tube.remote.example" || hit.Video.Title != "Remote premiere" {
					t.Errorf("video = %+v", hit.Video)
				}
				if hit.Video.StreamURL == nil || *hit.Video.StreamURL != rsOrigin+"/hls/abc.m3u8" {
					t.Errorf("stream_url = %v", hit.Video.StreamURL)
				}
			}
		})
	}
}

func TestRemoteURISearchHandleResolvesChannel(t *testing.T) {
	srv := remoteSearchServer(t, fedTestConfig(), nil)
	userTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	body := searchBody(t, srv, "q="+url.QueryEscape("@movies@tube.remote.example"), userTok)
	if len(body.Remote) != 1 {
		t.Fatalf("remote = %+v, want one channel hit", body.Remote)
	}
	hit := body.Remote[0]
	if hit.Type != "channel" || hit.Actor == nil || hit.Video != nil {
		t.Fatalf("hit = %+v, want a typed channel item", hit)
	}
	if hit.Actor.Handle != "movies@tube.remote.example" || hit.Actor.Domain != "tube.remote.example" || hit.Actor.ActorURL != rsGroupURL {
		t.Errorf("actor = %+v", hit.Actor)
	}
}

func TestRemoteURISearchSkipsWhenNotApplicable(t *testing.T) {
	calls := 0
	srv := remoteSearchServer(t, fedTestConfig(), &calls)
	userTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Plain-text queries never trigger resolution (no outbound fetch at all).
	if body := searchBody(t, srv, "q=premiere", userTok); len(body.Remote) != 0 {
		t.Errorf("plain text resolved remotely: %+v", body.Remote)
	}
	if calls != 0 {
		t.Fatalf("plain-text search made %d outbound fetches, want 0", calls)
	}

	// Later pages never re-resolve — the remote hit rode page one.
	if body := searchBody(t, srv, "q="+url.QueryEscape(rsVideoURL)+"&offset=20", userTok); len(body.Remote) != 0 {
		t.Errorf("offset>0 resolved remotely: %+v", body.Remote)
	}
	if calls != 0 {
		t.Fatalf("offset>0 search made %d outbound fetches, want 0", calls)
	}

	// An unresolvable target degrades silently: 200 with local-only results.
	if body := searchBody(t, srv, "q="+url.QueryEscape(rsOrigin+"/videos/nope"), userTok); len(body.Remote) != 0 {
		t.Errorf("unresolvable target produced remote hits: %+v", body.Remote)
	}
}

func TestRemoteURISearchDisabledWithoutFederation(t *testing.T) {
	// Federation off: gates report false and no resolution ever runs, even
	// with both settings on.
	cfg := fedTestConfig()
	cfg.FederationEnabled = false
	calls := 0
	srv := remoteSearchServer(t, cfg, &calls)
	// The first registered user is the admin in this harness.
	adminTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	patchSearchGates(t, srv, adminTok, true, true)
	userTok := adminTok

	if body := searchBody(t, srv, "q="+url.QueryEscape(rsVideoURL), userTok); len(body.Remote) != 0 {
		t.Errorf("federation-off search resolved remotely: %+v", body.Remote)
	}
	if calls != 0 {
		t.Fatalf("federation-off search made %d outbound fetches, want 0", calls)
	}

	var inst instanceResponse
	rec := getPath(srv, "/api/v1/instance")
	_ = json.Unmarshal(rec.Body.Bytes(), &inst)
	if inst.Search.RemoteURIUsers || inst.Search.RemoteURIAnonymous {
		t.Errorf("instance search block = %+v, want both false while federation is off", inst.Search)
	}
}

func TestRemoteURISearchInstanceBlock(t *testing.T) {
	srv := remoteSearchServer(t, fedTestConfig(), nil)
	var inst instanceResponse
	rec := getPath(srv, "/api/v1/instance")
	_ = json.Unmarshal(rec.Body.Bytes(), &inst)
	if !inst.Search.RemoteURIUsers || inst.Search.RemoteURIAnonymous {
		t.Errorf("instance search block = %+v, want users=true anonymous=false by default", inst.Search)
	}
}

func TestRemoteURISearchRateLimited(t *testing.T) {
	calls := 0
	limiter := ratelimit.NewLimiter(ratelimit.NewMemoryCounter(), 1, time.Minute)
	srv := remoteSearchServer(t, fedTestConfig(), &calls, WithSearchResolveRateLimiter(limiter))
	userTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	q := "q=" + url.QueryEscape(rsVideoURL)
	if body := searchBody(t, srv, q, userTok); len(body.Remote) != 1 {
		t.Fatalf("first search remote = %+v, want the resolved hit", body.Remote)
	}
	fetchesAfterFirst := calls
	// The second shaped search inside the window exhausts the caller's budget:
	// still 200, local-only, and NO further outbound fetch.
	if body := searchBody(t, srv, q, userTok); len(body.Remote) != 0 {
		t.Fatalf("rate-limited search still resolved: %+v", body.Remote)
	}
	if calls != fetchesAfterFirst {
		t.Errorf("rate-limited search made %d extra outbound fetches, want 0", calls-fetchesAfterFirst)
	}
}
