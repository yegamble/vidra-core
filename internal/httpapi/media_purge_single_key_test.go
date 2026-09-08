package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Single-URL purge coverage: avatars, banners and playlist covers each live at
// ONE stable ROUTE (/api/v1/users/<id>/avatar, /api/v1/playlists/<id>/thumbnail,
// …), flow through the delivery resolver under Redirectable classes, and are
// replaced in place — so without a purge, a replacement leaves the edge serving
// the old bytes until its TTL expires and a deletion leaves them there forever.
//
// ONE HAZARD DISAPPEARED WHEN THE CDN'S ORIGIN BECAME THIS API, and it is worth
// recording rather than silently dropping. These purges used to have to name
// the PRE-mutation storage KEY, because an extension-changing replacement moved
// the object from avatars/users/<id>.png to .jpg and a purge of the new key
// would have looked like a working invalidation while evicting nothing. The
// ROUTE does not carry the extension, so the URL the edge cached and the URL to
// purge are the same string before and after — the ordering still matters for
// the ROW read (an unset image must not spend a purge call), but the
// old-key/new-key trap is gone by construction.

// avatarPurgeServer is the profile-image harness with the CDN purge recorder
// mounted.
func avatarPurgeServer(t *testing.T) (*Server, *purgeRecorder) {
	t.Helper()
	opt, rec := testCDNPurge(t)
	return profileImageServerWith(t, testConfig(), opt), rec
}

// TestFirstAvatarUploadPurgesNothing. Before the first upload there has never
// been an object at the key, so there is nothing at the edge to invalidate —
// and a purge here would spend a purge-API call per profile creation.
func TestFirstAvatarUploadPurgesNothing(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
}

// TestReplaceAvatarPurgesTheURLTheEdgeCached. The avatar route is one stable
// URL, so BOTH kinds of replacement — same extension and extension-changing —
// invalidate that one URL, and two replacements are two purges of it.
func TestReplaceAvatarPurgesTheURLTheEdgeCached(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	// Extension change: the .png key is the cached one.
	if r := uploadImage(srv, "/api/v1/me/avatar", "me2.jpg", "jpeg-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/users/" + userID + "/avatar"})
	// A second replacement purges the same URL again (the recorder accumulates).
	if r := uploadImage(srv, "/api/v1/me/avatar", "me3.jpg", "jpeg-bytes-2", tok); r.Code != http.StatusCreated {
		t.Fatalf("re-replace avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{
		"/api/v1/users/" + userID + "/avatar",
		"/api/v1/users/" + userID + "/avatar",
	})
}

// TestDeleteAvatarPurgesItsEdgeURL. The delete handler removes row and blob;
// without a purge the edge keeps the bytes with nothing left to name them.
func TestDeleteAvatarPurgesItsEdgeURL(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/avatar", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/users/" + userID + "/avatar"})
}

// createChannelView creates a channel and returns its view.
func createChannelView(t *testing.T, srv *Server, tok, handle string) channelView {
	t.Helper()
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"`+handle+`","display_name":"`+handle+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v channelView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("channel view: %v", err)
	}
	return v
}

// TestChannelBannerReplaceAndDeletePurge. Channel images follow the exact same
// stable-URL contract as user images, through their own handlers — addressed by
// HANDLE, which is what the route is addressed by.
func TestChannelBannerReplaceAndDeletePurge(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_ = createChannelView(t, srv, tok, "ada")
	if r := uploadImage(srv, "/api/v1/channels/ada/banner", "b.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set banner = %d; body=%s", r.Code, r.Body.String())
	}
	if r := uploadImage(srv, "/api/v1/channels/ada/banner", "b2.webp", "webp-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace banner = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/channels/ada/banner"})
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada/banner", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete banner = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{
		"/api/v1/channels/ada/banner",
		"/api/v1/channels/ada/banner",
	})
}

// playlistPurgeServer is the full video harness (it wires the playlist
// service with storage) with the CDN purge recorder mounted.
func playlistPurgeServer(t *testing.T) (*Server, *purgeRecorder) {
	t.Helper()
	opt, rec := testCDNPurge(t)
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{opt})
	return srv, rec
}

