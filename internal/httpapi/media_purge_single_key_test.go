package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Single-key purge coverage: avatars, banners and playlist covers live at ONE
// stable identity key each (avatars/users/<id><ext>, playlist-thumbnails/
// <id>.<ext>, …), flow through the delivery resolver under Redirectable
// classes, and are replaced IN PLACE — so without a purge, a replacement
// leaves the edge serving the old bytes until its TTL expires and a deletion
// leaves them there forever. Same contract as media_purge_test.go: the tests
// assert the exact KEY, not merely that a purge fired, because the key the
// edge cached is the OLD one — on an extension-changing replacement a purge of
// the new key would look like a working invalidation and evict nothing.

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

// TestReplaceAvatarPurgesTheKeyTheEdgeCached. The key the edge cached is the
// OLD one: on a same-extension re-upload it is the very key being overwritten,
// and on an extension change it is the superseded key whose blob the service
// deletes — either way the pre-mutation key is the one to purge.
func TestReplaceAvatarPurgesTheKeyTheEdgeCached(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	// Extension change: the .png key is the cached one.
	if r := uploadImage(srv, "/api/v1/me/avatar", "me2.jpg", "jpeg-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"avatars/users/" + userID + ".png"})
	// Same-extension replacement: the overwritten .jpg key must be purged too
	// (the recorder accumulates, so the expected set now holds both keys).
	if r := uploadImage(srv, "/api/v1/me/avatar", "me3.jpg", "jpeg-bytes-2", tok); r.Code != http.StatusCreated {
		t.Fatalf("re-replace avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{
		"avatars/users/" + userID + ".jpg",
		"avatars/users/" + userID + ".png",
	})
}

// TestDeleteAvatarPurgesItsEdgeKey. The delete handler removes row and blob;
// without a purge the edge keeps the bytes with nothing left to name them.
func TestDeleteAvatarPurgesItsEdgeKey(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if r := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/avatar", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete avatar = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"avatars/users/" + userID + ".png"})
}

// createChannelView creates a channel and returns its view (the tests need the
// channel ID to name the expected storage keys).
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

// TestChannelBannerReplaceAndDeletePurge. Channel images follow the exact
// same stable-key contract as user images, through their own handlers.
func TestChannelBannerReplaceAndDeletePurge(t *testing.T) {
	srv, rec := avatarPurgeServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	ch := createChannelView(t, srv, tok, "ada")
	if r := uploadImage(srv, "/api/v1/channels/ada/banner", "b.png", "\x89PNG-fake", tok); r.Code != http.StatusCreated {
		t.Fatalf("set banner = %d; body=%s", r.Code, r.Body.String())
	}
	if r := uploadImage(srv, "/api/v1/channels/ada/banner", "b2.webp", "webp-bytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("replace banner = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"banners/channels/" + ch.ID + ".png"})
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada/banner", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete banner = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{
		"banners/channels/" + ch.ID + ".png",
		"banners/channels/" + ch.ID + ".webp",
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

// TestReplacePublicPlaylistCoverPurgesTheKeyTheEdgeCached. A public playlist's
// cover is Eligible and Redirectable at the stable key playlist-thumbnails/
// <id>.<ext>; SetThumbnail replaces it in place. Like the avatar tests, the
// PRE-mutation key is the one that must go — on an extension change it is the
// superseded key the edge cached.
func TestReplacePublicPlaylistCoverPurgesTheKeyTheEdgeCached(t *testing.T) {
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
	waitForPurge(t, rec, []string{"playlist-thumbnails/" + pl.ID + ".jpg"})
}

// TestDeletePublicPlaylistCoverPurgesItsEdgeKey. ClearThumbnail removes blob
// and column; the edge copy must go with them.
func TestDeletePublicPlaylistCoverPurgesItsEdgeKey(t *testing.T) {
	srv, rec := playlistPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if r := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "\xff\xd8\xff\xe0jpegbytes", tok); r.Code != http.StatusCreated {
		t.Fatalf("set cover = %d; body=%s", r.Code, r.Body.String())
	}
	if r := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", tok); r.Code != http.StatusNoContent {
		t.Fatalf("delete cover = %d; body=%s", r.Code, r.Body.String())
	}
	waitForPurge(t, rec, []string{"playlist-thumbnails/" + pl.ID + ".jpg"})
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
	waitForPurge(t, rec, []string{"playlist-thumbnails/" + pl.ID + ".jpg"})
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
	waitForPurge(t, rec, []string{"playlist-thumbnails/" + pl.ID + ".jpg"})
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
	ch := createChannelView(t, srv, tok, "ada")
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
		"avatars/channels/" + ch.ID + ".png",
		"banners/channels/" + ch.ID + ".jpg",
	})
}
