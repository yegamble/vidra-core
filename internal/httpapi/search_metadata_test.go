package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/searchclient"
)

func TestSuggestionsUseCanonicalMetadata(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv, _, _, _, repo := videoServerFullWith(t, testConfig(), []Option{WithSearchClient(fake)})
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	publicID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Canonical title","tags":["visible"],"privacy":"public"}`)
	privateID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Private title","tags":["private-tag"],"privacy":"private"}`)
	str := func(v string) *string { return &v }
	fake.suggestResp.Suggestions = []searchclient.Suggestion{
		{Text: "aggregate", Type: "query", ChannelHandle: str("unverified")},
		{Text: "visible", Type: "tag", VideoID: &publicID},
		{Text: "stale-tag", Type: "tag", VideoID: &publicID},
		{Text: "unverified-tag", Type: "tag"},
		{Text: "private-tag", Type: "tag", VideoID: &privateID},
		{Text: "ada", Type: "channel", ChannelHandle: str("ada"), VideoID: &publicID},
		{Text: "Stale channel", Type: "channel", ChannelHandle: str("ada"), VideoID: &publicID},
		{Text: "Wrong channel", Type: "channel", ChannelHandle: str("other"), VideoID: &publicID},
		{Text: "Unverified channel", Type: "channel", ChannelHandle: str("ada")},
		{Text: "Stale title", Type: "query", VideoID: &publicID},
		{Text: "Canonical title", Type: "query", VideoID: &publicID},
		{Text: "visible", Type: "query", VideoID: &publicID}, // Blended aggregate/tag collision.
		{Text: "ada", Type: "query", VideoID: &publicID},     // Blended aggregate/channel collision.
		{Text: "Unknown", Type: "new-unverified-type"},
	}
	for _, tc := range []struct {
		name string
		err  error
		want map[string]bool
	}{
		{"canonical", nil, map[string]bool{"query/aggregate": true, "tag/visible": true, "channel/ada": true, "query/Canonical title": true, "query/visible": true, "query/ada": true}},
		{"query failure", errors.New("canonical query unavailable"), map[string]bool{"query/aggregate": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.hydrateErr = tc.err
			rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=vi", "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d", rec.Code)
			}
			var out suggestionsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Suggestions) != len(tc.want) {
				t.Fatalf("suggestions=%+v, want %v", out.Suggestions, tc.want)
			}
			for _, row := range out.Suggestions {
				if !tc.want[row.Type+"/"+row.Text] {
					t.Errorf("unverified row %+v", row)
				}
				if row.Text != "Canonical title" && row.VideoID != nil {
					t.Error("internal witness escaped into metadata response")
				}
				if row.Type != "channel" && row.ChannelHandle != nil {
					t.Error("unverified channel field escaped into response")
				}
			}
		})
	}
	repo.hydrateErr = nil
	updated := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+publicID, `{"privacy":"private"}`, tok)
	if updated.Code != http.StatusOK {
		t.Fatalf("privacy update=%d: %s", updated.Code, updated.Body.String())
	}
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=vi", "", "")
	var out suggestionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Suggestions) != 1 || out.Suggestions[0].Text != "aggregate" {
		t.Fatalf("fresh canonical read after update=%s, %v", rec.Body.String(), err)
	}
}