// TestReplacePublicPlaylistCoverPurgesTheURLTheEdgeCached. A public playlist's
// cover is Eligible and Redirectable at the stable route
// /api/v1/playlists/<id>/thumbnail; SetThumbnail replaces its bytes in place.
func TestReplacePublicPlaylistCoverPurgesTheURLTheEdgeCached(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	// First cover: never cached, nothing to invalidate.
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover2.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace cover = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/playlists/" + pl.ID + "/thumbnail"})
}

// TestDeletePublicPlaylistCoverPurgesItsEdgeURL. ClearThumbnail removes blob
// and column; the edge copy must go with them.
func TestDeletePublicPlaylistCoverPurgesItsEdgeURL(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete cover = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/playlists/" + pl.ID + "/thumbnail"})
}

// TestDeletePublicPlaylistPurgesItsCover. Deleting the playlist deletes the
// row that names the cover without visiting the cover handler — the same
// cascade trap as channels, at single-key scale.
func TestDeletePublicPlaylistPurgesItsCover(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID, "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete playlist = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/playlists/" + pl.ID + "/thumbnail"})
}

// TestPlaylistVisibilityFlipAwayFromPublicPurgesCover. The same privacy-leak
// class the video privacy-flip purge closed: cover eligibility is
// `visibility == "public"`, so leaving public is the moment the edge copy
// becomes unauthorized — while an ordinary title edit purges nothing.
func TestPlaylistVisibilityFlipAwayFromPublicPurgesCover(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+pl.ID, `{"title":"Renamed"}`, tok); r.Code != http.StatusOK {
		t.Fatalf("title edit = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
	if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/playlists/"+pl.ID, `{"visibility":"private"}`, tok); r.Code != http.StatusOK {
		t.Fatalf("visibility flip = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/playlists/" + pl.ID + "/thumbnail"})
}

// TestPrivatePlaylistCoverNeverPurged. A non-public cover fails the resolver's
// eligibility fence, so it structurally never reached the edge — replacing or
// deleting it must not spend purge-API calls.
func TestPrivatePlaylistCoverNeverPurged(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Secret","visibility":"private"}`)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover2.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID, "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete playlist = %d; body=%s", r.Code, r.Body.String())
	}
	assertNoPurge(t, rec)
}

// TestDeleteChannelPurgesItsImages. Deleting a channel cascades its
// avatar/banner rows away at the database (0040 ON DELETE CASCADE) without
// visiting the image delete handlers — the same trap as the video cascade, at
// single-key scale. This harness has no video service at all, which also
// proves the channel-delete purge path is nil-safe on a minimal wiring.
func TestDeleteChannelPurgesItsImages(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_ = createChannelView(t, srv, tok, "ada")
	if r := uploadImage(srv, "/api/v1/channels/ada/avatar", "a.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	if r := uploadImage(srv, "/api/v1/channels/ada/banner", "b.jpg", "jpeg-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set banner = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{
		"/api/v1/channels/ada/avatar",
		"/api/v1/channels/ada/banner",
	})
}

// TestSingleURLPurgeCountsInTheExerciseRecord. The cdn_purge counters are the
// operator's only answer to "has purge been exercised" — if single-URL
// invalidations (avatars, banners, covers) bypassed them, an install that
// only ever replaced images would read "0 runs" while purging daily. Deltas
// with >=, not ==: the counters are package-global and other purge tests run
// in parallel.
func TestSingleURLPurgeCountsInTheExerciseRecord(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	runsBefore, purgedBefore, _, _ := videoEdgePurgeCounters()
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	if r := uploadImage(srv, "/api/v1/me/avatar", "me2.jpg", "jpeg-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"/api/v1/users/" + userID + "/avatar"})
	// The record lands after the purge call returns on the detached goroutine —
	// poll briefly rather than racing it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, purged, _, _ := videoEdgePurgeCounters()
		if runs >= runsBefore+1 && purged >= purgedBefore+1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	runs, purged, _, _ := videoEdgePurgeCounters()
	t.Fatalf("single-key purge not recorded: runs %d->%d, purged %d->%d",
		runsBefore, runs, purgedBefore, purged)
}
