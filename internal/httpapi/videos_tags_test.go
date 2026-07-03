package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestVideoTagsLifecycle proves tags round-trip: normalized on create, exposed
// on the detail view, replaced in full on update (empty slice clears), and
// left untouched by unrelated updates.
func TestVideoTagsLifecycle(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Create with messy tags: lowercased, trimmed, deduped.
	rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"Tagged","privacy":"public","tags":[" Go ","REDIS","go"]}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if want := []string{"go", "redis"}; !reflect.DeepEqual(v.Tags, want) {
		t.Fatalf("create tags = %v, want %v", v.Tags, want)
	}

	// Detail exposes them.
	var detail videoView
	_ = json.Unmarshal(getVideo(srv, v.ID, "").Body.Bytes(), &detail)
	if want := []string{"go", "redis"}; !reflect.DeepEqual(detail.Tags, want) {
		t.Fatalf("detail tags = %v, want %v", detail.Tags, want)
	}

	// An unrelated PATCH leaves tags alone.
	up := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+v.ID, `{"title":"renamed"}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("unrelated patch = %d; body=%s", up.Code, up.Body.String())
	}
	var kept videoView
	_ = json.Unmarshal(up.Body.Bytes(), &kept)
	if want := []string{"go", "redis"}; !reflect.DeepEqual(kept.Tags, want) {
		t.Fatalf("tags after unrelated patch = %v, want %v", kept.Tags, want)
	}

	// PATCH with tags replaces the whole set.
	up = sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+v.ID, `{"tags":["B","a "]}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("replace tags = %d; body=%s", up.Code, up.Body.String())
	}
	var replaced videoView
	_ = json.Unmarshal(up.Body.Bytes(), &replaced)
	if want := []string{"a", "b"}; !reflect.DeepEqual(replaced.Tags, want) {
		t.Fatalf("replaced tags = %v, want %v", replaced.Tags, want)
	}

	// PATCH with an empty array clears them (omitted from the JSON view).
	up = sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+v.ID, `{"tags":[]}`, tok)
	if up.Code != http.StatusOK {
		t.Fatalf("clear tags = %d; body=%s", up.Code, up.Body.String())
	}
	var cleared videoView
	_ = json.Unmarshal(up.Body.Bytes(), &cleared)
	if len(cleared.Tags) != 0 {
		t.Fatalf("cleared tags = %v, want none", cleared.Tags)
	}
}

// TestVideoTagsValidation proves the §18 limits: more than 5 distinct tags or
// a tag longer than 50 chars is a 422 on both create and update. Duplicates
// collapse before the count check.
func TestVideoTagsValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	sixTags := `["a","b","c","d","e","f"]`
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"x","tags":`+sixTags+`}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("six tags create = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	long := strings.Repeat("x", 51)
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"x","tags":["`+long+`"]}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long tag create = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// Duplicates collapse: 6 entries but 5 distinct tags is fine.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos",
		`{"title":"x","tags":["a","A","b","c","d","e"]}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("five-distinct create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	id := createVideo(t, srv, tok, "ada", `{"title":"y"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id,
		`{"tags":`+sixTags+`}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("six tags update = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestFeedTagAndTaxonomyFilters proves GET /videos?tag=&category=&language=
// narrows the feed, that tag matching is case-insensitive (stored lowercased),
// and that unknown taxonomy values are refused with 422 (the frontend
// filter-controls contract).
func TestFeedTagAndTaxonomyFilters(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada",
		`{"title":"go talk","privacy":"public","tags":["go","conference"],"category":"1","language":"en"}`)
	_ = createPublishedVideo(t, srv, tok, "ada",
		`{"title":"cooking","privacy":"public","category":"2","language":"fr"}`)

	feed := func(query string) (int, []string) {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos"+query, nil))
		var body videoFeedResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		titles := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			titles = append(titles, v.Title)
		}
		return rec.Code, titles
	}

	if code, titles := feed(""); code != http.StatusOK || len(titles) != 2 {
		t.Fatalf("unfiltered feed = %d %v, want 200 with 2", code, titles)
	}
	if code, titles := feed("?tag=go"); code != http.StatusOK || len(titles) != 1 || titles[0] != "go talk" {
		t.Errorf("tag filter = %d %v, want [go talk]", code, titles)
	}
	// Uppercase query matches the stored lowercased tag.
	if code, titles := feed("?tag=GO"); code != http.StatusOK || len(titles) != 1 {
		t.Errorf("uppercase tag filter = %d %v, want 1 match", code, titles)
	}
	if code, titles := feed("?tag=nomatch"); code != http.StatusOK || len(titles) != 0 {
		t.Errorf("no-match tag filter = %d %v, want empty", code, titles)
	}
	if code, titles := feed("?category=2"); code != http.StatusOK || len(titles) != 1 || titles[0] != "cooking" {
		t.Errorf("category filter = %d %v, want [cooking]", code, titles)
	}
	if code, titles := feed("?language=en"); code != http.StatusOK || len(titles) != 1 || titles[0] != "go talk" {
		t.Errorf("language filter = %d %v, want [go talk]", code, titles)
	}
	if code, titles := feed("?category=1&language=en&tag=conference"); code != http.StatusOK || len(titles) != 1 {
		t.Errorf("combined filter = %d %v, want 1", code, titles)
	}

	// Unknown taxonomy values and an over-long tag are 422.
	if code, _ := feed("?category=999"); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown category = %d, want 422", code)
	}
	if code, _ := feed("?language=zz"); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown language = %d, want 422", code)
	}
	if code, _ := feed("?tag=" + strings.Repeat("x", 51)); code != http.StatusUnprocessableEntity {
		t.Errorf("over-long tag = %d, want 422", code)
	}
}

// TestSearchMatchesTags proves the public search also matches a video by its
// tags, not just its title.
func TestSearchMatchesTags(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, tok, "ada",
		`{"title":"untitled clip","privacy":"public","tags":["kubernetes"]}`)
	_ = createPublishedVideo(t, srv, tok, "ada", `{"title":"other","privacy":"public"}`)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=Kubernetes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body videoSearchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Videos) != 1 || body.Videos[0].Title != "untitled clip" {
		t.Fatalf("search by tag = %+v, want the tagged video", body.Videos)
	}
}
