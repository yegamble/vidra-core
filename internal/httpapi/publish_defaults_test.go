package httpapi

// Publish defaults & per-video policies (config-parity W9,
// .ralph/specs/config-parity/waves.md).
//
// Covered here:
//   - create seeding: omitted privacy/licence/comments_policy/download_enabled
//     follow the defaults.publish settings (admin-overridable, no restart);
//     explicit client values always win.
//   - per-video comment gate: comments_policy=disabled answers the same 403
//     feature_disabled shape as the instance toggle; reading stays open.
//   - per-video download gate LAYERED on the instance downloads_enabled gate
//     (84b5a38): the full off/on × off/on matrix, every download route, and
//     the moderator bypass.
//   - payload fields: detail carries comments_policy, effective
//     comments_enabled, download_enabled, and author_display_name; feed and
//     search cards carry author_display_name.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// patchVideo PATCHes a video's metadata and decodes the updated view.
func patchVideo(t *testing.T, srv *Server, tok, id, body string) videoView {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, body, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH video %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v
}

// TestCreateVideoSeedsOverriddenPublishDefaults: an admin override of every
// defaults.publish key seeds omitted fields on the next create — and explicit
// client values still win over the overrides.
func TestCreateVideoSeedsOverriddenPublishDefaults(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada") // first account = admin
	patchSettings(t, srv, admin,
		`{"default_video_privacy":"unlisted","default_video_licence":3,"default_comment_policy":"disabled","default_download_enabled":false}`)

	// Omitted fields follow the overridden defaults.
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"Seeded"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var seeded videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &seeded)
	if seeded.Privacy != "unlisted" {
		t.Errorf("privacy = %q, want unlisted", seeded.Privacy)
	}
	if seeded.License == nil || *seeded.License != "3" {
		t.Errorf("license = %v, want 3", seeded.License)
	}
	if seeded.CommentsPolicy != "disabled" {
		t.Errorf("comments_policy = %q, want disabled", seeded.CommentsPolicy)
	}
	if seeded.DownloadEnabled == nil || *seeded.DownloadEnabled {
		t.Errorf("download_enabled = %v, want false", seeded.DownloadEnabled)
	}

	// Explicit client values win over every default.
	rec = postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"Explicit","privacy":"public","license":"1","comments_policy":"enabled","download_enabled":true}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("explicit create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var explicit videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &explicit)
	if explicit.Privacy != "public" || explicit.License == nil || *explicit.License != "1" ||
		explicit.CommentsPolicy != "enabled" || explicit.DownloadEnabled == nil || !*explicit.DownloadEnabled {
		t.Errorf("explicit video = %+v, want client values to win", explicit)
	}

	// Clearing the overrides (null-PATCH) restores the registry defaults.
	patchSettings(t, srv, admin,
		`{"default_video_privacy":null,"default_video_licence":null,"default_comment_policy":null,"default_download_enabled":null}`)
	rec = postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"Back to defaults"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create after clear = %d; body=%s", rec.Code, rec.Body.String())
	}
	var cleared videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if cleared.Privacy != "public" || cleared.License != nil ||
		cleared.CommentsPolicy != "enabled" || cleared.DownloadEnabled == nil || !*cleared.DownloadEnabled {
		t.Errorf("cleared-defaults video = %+v, want public/no licence/enabled/downloadable", cleared)
	}
}

