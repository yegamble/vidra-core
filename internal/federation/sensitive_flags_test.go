package federation

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// withTag injects a `tag` array into a createActivity body's Video object.
func withTag(body, tag string) string {
	return strings.Replace(body, `"type":"Video",`, `"type":"Video","tag":`+tag+`,`, 1)
}

// Per-flag sensitive content (0173) on the wire the way PeerTube 7.2 carries
// it: `sensitive` boolean (Mastodon keeps reading it), `summary` text, and the
// kinds as SensitiveTag entries in `tag` (vidra "sex" = PeerTube explicitSex).
func TestOutboundSensitiveFlags(t *testing.T) {
	svc := NewService(newIngestRepo(), WithBaseURL("https://videos.example"))
	row := sqlcgen.ListChannelOutboxVideosRow{
		ID: uuid.New(), Title: "T", IsSensitive: true,
		SensitiveFlags: []string{"violent", "sex"}, SensitiveReason: "graphic injury",
	}
	obj := svc.videoObjectFromOutbox(context.Background(), "https://videos.example/channels/c", row)
	if obj["sensitive"] != true {
		t.Errorf("sensitive = %v, want true (Mastodon reads it)", obj["sensitive"])
	}
	if obj["summary"] != "graphic injury" {
		t.Errorf("summary = %v, want the creator's text", obj["summary"])
	}
	var names []string
	for _, tg := range obj["tag"].([]map[string]any) {
		if tg["type"] == "SensitiveTag" {
			names = append(names, tg["name"].(string))
		}
	}
	if want := []string{"violent", "explicitSex"}; !reflect.DeepEqual(names, want) {
		t.Errorf("SensitiveTag names = %v, want %v", names, want)
	}

	// Not sensitive: no tags, no summary even if text is stored.
	row.IsSensitive = false
	obj = svc.videoObjectFromOutbox(context.Background(), "https://videos.example/channels/c", row)
	if _, ok := obj["summary"]; ok {
		t.Error("summary emitted for a video that is not sensitive")
	}
}

func TestInboundSensitiveFlags(t *testing.T) {
	cases := []struct {
		name, sensitive, tag string
		want                 []string
	}{
		{"peertube explicitSex", "true", `[{"type":"SensitiveTag","name":"explicitSex"},{"type":"Hashtag","name":"#x"}]`, []string{"sex"}},
		{"both, canonical order", "true", `[{"type":"SensitiveTag","name":"explicitSex"},{"type":"SensitiveTag","name":"violent"}]`, []string{"violent", "sex"}},
		{"vidra shocking", "true", `[{"type":"SensitiveTag","name":"shocking"}]`, []string{"shocking"}},
		{"unknown dropped", "true", `[{"type":"SensitiveTag","name":"gore9"}]`, []string{}},
		{"flags need sensitive", "false", `[{"type":"SensitiveTag","name":"violent"}]`, []string{}},
		{"malformed tag does not drop the video", "true", `"nope"`, []string{}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newIngestRepo()
			svc := NewService(repo, WithBaseURL("https://videos.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
			body := withTag(withSensitive(createActivity("https://remote.example/act/f"+string(rune('a'+i)), remoteChan, remoteVideo, remoteChan, "Flagged"), tc.sensitive), tc.tag)
			if err := svc.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
				t.Fatalf("HandleInbox: %v", err)
			}
			rv, ok := repo.remoteVideos[remoteVideo]
			if !ok {
				t.Fatal("remote video not stored")
			}
			got := rv.params.SensitiveFlags
			if len(got) == 0 {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("flags = %v, want %v", got, tc.want)
			}
		})
	}
}

// The round trip: what vidra emits, vidra ingests back unchanged.
func TestSensitiveFlagsRoundTrip(t *testing.T) {
	out := NewService(newIngestRepo(), WithBaseURL("https://videos.example"))
	obj := out.videoObjectFromOutbox(context.Background(), "https://videos.example/channels/c",
		sqlcgen.ListChannelOutboxVideosRow{ID: uuid.New(), Title: "T", IsSensitive: true, SensitiveFlags: []string{"violent", "shocking", "sex"}})
	tagJSON, _ := json.Marshal(obj["tag"])
	repo := newIngestRepo()
	in := NewService(repo, WithBaseURL("https://remote.example"), WithFollowEdgeChecker(edgesFor(remoteChan)))
	body := withTag(withSensitive(createActivity("https://remote.example/act/rt", remoteChan, remoteVideo, remoteChan, "Flagged"), "true"), string(tagJSON))
	if err := in.HandleInbox(context.Background(), remoteChan, []byte(body)); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if got, want := repo.remoteVideos[remoteVideo].params.SensitiveFlags, []string{"violent", "shocking", "sex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("round trip flags = %v, want %v", got, want)
	}
}
