package federation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Inbound `sensitive` (T05). Before this, a remote video's sensitive flag was
// read by nothing, so remote NSFW content played with no warning whatever the
// viewer's policy said. These tests pin that the flag is persisted from Create
// AND Update, in every form PeerTube's own validator accepts.

// withSensitive injects a `sensitive` value into a createActivity body.
func withSensitive(body, value string) string {
	return strings.Replace(body, `"type":"Video",`, `"type":"Video","sensitive":`+value+`,`, 1)
}

func TestInboundVideoPersistsSensitive(t *testing.T) {
	cases := []struct {
		name, value string
		want        bool
	}{
		{"absent", "", false},
		{"boolean true", "true", true},
		{"boolean false", "false", false},
		// misc.ts:77-79: PeerTube accepts the string forms, so a peer may send
		// them; a typed bool field would fail the WHOLE object's unmarshal.
		{"string true", `"true"`, true},
		{"string 1", `"1"`, true},
		{"string false", `"false"`, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newIngestRepo()
			svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
			body := createActivity("https://remote.example/act/s"+string(rune('a'+i)), remoteChan, remoteVideo, remoteChan, "Flagged")
			if tc.value != "" {
				body = withSensitive(body, tc.value)
			}
			if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			rv, ok := repo.remoteVideos[remoteVideo]
			if !ok {
				t.Fatal("remote video not stored: a malformed sensitive value must not drop the video")
			}
			if rv.params.IsSensitive != tc.want {
				t.Errorf("is_sensitive = %v, want %v", rv.params.IsSensitive, tc.want)
			}
		})
	}
}

// TestInboundUpdateClearsSensitive: the origin un-flagging a video must reach
// the stored row, not only the first flag.
func TestInboundUpdateClearsSensitive(t *testing.T) {
	repo := newIngestRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	create := withSensitive(createActivity("https://remote.example/act/u1", remoteChan, remoteVideo, remoteChan, "Flagged"), "true")
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(create)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	update := strings.Replace(withSensitive(createActivity("https://remote.example/act/u2", remoteChan, remoteVideo, remoteChan, "Flagged"), "false"), `"type":"Create"`, `"type":"Update"`, 1)
	if err := svc.HandleInbox(context.Background(), remoteChan, []byte(update)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if repo.remoteVideos[remoteVideo].params.IsSensitive {
		t.Error("is_sensitive stayed true after the origin's Update cleared it")
	}
}

// TestEmittedSensitiveSurvivesOurOwnIngest closes the vidra→vidra loop for
// the flag: what VideoObject emits for a sensitive video, our reader keeps.
func TestEmittedSensitiveSurvivesOurOwnIngest(t *testing.T) {
	svc, _ := ptWorld(t)
	obj, err := svc.VideoObject(t.Context(), ptSensitiveID)
	if err != nil {
		t.Fatalf("VideoObject: %v", err)
	}
	raw, _ := json.Marshal(obj)
	var parsed apVideoObject
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !apSensitive(parsed.Sensitive) {
		t.Errorf("our own ingest read sensitive=%s as false", parsed.Sensitive)
	}
}