// TestCreateVideoRejectsBadCommentsPolicy: the create/update enums refuse
// anything outside enabled|disabled (requires_approval is deliberately
// deferred — parity ledger §13).
func TestCreateVideoRejectsBadCommentsPolicy(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"x","comments_policy":"requires_approval"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create with bad policy = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	id := createVideo(t, srv, tok, "ada", `{"title":"ok"}`)
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"comments_policy":"maybe"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update with bad policy = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestUpdateVideoPublishPolicies: the owner can flip both per-video policies
// through PATCH /videos/{id}, and the update round-trips on the response and
// the detail view.
func TestUpdateVideoPublishPolicies(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	v := patchVideo(t, srv, tok, id, `{"comments_policy":"disabled","download_enabled":false}`)
	if v.CommentsPolicy != "disabled" || v.DownloadEnabled == nil || *v.DownloadEnabled {
		t.Errorf("updated view = comments %q / download %v, want disabled/false", v.CommentsPolicy, v.DownloadEnabled)
	}

	rec := getVideo(srv, id, tok)
	var detail videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.CommentsPolicy != "disabled" || detail.DownloadEnabled == nil || *detail.DownloadEnabled {
		t.Errorf("detail after update = comments %q / download %v, want disabled/false", detail.CommentsPolicy, detail.DownloadEnabled)
	}

	// A partial update leaves the other policy untouched.
	v = patchVideo(t, srv, tok, id, `{"download_enabled":true}`)
	if v.CommentsPolicy != "disabled" || v.DownloadEnabled == nil || !*v.DownloadEnabled {
		t.Errorf("partial update = comments %q / download %v, want disabled/true", v.CommentsPolicy, v.DownloadEnabled)
	}
}

// TestCommentCreatePerVideoPolicy: comments_policy=disabled gates POSTING with
// the instance gate's exact 403 feature_disabled shape, while reading existing
// comments stays open; flipping the policy back re-opens posting.
func TestCommentCreatePerVideoPolicy(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	// Baseline: comments post fine while the policy is enabled.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/comments", `{"body":"first"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("comment with policy enabled = %d; body=%s", rec.Code, rec.Body.String())
	}

	patchVideo(t, srv, tok, id, `{"comments_policy":"disabled"}`)
	rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/comments", `{"body":"blocked"}`, tok)
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
		t.Fatalf("comment with policy disabled = %d/%s, want 403/feature_disabled", rec.Code, errorCode(t, rec))
	}
	// Reading stays open (matching the instance-wide toggle's semantics).
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/comments", "", ""); rec.Code != http.StatusOK {
		t.Errorf("list comments with policy disabled = %d, want 200", rec.Code)
	}

	patchVideo(t, srv, tok, id, `{"comments_policy":"enabled"}`)
	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/comments", `{"body":"reopened"}`, tok); rec.Code != http.StatusCreated {
		t.Errorf("comment after re-enable = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// TestVideoDetailEffectiveCommentsEnabled: the detail view's comments_enabled
// is the EFFECTIVE value — instance toggle AND per-video policy.
func TestVideoDetailEffectiveCommentsEnabled(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	effective := func() bool {
		t.Helper()
		rec := getVideo(srv, id, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("detail = %d", rec.Code)
		}
		var v videoView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		if v.CommentsEnabled == nil {
			t.Fatalf("detail carries no comments_enabled: %s", rec.Body.String())
		}
		return *v.CommentsEnabled
	}

	if !effective() {
		t.Error("instance on + video enabled: comments_enabled = false, want true")
	}
	patchVideo(t, srv, admin, id, `{"comments_policy":"disabled"}`)
	if effective() {
		t.Error("instance on + video disabled: comments_enabled = true, want false")
	}
	patchVideo(t, srv, admin, id, `{"comments_policy":"enabled"}`)
	setToggle(t, srv, admin, "comments_enabled", false)
	if effective() {
		t.Error("instance off + video enabled: comments_enabled = true, want false")
	}
}

// TestDownloadPolicyLayeringMatrix drives the full instance × per-video gate
// matrix over the download surface: downloads are served only when BOTH gates
// are on; a moderator/admin bypasses both.
func TestDownloadPolicyLayeringMatrix(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	cases := []struct {
		name          string
		instance      bool
		video         bool
		wantAnonymous int
	}{
		{"instance on / video on", true, true, http.StatusOK},
		{"instance on / video off", true, false, http.StatusForbidden},
		{"instance off / video on", false, true, http.StatusForbidden},
		{"instance off / video off", false, false, http.StatusForbidden},
	}
	for _, tc := range cases {
		setToggle(t, srv, admin, "downloads_enabled", tc.instance)
		patchVideo(t, srv, admin, id, `{"download_enabled":`+boolJSON(tc.video)+`}`)

		if rec := getDownloads(srv, id, ""); rec.Code != tc.wantAnonymous {
			t.Errorf("%s: anonymous list = %d, want %d; body=%s", tc.name, rec.Code, tc.wantAnonymous, rec.Body.String())
		} else if tc.wantAnonymous == http.StatusForbidden && errorCode(t, rec) != "feature_disabled" {
			t.Errorf("%s: error code = %s, want feature_disabled", tc.name, errorCode(t, rec))
		}
		// The admin (privileged) bypasses BOTH gates for moderation.
		if rec := getDownloads(srv, id, admin); rec.Code != http.StatusOK {
			t.Errorf("%s: admin list = %d, want 200 (bypass)", tc.name, rec.Code)
		}
	}
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestDownloadPolicyCoversEveryRoute: with the instance gate ON and the
// per-video flag OFF, every official download route — the listing, the
// original file, the HLS-derived progressive MP4s, the audio-only asset, and
// subtitles — answers 403 feature_disabled for a regular viewer, and the
// per-video flag alone re-opens them all.
func TestDownloadPolicyCoversEveryRoute(t *testing.T) {
	srv, blobs, tcRepo, _, _ := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, tcRepo, blobs, id)
	if rec := uploadCaption(srv, id, "en", "English", sampleVTT, tok, true); rec.Code != http.StatusCreated {
		t.Fatalf("caption upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	routes := []string{
		"/api/v1/videos/" + id + "/download",
		"/api/v1/videos/" + id + "/download/original",
		"/api/v1/videos/" + id + "/download/hls/240",
		"/api/v1/videos/" + id + "/download/hls/240?audio=false",
		"/api/v1/videos/" + id + "/download/audio",
		"/api/v1/videos/" + id + "/download/subtitles/en",
	}

	patchVideo(t, srv, tok, id, `{"download_enabled":false}`)
	for _, route := range routes {
		rec := sendJSONAuth(srv, http.MethodGet, route, "", "")
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
			t.Errorf("video-off %s = %d/%s, want 403/feature_disabled", route, rec.Code, errorCode(t, rec))
		}
	}

	patchVideo(t, srv, tok, id, `{"download_enabled":true}`)
	for _, route := range routes {
		if rec := sendJSONAuth(srv, http.MethodGet, route, "", ""); rec.Code != http.StatusOK {
			t.Errorf("video-on %s = %d, want 200; body=%s", route, rec.Code, rec.Body.String())
		}
	}
}

// TestBackfilledVideosKeepShippedBehaviour: a pre-W9 video row (no explicit
// policies — the migration backfill: comments 'enabled', download true) keeps
// commenting and downloading exactly as before.
func TestBackfilledVideosKeepShippedBehaviour(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Old","privacy":"public"}`)

	// Simulate the 0089 backfill on a legacy row: the stored policies are the
	// column defaults, regardless of what create-time seeding would have done.
	vid := uuid.MustParse(id)
	row := repo.videos[vid]
	row.CommentsPolicy = "enabled"
	row.DownloadEnabled = true
	repo.videos[vid] = row

	if rec := postJSONAuth(srv, "/api/v1/videos/"+id+"/comments", `{"body":"still works"}`, tok); rec.Code != http.StatusCreated {
		t.Errorf("comment on backfilled video = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getDownloads(srv, id, ""); rec.Code != http.StatusOK {
		t.Errorf("download list on backfilled video = %d, want 200", rec.Code)
	}
}

// TestVideoPayloadsCarryAuthorDisplayName: the uploader account's display name
// rides the detail, feed, and search payloads next to the channel identity
// (W5's miniature_prefer_author_display_name backend half).
func TestVideoPayloadsCarryAuthorDisplayName(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// author_display_name carries the ACCOUNT's display name; empty until the
	// user sets one (the frontend then falls back to the channel name).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Ada L."}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("set display name = %d; body=%s", rec.Code, rec.Body.String())
	}
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Attribution","privacy":"public"}`)

	// Detail.
	rec := getVideo(srv, id, "")
	var detail videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	if detail.AuthorDisplayName == nil || *detail.AuthorDisplayName != "Ada L." {
		t.Errorf("detail author_display_name = %v, want Ada L.", detail.AuthorDisplayName)
	}

	// Feed and search cards.
	for _, path := range []string{"/api/v1/videos", "/api/v1/videos/search?q=Attribution"} {
		rec := sendJSONAuth(srv, http.MethodGet, path, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Videos) == 0 {
			t.Fatalf("%s returned no videos", path)
		}
		card := body.Videos[0]
		if card.AuthorDisplayName == nil || *card.AuthorDisplayName != "Ada L." {
			t.Errorf("%s author_display_name = %v, want Ada L.", path, card.AuthorDisplayName)
		}
		// Cards never claim a per-video policy (the card queries do not read
		// the policy columns).
		if card.CommentsPolicy != "" || card.DownloadEnabled != nil {
			t.Errorf("%s card carries policy fields: comments %q download %v", path, card.CommentsPolicy, card.DownloadEnabled)
		}
	}
}
