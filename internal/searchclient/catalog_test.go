package searchclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/observability"
)

const initialCatalogStatus = `{"protocol_version":2,"supported_scopes":["local"],"supported_event_types":["catalog.snapshot.page","catalog.snapshot.end"],"limits":{"max_request_bytes":1048576,"max_snapshot_bytes":536870912,"max_documents":100000,"max_pages":1024},"scope":"local","epoch":null,"ready":false,"highest_observed_generation":"0","completed_generation":"0","completed_at":null,"completed_manifest":null,"staged_snapshots":0,"oldest_staged_at":null}`

func TestCatalogStatusUsesAuthenticatedLocalCapabilityProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v2/catalog/status" || r.URL.RawQuery != "scope=local" || !verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path) || r.Header.Get("X-Correlation-ID") != "catalog-probe" {
			t.Errorf("incorrect catalog probe %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(initialCatalogStatus))
	}))
	defer srv.Close()
	ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{CorrelationID: "catalog-probe"})
	got, err := New(srv.URL, testSecret).CatalogStatus(ctx)
	if err != nil || got.Ready || got.Epoch != nil || got.HighestObservedGeneration != "0" {
		t.Fatalf("initial receiver status %+v, %v", got, err)
	}
}

func TestCatalogStatusRejectsUnsupportedMalformedAndUnboundedResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		valid  bool
	}{
		"old receiver": {http.StatusNotFound, `{}`, false},
		"outage":       {http.StatusServiceUnavailable, `{}`, false},
		"incomplete":   {http.StatusOK, `{}`, false},
		"trailing":     {http.StatusOK, initialCatalogStatus + `{}`, false},
		"alias":        {http.StatusOK, strings.Replace(initialCatalogStatus, `"ready":false`, `"ready":false,"Ready":false`, 1), false},
		"exact bound":  {http.StatusOK, initialCatalogStatus + strings.Repeat(" ", catalog.MaxRequestBytes-len(initialCatalogStatus)), true},
		"over bound":   {http.StatusOK, initialCatalogStatus + strings.Repeat(" ", catalog.MaxRequestBytes-len(initialCatalogStatus)+1), false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL, testSecret).CatalogStatus(context.Background())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.status == http.StatusNotFound {
				var rejected *HTTPError
				if !errors.As(err, &rejected) || rejected.Status != http.StatusNotFound {
					t.Fatalf("unsupported receiver error = %v", err)
				}
			}
		})
	}
}

func TestCatalogStatusHonorsItsEndpointDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	c.timeouts[groupCatalog] = 15 * time.Millisecond
	if _, err := c.CatalogStatus(context.Background()); err == nil {
		t.Fatal("catalog capability probe ignored its deadline")
	}
}
