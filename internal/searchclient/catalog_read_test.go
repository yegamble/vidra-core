package searchclient

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/observability"
)

func catalogReadCalls() []func(context.Context, *Client, catalog.Identity, string) error {
	return []func(context.Context, *Client, catalog.Identity, string) error{
		func(ctx context.Context, c *Client, fence catalog.Identity, session string) error {
			_, err := c.Suggestions(ctx, SuggestParams{CatalogFence: fence, SessionID: session})
			return err
		},
		func(ctx context.Context, c *Client, fence catalog.Identity, session string) error {
			_, err := c.Search(ctx, SearchParams{CatalogFence: fence, SessionID: session})
			return err
		},
		func(ctx context.Context, c *Client, fence catalog.Identity, session string) error {
			_, err := c.RecommendationsHome(ctx, RecsParams{CatalogFence: fence, SessionID: session})
			return err
		},
		func(ctx context.Context, c *Client, fence catalog.Identity, session string) error {
			_, err := c.RecommendationsRelated(ctx, RecsParams{CatalogFence: fence, SessionID: session})
			return err
		},
	}
}

func TestCatalogReadFenceHeadersAndLegacyIsolation(t *testing.T) {
	requests := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path) || r.Header.Get("X-Correlation-ID") != "read-fence" {
			t.Error("catalog read lost method, HMAC or correlation")
		}
		requests <- r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{CorrelationID: "read-fence"})
	fence := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: math.MaxInt64}
	for _, call := range catalogReadCalls() {
		for _, id := range []catalog.Identity{fence, {}} {
			if err := call(ctx, c, id, ""); err != nil {
				t.Fatal(err)
			}
			h := <-requests
			if id == (catalog.Identity{}) {
				if h.Get("X-Vidra-Catalog-Epoch") != "" || h.Get("X-Vidra-Catalog-Generation") != "" {
					t.Error("legacy read inherited another request's fence")
				}
			} else if h.Get("X-Vidra-Catalog-Epoch") != id.Epoch.String() || h.Get("X-Vidra-Catalog-Generation") != "9223372036854775807" {
				t.Error("catalog read omitted or rounded the verified fence")
			}
		}
	}
	if _, err := c.GetUserHistory(ctx, HistoryParams{UserID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	if h := <-requests; h.Get("X-Vidra-Catalog-Epoch") != "" || h.Get("X-Vidra-Catalog-Generation") != "" {
		t.Error("history request inherited a catalog fence")
	}
}

func TestCatalogReadFenceRejectsPartialAndForeignBeforeHTTP(t *testing.T) {
	requests := make(chan bool, 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	for _, fence := range []catalog.Identity{
		{Scope: catalog.ScopeLocal},
		{Epoch: uuid.New(), Generation: 1},
		{Scope: catalog.ScopeLocal, Generation: 1},
		{Scope: catalog.ScopeLocal, Epoch: uuid.New()},
		{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: -1},
		{Scope: "remote", Epoch: uuid.New(), Generation: 1},
	} {
		for _, call := range catalogReadCalls() {
			if err := call(context.Background(), c, fence, ""); err == nil {
				t.Errorf("invalid fence accepted: %+v", fence)
			}
		}
	}
	if len(requests) != 0 {
		t.Fatal("invalid identity reached HTTP")
	}
}

func TestCatalogReadFenceConcurrentRequestsKeepOwnValues(t *testing.T) {
	first := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: 9007199254740993}
	second := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: 2}
	entered, release := make(chan bool, 2), make(chan bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- true
		<-release
		want := first
		if r.URL.Query().Get("session_id") == "second" {
			want = second
		}
		if r.Header.Get("X-Vidra-Catalog-Epoch") != want.Epoch.String() || r.Header.Get("X-Vidra-Catalog-Generation") != strconv.FormatInt(int64(want.Generation), 10) {
			t.Error("concurrent request used another request's fence")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret, WithTimeouts(time.Second, time.Second, time.Second))
	var wg sync.WaitGroup
	for i, fence := range []catalog.Identity{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := "first"
			if i == 1 {
				session = "second"
			}
			if _, err := c.Search(context.Background(), SearchParams{CatalogFence: fence, SessionID: session}); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("both concurrent requests did not arrive")
		}
	}
	close(release)
	wg.Wait()
}

func TestCatalogReadFenceServiceFailureRemainsCallerFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	fence := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: 1}
	for _, call := range catalogReadCalls() {
		if err := call(context.Background(), New(srv.URL, testSecret), fence, ""); err == nil {
			t.Error("receiver rejection was treated as successful read authority")
		}
	}
}

func TestCatalogReadFenceKeepsReadDeadlinesAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	fence := catalog.Identity{Scope: catalog.ScopeLocal, Epoch: uuid.New(), Generation: 1}
	for _, call := range catalogReadCalls() {
		c := New(srv.URL, testSecret, WithTimeouts(15*time.Millisecond, 15*time.Millisecond, 15*time.Millisecond))
		start := time.Now()
		if err := call(context.Background(), c, fence, ""); err == nil || time.Since(start) > time.Second {
			t.Error("fenced read ignored its endpoint deadline")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := call(ctx, c, fence, ""); err == nil {
			t.Error("fenced read ignored caller cancellation")
		}
	}
}
