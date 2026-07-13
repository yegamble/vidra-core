package searchclient

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchevents"
)

const testSecret = "test-internal-secret-test-internal-secret"

// verifyHMAC recomputes the v1 auth header the way vidra-search will, returning
// whether it is valid for the given method+path.
func verifyHMAC(header, method, path string) bool {
	parts := strings.SplitN(header, ":", 3)
	if len(parts) != 3 || parts[0] != "v1" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(parts[1] + "\n" + method + "\n" + path))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[2]), []byte(want))
}

func TestSuggestionsSuccessAndHMAC(t *testing.T) {
	var sawAuth, sawCorrelation bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path)
		sawCorrelation = r.Header.Get("X-Correlation-ID") == "corr-123"
		if r.URL.Query().Get("hide_sensitive") != "true" {
			t.Errorf("hide_sensitive not propagated: %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(SuggestionsResponse{
			Query: "go", Suggestions: []Suggestion{{Text: "golang", Type: "query"}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, testSecret)
	// Attach a correlation id the way the httpapi middleware does.
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{CorrelationID: "corr-123"})
	out, err := c.Suggestions(ctx, SuggestParams{Query: "go", Limit: 10, HideSensitive: true})
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if len(out.Suggestions) != 1 || out.Suggestions[0].Text != "golang" {
		t.Fatalf("suggestions = %+v", out.Suggestions)
	}
	if !sawAuth {
		t.Error("HMAC auth header was not valid")
	}
	if !sawCorrelation {
		t.Error("correlation id not propagated")
	}
}

func TestSearchReturnsIDs(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(SearchResponse{Query: "q", IDs: []ScoredID{{VideoID: id, Score: 1.5}}})
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	out, err := c.Search(context.Background(), SearchParams{Query: "q", Limit: 20, Mode: "simple"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.IDs) != 1 || out.IDs[0].VideoID != id {
		t.Fatalf("ids = %+v", out.IDs)
	}
}

func TestServerErrorReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "go", Limit: 5}); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestTimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(SuggestionsResponse{})
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret, WithTimeouts(15*time.Millisecond, 0, 0))
	if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "go", Limit: 5}); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestBreakerOpensAndRecovers(t *testing.T) {
	fail := true
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(SuggestionsResponse{Query: "ok"})
	}))
	defer srv.Close()

	now := time.Now()
	c := New(srv.URL, testSecret, WithClock(func() time.Time { return now }))

	// 5 failures open the breaker.
	for i := 0; i < breakerThreshold; i++ {
		if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "x"}); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	hitsAtOpen := hits
	// Next call fails fast (breaker open) without touching the server.
	if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "x"}); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if hits != hitsAtOpen {
		t.Errorf("server hit while breaker open (hits %d -> %d)", hitsAtOpen, hits)
	}

	// Advance past the cooldown and let the server recover: the half-open probe
	// succeeds and closes the breaker.
	fail = false
	now = now.Add(breakerCooldown + time.Second)
	if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "x"}); err != nil {
		t.Fatalf("half-open probe should succeed: %v", err)
	}
	if _, err := c.Suggestions(context.Background(), SuggestParams{Query: "x"}); err != nil {
		t.Fatalf("breaker should be closed: %v", err)
	}
}

func TestSendEventsRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var batch searchevents.EventBatch
		_ = json.NewDecoder(r.Body).Decode(&batch)
		_ = json.NewEncoder(w).Encode(searchevents.EventBatchResult{Accepted: len(batch.Events)})
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	res, err := c.SendEvents(context.Background(), searchevents.EventBatch{
		Events: []searchevents.Envelope{{EventID: uuid.New(), Type: "video.upsert"}},
	})
	if err != nil {
		t.Fatalf("send events: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", res.Accepted)
	}
}

func TestDeleteUserHistoryQueryPathEscaped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	uid := uuid.New()
	if err := c.DeleteUserHistoryQuery(context.Background(), uid, "hello world/slash"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(gotPath, "hello%20world%2Fslash") {
		t.Errorf("path not escaped: %q", gotPath)
	}
}

// TestDeleteUserHistoryQueryHMACSignsDecodedPath pins the HMAC contract for the
// one endpoint whose wire path carries percent-escapes: vidra-search verifies
// the signature over ts+"\n"+METHOD+"\n"+r.URL.Path — Go's DECODED path — so
// the client must sign the unescaped form while sending the escaped form on the
// wire. The fake server below IS the authoritative server-side recipe: it
// recomputes over the decoded r.URL.Path and 401s a mismatch.
func TestDeleteUserHistoryQueryHMACSignsDecodedPath(t *testing.T) {
	for _, query := range []string{
		"hello world",    // space → %20 on the wire
		"go/concurrency", // slash → %2F on the wire
		"こんにちは 世界",       // CJK + space → multi-byte escapes on the wire
	} {
		t.Run(query, func(t *testing.T) {
			var validated bool
			var wirePath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wirePath = r.URL.EscapedPath()
				// Server-side recipe: recompute over the DECODED r.URL.Path.
				validated = verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path)
				if !validated {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			c := New(srv.URL, testSecret)
			uid := uuid.New()
			if err := c.DeleteUserHistoryQuery(context.Background(), uid, query); err != nil {
				t.Fatalf("delete %q: %v", query, err)
			}
			if !validated {
				t.Fatalf("server-side decoded-path HMAC recipe rejected the signature for %q", query)
			}
			// The wire path must be the ESCAPED form, not the raw query.
			wantEscaped := "/internal/v1/users/" + uid.String() + "/search-history/" + url.PathEscape(query)
			if wirePath != wantEscaped {
				t.Errorf("wire path = %q, want %q", wirePath, wantEscaped)
			}
		})
	}
}
