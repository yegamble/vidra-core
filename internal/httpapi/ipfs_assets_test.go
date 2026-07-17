package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/video"
)

const testPublicAssetURL = "https://gateway.example/ipfs/bafkreigatewayasset"

func assertIPFSRedirect(t *testing.T, status int, location, cacheControl string) {
	t.Helper()
	if status != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", status)
	}
	if location != testPublicAssetURL {
		t.Errorf("Location = %q, want %q", location, testPublicAssetURL)
	}
	if cacheControl != "public, max-age=300, must-revalidate" {
		t.Errorf("Cache-Control = %q, want short public redirect caching", cacheControl)
	}
}

func TestIdentityIPFSClass(t *testing.T) {
	tests := []struct {
		owner string
		kind  string
		want  ipfsmirror.MediaClass
	}{
		{owner: "user", kind: "avatar", want: ipfsmirror.ClassUserAvatar},
		{owner: "user", kind: "banner", want: ipfsmirror.ClassUserBanner},
		{owner: "channel", kind: "avatar", want: ipfsmirror.ClassChannelAvatar},
		{owner: "channel", kind: "banner", want: ipfsmirror.ClassChannelBanner},
		{owner: "user", kind: "unknown", want: ""},
	}
	for _, tt := range tests {
		if got := identityIPFSClass(tt.owner, tt.kind); got != tt.want {
			t.Errorf("identityIPFSClass(%q, %q) = %q, want %q", tt.owner, tt.kind, got, tt.want)
		}
	}
}

func TestVideoThumbnailRedirectsToPinnedPublicIPFS(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Poster","privacy":"public"}`)
	if rec := uploadThumbnail(srv, id, "poster.png", "image/png", "poster-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("thumbnail upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	key := "thumbnails/" + id + ".jpg"
	mirror.assetURLs[fakeAssetLookupKey(key, ipfsmirror.ClassThumbnail)] = testPublicAssetURL

	rec := getThumbnail(srv, id, "")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))

	// IPFS is optional: an absent pin or a ledger lookup error falls back to the
	// authoritative object instead of failing the asset request.
	delete(mirror.assetURLs, fakeAssetLookupKey(key, ipfsmirror.ClassThumbnail))
	if rec := getThumbnail(srv, id, ""); rec.Code != http.StatusOK || rec.Body.String() != "poster-bytes" {
		t.Errorf("missing pin fallback = %d/%q, want 200/local bytes", rec.Code, rec.Body.String())
	}
	mirror.assetErr = errors.New("ledger unavailable")
	if rec := getThumbnail(srv, id, ""); rec.Code != http.StatusOK || rec.Body.String() != "poster-bytes" {
		t.Errorf("lookup error fallback = %d/%q, want 200/local bytes", rec.Code, rec.Body.String())
	}
}

func TestPrivateVideoThumbnailNeverRedirectsToPublicIPFS(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadThumbnail(srv, id, "poster.jpg", "image/jpeg", "private-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("thumbnail upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	key := "thumbnails/" + id + ".jpg"
	mirror.assetURLs[fakeAssetLookupKey(key, ipfsmirror.ClassThumbnail)] = testPublicAssetURL

	if rec := getThumbnail(srv, id, tok); rec.Code != http.StatusOK || rec.Body.String() != "private-poster" {
		t.Errorf("owner private thumbnail = %d/%q, want 200/local bytes", rec.Code, rec.Body.String())
	}
}

func TestStoryboardImageRedirectsButVTTStaysStable(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{WithIPFSMirrorService(mirror)}, video.WithStoryboarder(storyboarderStub{}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	videoID := uuid.MustParse(id)
	mirror.assetURLs[fakeAssetLookupKey(media.StoryboardKeyJPG(videoID), ipfsmirror.ClassStoryboard)] = testPublicAssetURL
	// Even if the VTT is pinned, keep its application URL stable because its cue
	// references the independently redirected storyboard.jpg route.
	mirror.assetURLs[fakeAssetLookupKey(media.StoryboardKeyVTT(videoID), ipfsmirror.ClassStoryboardVTT)] = testPublicAssetURL

	jpg := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.jpg", "", "")
	assertIPFSRedirect(t, jpg.Code, jpg.Header().Get("Location"), jpg.Header().Get("Cache-Control"))
	vtt := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.vtt", "", "")
	if vtt.Code != http.StatusOK || vtt.Header().Get("Location") != "" {
		t.Errorf("storyboard VTT = %d location=%q, want 200 without redirect", vtt.Code, vtt.Header().Get("Location"))
	}
}

func TestUserAndChannelAvatarsRedirectToPinnedPublicIPFS(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	cfg := testConfig()
	cfg.IPFSEnabled = true
	cfg.IPFSGatewayURL = "https://ipfs.example.org"
	srv := profileImageServerWith(t, cfg, WithIPFSMirrorService(mirror))
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := uploadImage(srv, "/api/v1/me/avatar", "me.png", "user-avatar", tok); rec.Code != http.StatusCreated {
		t.Fatalf("user avatar upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	userKey := "avatars/users/" + userID + ".png"
	mirror.assetURLs[fakeAssetLookupKey(userKey, ipfsmirror.ClassUserAvatar)] = testPublicAssetURL
	rec := getPath(srv, "/api/v1/users/"+userID+"/avatar")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))

	created := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada","display_name":"Ada"}`, tok)
	if created.Code != http.StatusCreated {
		t.Fatalf("channel create = %d; body=%s", created.Code, created.Body.String())
	}
	var ch channelView
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	if rec := uploadImage(srv, "/api/v1/channels/ada/avatar", "logo.webp", "channel-avatar", tok); rec.Code != http.StatusCreated {
		t.Fatalf("channel avatar upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	channelKey := "avatars/channels/" + ch.ID + ".webp"
	mirror.assetURLs[fakeAssetLookupKey(channelKey, ipfsmirror.ClassChannelAvatar)] = testPublicAssetURL
	rec = getPath(srv, "/api/v1/channels/ada/avatar")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
}

func TestPublicPlaylistCoverRedirectsToPinnedPublicIPFS(t *testing.T) {
	mirror := &fakeIPFSMirror{assetURLs: map[string]string{}}
	srv := ipfsVideoServer(t, mirror)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "cover-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("cover upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	key := media.PlaylistThumbnailKey(uuid.MustParse(pl.ID), "jpg")
	mirror.assetURLs[fakeAssetLookupKey(key, ipfsmirror.ClassPlaylistCover)] = testPublicAssetURL

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", "")
	assertIPFSRedirect(t, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
}
