package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/labstack/echo/v4"
)

// pageFor drives parsePage through a real echo context so the test exercises
// the same query parsing production does.
func pageFor(query string, def, max int) pageParams {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	return parsePage(e.NewContext(req, httptest.NewRecorder()), def, max)
}

// TestParsePageClamps pins the semantics every list endpoint inherited from the
// blocks this helper replaced: values are CLAMPED, never rejected. A client
// sending limit=500 today receives the first `max` rows; turning that into a
// 422 would break it, which is why parsePage returns no error at all.
func TestParsePageClamps(t *testing.T) {
	for _, tc := range []struct {
		name          string
		query         string
		limit, offset int
		def, max      int
	}{
		{name: "absent uses the endpoint default", query: "", limit: 20, offset: 0, def: 20, max: 100},
		{name: "in range is honoured verbatim", query: "limit=37&offset=5", limit: 37, offset: 5, def: 20, max: 100},
		{name: "above the ceiling clamps down", query: "limit=500", limit: 100, offset: 0, def: 20, max: 100},
		{name: "zero clamps up to one", query: "limit=0", limit: 1, offset: 0, def: 20, max: 100},
		{name: "negative limit clamps up to one", query: "limit=-3", limit: 1, offset: 0, def: 20, max: 100},
		{name: "negative offset floors at zero", query: "offset=-9", limit: 20, offset: 0, def: 20, max: 100},
		{name: "malformed falls back to the default", query: "limit=abc&offset=xyz", limit: 20, offset: 0, def: 20, max: 100},
		{name: "each endpoint keeps its own bounds", query: "limit=150", limit: 150, offset: 0, def: 50, max: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pageFor(tc.query, tc.def, tc.max)
			if got.Limit != tc.limit || got.Offset != tc.offset {
				t.Errorf("parsePage(%q) = %+v, want {Limit:%d Offset:%d}", tc.query, got, tc.limit, tc.offset)
			}
		})
	}
}

// TestParsePageAcceptsAnyLimitInRange guards the contract explicitly: the API
// takes the whole range, not the option set the UI happens to offer. A picker
// showing 5/10/20/50/100 must never become the server's validation rule.
func TestParsePageAcceptsAnyLimitInRange(t *testing.T) {
	for n := 1; n <= 100; n++ {
		if got := pageFor("limit="+strconv.Itoa(n), 20, 100); got.Limit != n {
			t.Fatalf("limit=%d accepted as %d, want %d — the contract is a range, not a fixed option set", n, got.Limit, n)
		}
	}
}

// TestPageMetaWireFormat proves the shared envelope serialises FLAT with the
// pre-existing field names. Embedding pageMeta rather than naming it is what
// keeps limit/offset at the top level; a named field would nest them and break
// every client at once.
func TestPageMetaWireFormat(t *testing.T) {
	type sample struct {
		Items []string `json:"items"`
		pageMeta
	}
	body, err := json.Marshal(sample{
		Items:    []string{"a"},
		pageMeta: pageParams{Limit: 20, Offset: 40}.meta(137),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key, want := range map[string]float64{"total": 137, "limit": 20, "offset": 40} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("%s missing from %s — the envelope must stay flat", key, body)
		}
		if v.(float64) != want {
			t.Errorf("%s = %v, want %v", key, v, want)
		}
	}
	if _, nested := got["pageMeta"]; nested {
		t.Errorf("pageMeta serialised as a nested object: %s", body)
	}
}
