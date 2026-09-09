package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A CIDv1 the validator accepts. Fake fixture; no bytes behind it anywhere.
const probeCID = "bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy"

func TestGatewayProbeAcceptsAServingGateway(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.Method + " " + r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewGatewayProbe(srv.URL, srv.Client()).Fetch(context.Background(), probeCID); err != nil {
		t.Fatalf("Fetch on a serving gateway: %v", err)
	}
	// The probe must ask for the SAME URL shape delivery mints, or it is health
	// data about a path no viewer ever takes.
	if got := path.Load().(string); got != "HEAD /ipfs/"+probeCID {
		t.Errorf("probe asked for %q, want HEAD /ipfs/<cid>", got)
	}
}

func TestGatewayProbeRejectsAGatewayThatDoesNotServe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such CID", http.StatusNotFound)
	}))
	defer srv.Close()

	err := NewGatewayProbe(srv.URL, srv.Client()).Fetch(context.Background(), probeCID)
	if err == nil {
		t.Fatal("Fetch on a 404ing gateway: want an error")
	}
	// The gateway's own error page is never quoted onto an admin surface.
	if strings.Contains(err.Error(), "no such CID") {
		t.Errorf("probe error quotes the gateway body: %v", err)
	}
}

func TestGatewayProbeRejectsAnUnreachableGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now — the A31 failure, exactly.

	if err := NewGatewayProbe(url, nil).Fetch(context.Background(), probeCID); err == nil {
		t.Fatal("Fetch against a closed gateway: want an error")
	}
}

// A gateway that refuses HEAD but serves GET is healthy: reporting it down would
// take the mirror offline over a verb.
func TestGatewayProbeFallsBackToGETWhenHEADIsRefused(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gets.Add(1)
		if r.Header.Get("Range") == "" {
			t.Errorf("the GET fallback must be Range-limited, not a full transfer")
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	if err := NewGatewayProbe(srv.URL, srv.Client()).Fetch(context.Background(), probeCID); err != nil {
		t.Fatalf("Fetch with HEAD refused: %v", err)
	}
	if gets.Load() != 1 {
		t.Errorf("GET fallback ran %d times, want 1", gets.Load())
	}
}

func TestGatewayProbeRefusesAnInvalidCIDAndAnUnconfiguredGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request should reach the gateway: %s", r.URL.Path)
	}))
	defer srv.Close()

	if err := NewGatewayProbe(srv.URL, srv.Client()).Fetch(context.Background(), "../../etc/passwd"); err == nil {
		t.Error("Fetch with a non-CID: want an error before any request")
	}
	if err := NewGatewayProbe("", nil).Fetch(context.Background(), probeCID); err == nil {
		t.Error("Fetch with no gateway URL: want an error")
	}
}

func TestListPinsReadsTheKeysMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v0/pin/ls") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "recursive" {
			t.Errorf("pin/ls must ask for recursive pins, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"Keys":{"` + probeCID + `":{"Type":"recursive"}}}`))
	}))
	defer srv.Close()

	pins, err := NewKuboClient(srv.URL, srv.Client()).ListPins(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if _, ok := pins[probeCID]; !ok || len(pins) != 1 {
		t.Fatalf("pins = %v, want exactly the one CID", pins)
	}
}

// A pinset larger than the sweep will compare is an ERROR, not a truncated set:
// acting on a partial list would re-add live content and under-report strays.
func TestListPinsRefusesAPinsetLargerThanTheCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Keys":{"` + probeCID + `":{"Type":"recursive"},"bafkreiaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"Type":"recursive"}}}`))
	}))
	defer srv.Close()

	if _, err := NewKuboClient(srv.URL, srv.Client()).ListPins(context.Background(), 1); err == nil {
		t.Fatal("ListPins past the cap: want an error rather than a truncated set")
	}
}

// The distinction ListPins exists for: an unreachable node must be an ERROR, not
// an empty pinset. An empty set would tell the reconcile sweep that every pinned
// row has lost its pin.
func TestListPinsReportsAnUnreachableNodeAsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	pins, err := NewKuboClient(url, nil).ListPins(context.Background(), 10)
	if err == nil {
		t.Fatalf("ListPins against a dead node returned %v, want an error", pins)
	}
}
