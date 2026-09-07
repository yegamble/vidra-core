package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ownerChannelListing parses GET /channels/{handle}/videos — the OWNER's
// management view (drafts, private and scheduled rows included), which is a
// different surface from the public channel page.
type ownerChannelListing struct {
	Videos []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		State       string `json:"state"`
		Privacy     string `json:"privacy"`
		Blocked     bool   `json:"blocked"`
		BlockReason string `json:"block_reason"`
	} `json:"videos"`
	Total int64 `json:"total"`
}

func ownerListing(t *testing.T, srv *Server, handle, token string) ownerChannelListing {
	t.Helper()
	rec := getWithAuth(srv, "/api/v1/channels/"+handle+"/videos", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner listing = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body ownerChannelListing
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("owner listing not JSON: %v", err)
	}
	return body
}

// TestBlockedVideoTellsItsOwner closes the sharpest gap A16 slice 2 recorded: a
// moderator block makes a video 404 for EVERYONE including its owner, removes it
// from every public surface, and told the creator nothing at all — while the
// owner's own management listing kept reading `published` with no field that
// could say otherwise. A creator's video became unreachable to them while their
// dashboard said it was live.
//
// The block itself is unchanged (state and privacy stay exactly what they were,
// which is why the marker has to be its own column rather than something read
// off the row): what changes is that the owner's listing now carries it and the
// owner is notified. Slice 2 left "may a creator read the block reason?" open and
// shipped the neutral notice; the A16 ruling settled it YES, so this test now
// asserts the reason reaches the owner on BOTH surfaces — the listing row and
// the notification — while the video itself stays 404 for them and the reason
// stays unreachable to everybody else (TestBlockReasonIsOwnerOnly).
func TestBlockedVideoTellsItsOwner(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Owner Clip","privacy":"public"}`)

	before := ownerListing(t, srv, "bobtube", owner)
	if len(before.Videos) != 1 || before.Videos[0].ID != vid {
		t.Fatalf("owner listing before block = %+v, want the one video", before.Videos)
	}
	if before.Videos[0].Blocked {
		t.Errorf("unblocked video reads blocked=true in the owner listing")
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"copyright-strike-prose"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The block's reach is unchanged: the owner still cannot read the video.
	if rec := getVideo(srv, vid, owner); rec.Code != http.StatusNotFound {
		t.Errorf("owner get blocked video = %d, want 404 (the block must not be weakened)", rec.Code)
	}

	after := ownerListing(t, srv, "bobtube", owner)
	if len(after.Videos) != 1 {
		t.Fatalf("owner listing after block = %+v, want the video still listed", after.Videos)
	}
	if !after.Videos[0].Blocked {
		t.Errorf("owner listing after block = %+v, want blocked=true", after.Videos[0])
	}
	if after.Videos[0].State != "published" {
		t.Errorf("state after block = %q, want published (a block changes neither state nor privacy)", after.Videos[0].State)
	}
	if after.Videos[0].BlockReason != "copyright-strike-prose" {
		t.Errorf("owner listing block_reason = %q, want the moderator's reason", after.Videos[0].BlockReason)
	}

	// The owner is told, without the moderator's identity or prose.
	nrec := getWithAuth(srv, "/api/v1/me/notifications", owner)
	if nrec.Code != http.StatusOK {
		t.Fatalf("notifications = %d; body=%s", nrec.Code, nrec.Body.String())
	}
	var notifs struct {
		Notifications []struct {
			Type           string          `json:"type"`
			VideoID        string          `json:"video_id"`
			VideoTitle     string          `json:"video_title"`
			ModerationNote string          `json:"moderation_note"`
			Actor          json.RawMessage `json:"actor"`
		} `json:"notifications"`
	}
	_ = json.Unmarshal(nrec.Body.Bytes(), &notifs)
	found := false
	for _, n := range notifs.Notifications {
		if n.Type != "video_blocked" {
			continue
		}
		found = true
		if n.VideoID != vid || n.VideoTitle != "Owner Clip" {
			t.Errorf("video_blocked context = (%q, %q), want (%s, Owner Clip)", n.VideoID, n.VideoTitle, vid)
		}
		if len(n.Actor) != 0 {
			t.Errorf("video_blocked exposes the moderator: %s", n.Actor)
		}
		if n.ModerationNote != "copyright-strike-prose" {
			t.Errorf("video_blocked moderation_note = %q, want the moderator's reason — a creator who is told nothing cannot appeal or fix it", n.ModerationNote)
		}
	}
	if !found {
		t.Fatalf("owner got no video_blocked notification: %+v", notifs.Notifications)
	}

	// Unblocking clears the marker again.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	unblocked := ownerListing(t, srv, "bobtube", owner)
	if unblocked.Videos[0].Blocked {
		t.Errorf("owner listing after unblock still reads blocked=true")
	}
	if unblocked.Videos[0].BlockReason != "" {
		t.Errorf("owner listing after unblock still carries a block reason: %q", unblocked.Videos[0].BlockReason)
	}
}

// TestBlockReasonIsOwnerOnly is the other half of the ruling: making the reason
// creator-facing must not make it PUBLIC. The reason is moderator prose about a
// third party, so every caller who is not the video's owner — a signed-in
// stranger and an anonymous reader — must see neither the row nor the reason,
// and the video itself must stay 404 for all of them (including the owner).
func TestBlockReasonIsOwnerOnly(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	stranger := createChannelFor(t, srv, "eve", "eve@example.test", "evetube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Owner Clip","privacy":"public"}`)
	const reason = "block-reason-prose-not-for-strangers"
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"`+reason+`"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A signed-in stranger reading the same channel listing.
	srec := getWithAuth(srv, "/api/v1/channels/bobtube/videos", stranger)
	if strings.Contains(srec.Body.String(), reason) {
		t.Errorf("a stranger's channel listing carries the block reason: %s", srec.Body.String())
	}
	// Anonymous, on the public listing.
	arec := get(t, srv, "/api/v1/channels/bobtube/videos")
	if strings.Contains(arec.Body.String(), reason) {
		t.Errorf("the anonymous channel listing carries the block reason: %s", arec.Body.String())
	}
	// And the video stays unreachable for all three, owner included.
	for who, tok := range map[string]string{"owner": owner, "stranger": stranger, "anonymous": ""} {
		if rec := getVideo(srv, vid, tok); rec.Code != http.StatusNotFound {
			t.Errorf("%s get blocked video = %d, want 404 — the block must not be weakened by showing its reason", who, rec.Code)
		}
	}
}

// TestUnblockNotifiesTheOwner closes the loop the block notice opened. Before
// this the creator was told their video had been taken down and never told when
// it came back: A16 slice 2 and slice 3 both recorded "unblocking still notifies
// nobody" as an open finding. The notice is its own type (video_unblocked), it
// links to the restored video, and — because the unblock route is idempotent —
// a repeated unblock must not deliver a second one.
func TestUnblockNotifiesTheOwner(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Owner Clip","privacy":"public"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"a reason"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}

	countUnblocked := func() (int, string) {
		t.Helper()
		nrec := getWithAuth(srv, "/api/v1/me/notifications", owner)
		if nrec.Code != http.StatusOK {
			t.Fatalf("notifications = %d; body=%s", nrec.Code, nrec.Body.String())
		}
		var notifs struct {
			Notifications []struct {
				Type       string          `json:"type"`
				VideoID    string          `json:"video_id"`
				VideoTitle string          `json:"video_title"`
				Actor      json.RawMessage `json:"actor"`
			} `json:"notifications"`
		}
		if err := json.Unmarshal(nrec.Body.Bytes(), &notifs); err != nil {
			t.Fatalf("notifications not JSON: %v", err)
		}
		n := 0
		for _, it := range notifs.Notifications {
			if it.Type != "video_unblocked" {
				continue
			}
			n++
			if it.VideoID != vid || it.VideoTitle != "Owner Clip" {
				t.Errorf("video_unblocked context = (%q, %q), want (%s, Owner Clip)", it.VideoID, it.VideoTitle, vid)
			}
			if len(it.Actor) != 0 {
				t.Errorf("video_unblocked exposes the moderator: %s", it.Actor)
			}
		}
		return n, nrec.Body.String()
	}

	if n, body := countUnblocked(); n != 1 {
		t.Fatalf("video_unblocked notifications after one unblock = %d, want 1; body=%s", n, body)
	}
	// The video really is back for its owner, which is what the notice claims.
	if rec := getVideo(srv, vid, owner); rec.Code != http.StatusOK {
		t.Errorf("owner get after unblock = %d, want 200", rec.Code)
	}

	// Idempotent unblock: same 204, no second notice.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+vid+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	if n, body := countUnblocked(); n != 1 {
		t.Fatalf("video_unblocked notifications after a repeated unblock = %d, want 1 — an idempotent route delivered a second notice; body=%s", n, body)
	}
}

// TestPlaylistAddRejectsBlockedVideo closes A16 slice 2's finding 6:
// handleAddPlaylistItem asked only "public and published?", which a moderator
// block changes neither of, so a blocked video could still be appended to a
// playlist. The read side filters it out, so the row was inert — but a write
// that succeeds on content the instance has taken down is the wrong answer, and
// it becomes a live item the moment the block is lifted.
func TestPlaylistAddRejectsBlockedVideo(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	viewer := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Taken Down","privacy":"public"}`)
	pl := createPlaylist(t, srv, viewer, `{"title":"Faves"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+vid+`"}`, viewer); rec.Code != http.StatusNoContent {
		t.Fatalf("add before block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/playlists/"+pl.ID+"/videos/"+vid, "", viewer); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+vid+"/block", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/playlists/"+pl.ID+"/videos", `{"video_id":"`+vid+`"}`, viewer); rec.Code != http.StatusNotFound {
		t.Errorf("add blocked video = %d, want 404 (same answer as an unknown id)", rec.Code)
	}
}

