package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestCreateAndUpdateCarryPublishAfterTranscode proves the flag round-trips on
// the create and update request/response bodies.
func TestCreateAndUpdateCarryPublishAfterTranscode(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"held","privacy":"public","publish_after_transcode":true}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.PublishAfterTranscode == nil || !*v.PublishAfterTranscode {
		t.Fatalf("create publish_after_transcode = %v, want true", v.PublishAfterTranscode)
	}

	// PATCH the flag back off.
	up := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+v.ID,
		`{"publish_after_transcode":false}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", up.Code, up.Body.String())
	}
	var v2 videoView
	_ = json.Unmarshal(up.Body.Bytes(), &v2)
	if v2.PublishAfterTranscode == nil || *v2.PublishAfterTranscode {
		t.Fatalf("patched publish_after_transcode = %v, want false", v2.PublishAfterTranscode)
	}
}

// TestPatchPublishAfterTranscodeOnlyIsAccepted proves the flag alone satisfies
// the "at least one updatable field" rule.
func TestPatchPublishAfterTranscodeOnlyIsAccepted(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"draft","privacy":"public"}`)
	up := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"publish_after_transcode":true}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("patch flag-only = %d; body=%s", up.Code, up.Body.String())
	}
}

// TestVideoDetailTranscodingFlag proves the DETAIL view's `transcoding` boolean
// tracks whether a transcode job is still live for the video.
func TestVideoDetailTranscodingFlag(t *testing.T) {
	srv, _, tcRepo, _, _ := videoServerFull(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"proc","privacy":"public"}`)
	vid := uuid.MustParse(id)

	// No job yet: transcoding omitted (false).
	var v videoView
	_ = json.Unmarshal(getVideo(srv, id, tok).Body.Bytes(), &v)
	if v.Transcoding {
		t.Fatalf("transcoding = true with no job, want false")
	}

	// A live job -> transcoding true.
	if err := tcRepo.EnqueueTranscodeJob(context.Background(), sqlcgen.EnqueueTranscodeJobParams{
		VideoID: vid, SourceKey: "web-videos/x.mp4", TranscodeType: "all",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	_ = json.Unmarshal(getVideo(srv, id, tok).Body.Bytes(), &v)
	if !v.Transcoding {
		t.Fatalf("transcoding = false with a live job, want true")
	}

	// Job done -> transcoding false again.
	for jid := range tcRepo.jobs {
		tcRepo.jobs[jid].State = "done"
	}
	var v3 videoView
	_ = json.Unmarshal(getVideo(srv, id, tok).Body.Bytes(), &v3)
	if v3.Transcoding {
		t.Fatalf("transcoding = true after job done, want false")
	}
}

// TestTranscodingHoldDetailVisibility proves a held video (state 'transcoding')
// is hidden (404) from anonymous and unrelated users but visible to its owner and
// to moderators, carrying state='transcoding' in the payload.
func TestTranscodingHoldDetailVisibility(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	// The first account is the admin/moderator (harness convention).
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")
	stranger := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	id := createVideo(t, srv, owner, "bobtube", `{"title":"held","privacy":"public","publish_after_transcode":true}`)
	// Drop the video into the held state directly (a fast upload's transcode hold).
	vid := uuid.MustParse(id)
	row := repo.videos[vid]
	row.State = "transcoding"
	repo.videos[vid] = row

	if rec := getVideo(srv, id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon get transcoding = %d, want 404", rec.Code)
	}
	if rec := getVideo(srv, id, stranger); rec.Code != http.StatusNotFound {
		t.Errorf("stranger get transcoding = %d, want 404", rec.Code)
	}

	rec := getVideo(srv, id, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner get transcoding = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ownerView videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &ownerView)
	if ownerView.State != "transcoding" {
		t.Errorf("owner sees state %q, want transcoding", ownerView.State)
	}

	mod := getVideo(srv, id, admin)
	if mod.Code != http.StatusOK {
		t.Fatalf("moderator get transcoding = %d, want 200", mod.Code)
	}
	var modView videoView
	_ = json.Unmarshal(mod.Body.Bytes(), &modView)
	if modView.State != "transcoding" {
		t.Errorf("moderator sees state %q, want transcoding", modView.State)
	}
}
