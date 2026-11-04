package searchclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/observability"
)

func catalogFrozenBody(id uuid.UUID) []byte {
	// Wire identity fixture only; receiver business validation is tested separately.
	return []byte(` { "events" : [{"event_id":"` + id.String() + `","type":"catalog.snapshot.end","occurred_at":"2026-11-03T12:00:00Z","schema_version":2,"payload":{"literal":"<>&","counter":9007199254740993}}] } `)
}

func TestCatalogEpochTransportUsesExact204AndExplicitPredecessor(t *testing.T) {
	epoch, predecessor := uuid.New(), uuid.New()
	for _, status := range []int{204, 200, 409} {
		for _, expected := range []*uuid.UUID{nil, &predecessor} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Scope    string     `json:"scope"`
					Epoch    uuid.UUID  `json:"epoch"`
					Expected *uuid.UUID `json:"expected_epoch"`
				}
				raw, _ := io.ReadAll(r.Body)
				if json.Unmarshal(raw, &body) != nil || body.Scope != "local" || body.Epoch != epoch ||
					(body.Expected == nil) != (expected == nil) || expected != nil && *body.Expected != *expected ||
					!bytes.Contains(raw, []byte(`"expected_epoch":`)) || r.Method != "PUT" || r.URL.Path != "/internal/v2/catalog/epoch" ||
					!verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path) {
					t.Errorf("invalid epoch request: %s %s %s", r.Method, r.URL, raw)
				}
				w.WriteHeader(status)
			}))
			err := New(srv.URL, testSecret).SetCatalogEpoch(context.Background(), epoch, expected)
			srv.Close()
			if (err == nil) != (status == 204) {
				t.Fatalf("epoch status%d: %v", status, err)
			}
		}
	}
	c := New("http://127.0.0.1:1", testSecret)
	if c.SetCatalogEpoch(context.Background(), uuid.Nil, nil) == nil || c.SetCatalogEpoch(context.Background(), epoch, &uuid.Nil) == nil {
		t.Fatal("zero epoch/predecessor accepted")
	}
}

func TestCatalogFrozenTransportPreservesBytesAndValidatesBoundedReceipts(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	body := catalogFrozenBody(id)
	ok := `{"accepted":1,"duplicates":0,"failed":[]}`
	failure := `{"accepted":0,"duplicates":0,"failed":[{"event_id":"` + id.String() + `","code":"catalog_conflict","message":"stale"}]}`
	for _, tc := range []struct {
		status int
		reply  string
		valid  bool
	}{
		{200, ok, true}, {200, failure, true}, {204, "", false}, {404, `{}`, false}, {503, `{}`, false},
		{200, strings.Replace(ok, `"accepted":1,"duplicates":0`, `"accepted":0,"duplicates":1`, 1), true},
		{200, `{}`, false}, {200, `{"accepted":1,"duplicates":0,"failed":null}`, false},
		{200, strings.Replace(ok, `"accepted":1`, `"accepted":1,"Accepted":1`, 1), false},
		{200, strings.Replace(ok, `"accepted":1`, `"accepted":1,"accepted":1`, 1), false},
		{200, strings.Replace(ok, `"accepted":1`, `"accepted":-1`, 1), false},
		{200, strings.Replace(ok, `"accepted":1`, `"accepted":0`, 1), false},
		{200, strings.Replace(failure, id.String(), uuid.NewString(), 1), false},
		{200, strings.Replace(failure, id.String(), strings.ToUpper(id.String()), 1), false},
		{200, strings.Replace(failure, "catalog_conflict", "", 1), false},
		{200, strings.Replace(failure, `"code":`, `"Code":`, 1), false},
		{200, strings.Replace(failure, `"code":`, `"code":"other","code":`, 1), false},
		{200, ok + `{}`, false},
		{200, ok + strings.Repeat(" ", catalog.MaxRequestBytes-len(ok)), true},
		{200, ok + strings.Repeat(" ", catalog.MaxRequestBytes-len(ok)+1), false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Equal(raw, body) || r.Method != "POST" || r.URL.Path != "/internal/v2/catalog/events" ||
				!verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path) || r.Header.Get("X-Correlation-ID") != "catalog-send" {
				t.Errorf("frozen/authenticated request changed: %s %s", r.Method, r.URL)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.reply))
		}))
		ctx := observability.ContextWithCorrelation(context.Background(), observability.Correlation{CorrelationID: "catalog-send"})
		_, err := New(srv.URL, testSecret).SendCatalogBody(ctx, body)
		srv.Close()
		if (err == nil) != tc.valid {
			t.Fatalf("receipt valid%v status%d prefix%.100s: %v", tc.valid, tc.status, tc.reply, err)
		}
	}
	// Exact request limit is accepted verbatim; +1 and empty reject locally.
	body = append(body, bytes.Repeat([]byte(" "), catalog.MaxRequestBytes-len(body))...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Equal(raw, body) {
			t.Error("exact-limit frozen bytes changed")
		}
		_, _ = w.Write([]byte(ok))
	}))
	defer srv.Close()
	c := New(srv.URL, testSecret)
	if _, err := c.SendCatalogBody(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendCatalogBody(context.Background(), append(bytes.Clone(body), ' ')); err == nil {
		t.Fatal("over-limit request accepted")
	}
	if _, err := c.SendCatalogBody(context.Background(), nil); err == nil {
		t.Fatal("empty frozen request accepted")
	}
	duplicateRequest := []byte(`{"events":[{"event_id":"` + id.String() + `"},{"event_id":"` + id.String() + `"}]}`)
	if _, err := c.SendCatalogBody(context.Background(), duplicateRequest); err == nil {
		t.Fatal("duplicate request identities accepted")
	}
	entry := `{"event_id":"` + id.String() + `","code":"catalog_conflict","message":"stale"}`
	duplicateFailures := []byte(`{"accepted":0,"duplicates":0,"failed":[` + entry + `,` + entry + `]}`)
	if _, err := catalog.ParseEventReceipt(duplicateFailures, []uuid.UUID{id, uuid.New()}); err == nil {
		t.Fatal("duplicate failure identities with valid count accepted")
	}
}

func TestCatalogWriteTransportHonorsCancellationAndDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	for _, epochCall := range []bool{false, true} {
		c := New(srv.URL, testSecret)
		c.timeouts[groupCatalog] = 15 * time.Millisecond
		call := func(ctx context.Context) error {
			if epochCall {
				return c.SetCatalogEpoch(ctx, uuid.New(), nil)
			}
			_, err := c.SendCatalogBody(ctx, catalogFrozenBody(uuid.New()))
			return err
		}
		if err := call(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("write deadline ignored: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := call(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("write cancellation ignored: %v", err)
		}
	}
}
