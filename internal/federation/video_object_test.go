package federation

import (
	"encoding/json"
	"testing"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The outbound Video and the inbound reader are two halves of one loop, and
// A29 proved they can be individually correct and jointly useless: the reader
// handled icon/duration/url-array perfectly while the writer emitted none of
// them. These tests drive a REAL emitted activity through the REAL ingest
// parsers rather than through a fixture either side could drift from.

// parseEmittedVideo runs an emitted Create/Update activity through the same
// json path handleCreateVideo takes.
func parseEmittedVideo(t *testing.T, payload []byte) apVideoObject {
	t.Helper()
	var act inboxActivity
	if err := json.Unmarshal(payload, &act); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	var obj apVideoObject
	if err := json.Unmarshal(act.Object, &obj); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if obj.Type != "Video" {
		t.Fatalf("object type = %q, want Video", obj.Type)
	}
	return obj
}

// TestEmittedVideoIsReadableByOurOwnIngest closes the A29-F1 loop: everything
// the follower needs to render a player is not merely PRESENT in the emitted
// document, it survives the reader.
func TestEmittedVideoIsReadableByOurOwnIngest(t *testing.T) {
	repo := newContractRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	payload, err := svc.buildVideoActivity("Create", "films", repo.videosByID[ctVideoID])
	if err != nil {
		t.Fatalf("buildVideoActivity: %v", err)
	}
	obj := parseEmittedVideo(t, payload)

	watchURL, streamURL := videoLinks(obj.URL)
	wantStream := "https://videos.example/api/v1/videos/" + ctVideoID.String() + "/hls/master.m3u8"
	if streamURL == nil {
		t.Fatalf("videoLinks found no playable stream in %s", obj.URL)
	}
	if *streamURL != wantStream {
		t.Errorf("stream_url = %q, want %q", *streamURL, wantStream)
	}
	if want := "https://videos.example/videos/" + ctVideoID.String(); watchURL != want {
		t.Errorf("watch_url = %q, want %q", watchURL, want)
	}
	if got, want := iconURL(obj.Icon), "https://videos.example/api/v1/videos/"+ctVideoID.String()+"/thumbnail"; got != want {
		t.Errorf("icon url = %q, want %q", got, want)
	}
	d := parseISODurationSeconds(obj.Duration)
	if d == nil || *d != 367 {
		t.Errorf("duration = %v (raw %q), want 367", d, obj.Duration)
	}
	if obj.Published == "" {
		t.Error("published is empty; a follower cannot order the video without it")
	}
}

// TestEmittedVideoOmitsWhatTheOriginCannotBack: a video mid-transcode has no
// ladder and no poster. The object must carry the html link and nothing else —
// an empty icon or a PT0S duration would be a claim the origin cannot honour,
// and the reader would faithfully store it.
func TestEmittedVideoOmitsWhatTheOriginCannotBack(t *testing.T) {
	repo := newContractRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	bare := sqlcgen.GetVideoByIDRow{
		ID: ctVideo2ID, ChannelID: ctChannelID,
		Title: "Still cooking", Description: "",
		Privacy: "public", State: "published",
	}
	repo.videosByID[ctVideo2ID] = bare

	payload, err := svc.buildVideoActivity("Create", "films", bare)
	if err != nil {
		t.Fatalf("buildVideoActivity: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj, ok := raw["object"].(map[string]any)
	if !ok {
		t.Fatalf("object is not a JSON object: %T", raw["object"])
	}
	for _, key := range []string{"icon", "duration"} {
		if _, present := obj[key]; present {
			t.Errorf("%q is present on a video with no poster and no ladder: %v", key, obj[key])
		}
	}
	parsed := parseEmittedVideo(t, payload)
	if _, streamURL := videoLinks(parsed.URL); streamURL != nil {
		t.Errorf("stream_url = %q; a video with no ready ladder must advertise none", *streamURL)
	}
}

// TestEmittedOutboxVideoMatchesTheDeliveredOne: discovery by walking the outbox
// and receiving the push must yield the same object, or a peer that does both
// sees a video change shape for no reason.
func TestEmittedOutboxVideoMatchesTheDeliveredOne(t *testing.T) {
	repo := newContractRepo()
	repo.outboxVideos[ctChannelID] = []sqlcgen.ListChannelOutboxVideosRow{{
		ID:                    ctVideoID,
		Title:                 "Dawn over the fjord",
		Description:           "A quiet opening.",
		ShortCode:             "",
		CreatedAt:             ctCreatedAt,
		UpdatedAt:             ctUpdatedAt,
		OriginallyPublishedAt: repo.videosByID[ctVideoID].OriginallyPublishedAt,
		DurationSeconds:       ctDurationPtr(367),
		HasThumbnail:          true,
		ThumbnailContentType:  "image/jpeg",
		HasHls:                true,
	}}
	svc := NewService(repo, WithBaseURL("https://videos.example"))

	pushed, err := svc.buildVideoActivity("Create", "films", repo.videosByID[ctVideoID])
	if err != nil {
		t.Fatalf("buildVideoActivity: %v", err)
	}
	var pushedAct map[string]any
	if err := json.Unmarshal(pushed, &pushedAct); err != nil {
		t.Fatalf("unmarshal pushed: %v", err)
	}

	page, err := svc.ChannelOutboxPage(t.Context(), "films", 1)
	if err != nil {
		t.Fatalf("ChannelOutboxPage: %v", err)
	}
	if len(page.OrderedItems) != 1 {
		t.Fatalf("outbox items = %d, want 1", len(page.OrderedItems))
	}
	got, err := json.Marshal(page.OrderedItems[0]["object"])
	if err != nil {
		t.Fatalf("marshal outbox object: %v", err)
	}
	want, err := json.Marshal(pushedAct["object"])
	if err != nil {
		t.Fatalf("marshal pushed object: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("outbox object differs from the delivered one\n outbox: %s\n pushed: %s", got, want)
	}
}

// TestISODurationRoundTrip pins the one encoding both halves must agree on.
func TestISODurationRoundTrip(t *testing.T) {
	for _, seconds := range []int32{1, 6, 367, 3661} {
		s := seconds
		encoded := isoDuration(&s)
		back := parseISODurationSeconds(encoded)
		if back == nil || *back != seconds {
			t.Errorf("isoDuration(%d) = %q, parsed back as %v", seconds, encoded, back)
		}
	}
	if got := isoDuration(nil); got != "" {
		t.Errorf("isoDuration(nil) = %q, want empty", got)
	}
	zero := int32(0)
	if got := isoDuration(&zero); got != "" {
		t.Errorf("isoDuration(0) = %q, want empty", got)
	}
}