// TestEmbedPrivacyHidesRejectedVideo closes A16 slice 2's finding 5. The detail
// route hides a rejected (state=failed) upload from everyone but its owner and
// staff, but GET /videos/{id}/embed-privacy reaches the row through
// videoReadBase, which gated blocks, quarantine, scheduled and transcoding and
// simply did not know about `failed` — so it answered 200 to an anonymous
// caller for a video the moderation queue had just refused. It leaks no content,
// but it is an existence oracle contradicting the surface promise, and the two
// routes must answer the same question the same way.
func TestEmbedPrivacyHidesRejectedVideo(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	vid := createPublishedVideo(t, srv, owner, "bobtube", `{"title":"Refused","privacy":"public"}`)

	embed := func(token string) int {
		return sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+vid+"/embed-privacy", "", token).Code
	}
	if got := embed(""); got != http.StatusOK {
		t.Fatalf("anonymous embed-privacy on a published video = %d, want 200", got)
	}

	// Drive the row to `failed`, which is exactly what a rejection leaves behind.
	row := repo.videos[uuid.MustParse(vid)]
	row.State = "failed"
	repo.videos[uuid.MustParse(vid)] = row

	if got := embed(""); got != http.StatusNotFound {
		t.Errorf("anonymous embed-privacy on a rejected video = %d, want 404", got)
	}
	if got := embed(owner); got != http.StatusOK {
		t.Errorf("owner embed-privacy on their rejected video = %d, want 200", got)
	}
	if got := embed(admin); got != http.StatusOK {
		t.Errorf("staff embed-privacy on a rejected video = %d, want 200", got)
	}
	// The detail route already answered this way; the two must agree.
	if got := getVideo(srv, vid, "").Code; got != http.StatusNotFound {
		t.Errorf("anonymous detail on a rejected video = %d, want 404", got)
	}
}
