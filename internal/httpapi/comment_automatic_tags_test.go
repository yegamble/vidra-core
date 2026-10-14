package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// TestCommentAutomaticTagsAreExposedToStaffAndFilterable (0177): a comment
// carrying a URL is tagged "external-link", one matching an instance
// watched-word list carries that LIST's name (never the matched word), an edit
// recomputes the set, ?auto_tag= filters the staff list with a matching total,
// and a non-staff caller cannot read the list at all.
func TestCommentAutomaticTagsAreExposedToStaffAndFilterable(t *testing.T) {
	c := newApprovalCast(t)
	vid := createPublishedVideo(t, c.srv, c.bob, "bob", `{"title":"open","privacy":"public"}`)
	comments := "/api/v1/videos/" + vid + "/comments"
	if rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"secretterm"}`, c.ada); rec.Code != http.StatusCreated {
		t.Fatalf("add word = %d", rec.Code)
	}
	post := func(body string) commentView {
		t.Helper()
		rec := sendJSONAuth(c.srv, http.MethodPost, comments, `{"body":"`+body+`"}`, c.eve)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post = %d; %s", rec.Code, rec.Body.String())
		}
		var cv commentView
		_ = json.Unmarshal(rec.Body.Bytes(), &cv)
		return cv
	}
	linked := post("see https://example.com/x for more")
	post("contains secretterm only")
	post("plain words")

	type row struct {
		ID   string   `json:"id"`
		Tags []string `json:"automatic_tags"`
	}
	list := func(q, tok string) (int, []row, int) {
		t.Helper()
		rec := getWithAuth(c.srv, "/api/v1/admin/comments"+q, tok)
		var out struct {
			Total    int   `json:"total"`
			Comments []row `json:"comments"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Comments, out.Total
	}

	if code, _, _ := list("", c.eve); code != http.StatusForbidden {
		t.Fatalf("non-staff list = %d, want 403", code)
	}
	_, all, _ := list("", c.ada)
	if len(all) != 3 {
		t.Fatalf("staff list = %d rows, want 3", len(all))
	}
	for _, r := range all {
		if r.Tags == nil {
			t.Errorf("row %s: automatic_tags null, want []", r.ID)
		}
		if slices.Contains(r.Tags, "secretterm") {
			t.Errorf("matched word leaked as a tag: %v", r.Tags)
		}
	}
	_, got, total := list("?auto_tag=external-link", c.ada)
	if len(got) != 1 || total != 1 || got[0].ID != linked.ID || !slices.Equal(got[0].Tags, []string{"external-link"}) {
		t.Fatalf("external-link filter = %+v total=%d", got, total)
	}
	_, got, total = list("?auto_tag=Default", c.ada)
	if len(got) != 1 || total != 1 || !slices.Equal(got[0].Tags, []string{"Default"}) {
		t.Fatalf("list-name filter = %+v total=%d", got, total)
	}

	// An edit recomputes: dropping the URL drops the tag.
	if rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+linked.ID, `{"body":"no link now"}`, c.eve); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d; %s", rec.Code, rec.Body.String())
	}
	if _, got, total = list("?auto_tag=external-link", c.ada); len(got) != 0 || total != 0 {
		t.Fatalf("after edit, external-link filter = %+v total=%d, want none", got, total)
	}
}

// A list may not take the built-in tag's name, on create or rename.
func TestWordListNameCannotBeTheBuiltInTag(t *testing.T) {
	for _, n := range []string{"external-link", " External-Link "} {
		if fe := (createWordListRequest{Name: n}).Validate(); len(fe) != 1 || fe[0].Field != "name" {
			t.Errorf("create %q: %v, want one name error", n, fe)
		}
		if fe := (updateWordListRequest{Name: &n}).Validate(); len(fe) != 1 || fe[0].Field != "name" {
			t.Errorf("rename %q: %v, want one name error", n, fe)
		}
	}
	if fe := (createWordListRequest{Name: "external links"}).Validate(); len(fe) != 0 {
		t.Errorf("a different name must pass: %v", fe)
	}
}
