package searchclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The internal-auth v1 protocol (MASTER-PLAN §1.4) has two independent
// implementations in two repositories: this client signs, vidra-search's
// middleware verifies. Nothing in either repo's build fails when they drift —
// the symptom is every internal call 401ing in production, after a deploy, on a
// path neither test suite exercises.
//
// testdata/internal_auth_vectors.json is the contract between them: a byte-
// identical copy of vidra-search's own testdata file (the verifier is the
// canonical generator). Both sides assert against it, so a change to either
// signer has to break its own test before it can break the pair.
//
// TWIN: vidra-search internal/api/testdata/internal_auth_vectors.json — the two
// copies must stay byte-identical; copy it, never re-derive it.

type authVector struct {
	Name           string `json:"name"`
	Note           string `json:"note"`
	Secret         string `json:"secret"`
	TS             int64  `json:"ts"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	ExpectedSig    string `json:"expected_sig"`
	ExpectedHeader string `json:"expected_header"`
}

func loadAuthVectors(t *testing.T) []authVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/internal_auth_vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var doc struct {
		Protocol string       `json:"protocol"`
		Vectors  []authVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if doc.Protocol != "v1" {
		t.Fatalf("vectors declare protocol %q, but this client only speaks v1", doc.Protocol)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("no vectors in testdata/internal_auth_vectors.json")
	}
	return doc.Vectors
}

// TestAuthHeaderMatchesGoldenVectors pins this client's signing against every
// vector vidra-search's verifier is pinned to. The clock is fixed per vector, so
// the whole header — "v1:<ts>:<sig>" — is compared, not just the digest.
func TestAuthHeaderMatchesGoldenVectors(t *testing.T) {
	for _, v := range loadAuthVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			c := New("http://search.invalid", v.Secret,
				WithClock(func() time.Time { return time.Unix(v.TS, 0) }))
			got := c.authHeader(v.Method, v.Path)
			if got != v.ExpectedHeader {
				t.Errorf("authHeader(%q, %q) = %q, want %q\nvector note: %s",
					v.Method, v.Path, got, v.ExpectedHeader, v.Note)
			}
		})
	}
}

// TestDeleteUserHistoryQuerySignsTheDecodedPath is the vector that catches the
// mistake the protocol is most exposed to. The one endpoint embedding
// caller-supplied text in its path escapes it on the wire and signs it
// unescaped, because vidra-search verifies over r.URL.Path — Go's DECODED form.
// Signing the escaped form instead produces a header that is well-formed,
// plausible, and rejected 100% of the time.
//
// It drives the real DeleteUserHistoryQuery against an httptest server and
// compares the header it actually sent to the golden value, so the wire/sign
// split in doPaths is covered, not just authHeader in isolation.
func TestDeleteUserHistoryQuerySignsTheDecodedPath(t *testing.T) {
	userID := uuid.MustParse("3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	for _, v := range loadAuthVectors(t) {
		if v.Name != "delete_history_entry_with_space" && v.Name != "delete_history_entry_cjk" {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			// The query is whatever the vector's path carries after the prefix.
			prefix := "/internal/v1/users/" + userID.String() + "/search-history/"
			query, ok := strings.CutPrefix(v.Path, prefix)
			if !ok {
				t.Fatalf("vector path %q does not start with %q", v.Path, prefix)
			}

			var gotHeader, gotDecodedPath, gotEscapedPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHeader = r.Header.Get(authHeaderName)
				gotDecodedPath = r.URL.Path
				gotEscapedPath = r.URL.EscapedPath()
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			c := New(srv.URL, v.Secret, WithClock(func() time.Time { return time.Unix(v.TS, 0) }))
			if err := c.DeleteUserHistoryQuery(t.Context(), userID, query); err != nil {
				t.Fatalf("DeleteUserHistoryQuery: %v", err)
			}
			if gotHeader != v.ExpectedHeader {
				t.Errorf("sent %s = %q, want %q", authHeaderName, gotHeader, v.ExpectedHeader)
			}
			// The server sees the decoded path — which is exactly what was signed.
			if gotDecodedPath != v.Path {
				t.Errorf("server saw r.URL.Path = %q, want %q", gotDecodedPath, v.Path)
			}
			if want := prefix + url.PathEscape(query); gotEscapedPath != want {
				t.Errorf("wire path = %q, want the escaped form %q", gotEscapedPath, want)
			}
		})
	}
}
