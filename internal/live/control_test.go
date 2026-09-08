package live

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The drop contract against a fake control server, which is the only way to test
// it short of an nginx: A26 read this off the wire and found the shipped
// assumption wrong in the one way that mattered — a drop that matched nothing
// answers 200 with a body of `0`, not the 404 the code was looking for, so every
// drop looked like a disconnect.

// controlServer stands in for nginx-rtmp's control module, answering whatever
// the test pins.
func controlServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDropPublisherReadsTheCount is SC1: the body is the answer, not the status.
func TestDropPublisherReadsTheCount(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantCount int
		wantErr   error
	}{
		// The A26 finding. 200/`0` is the module saying it closed nothing —
		// a hook-only phantom session, a publisher who already left, or (as
		// measured, twelve times in a row) a drop that reached a different
		// nginx worker than the one holding the socket.
		{name: "nothing matched", status: 200, body: "0\n", wantCount: 0, wantErr: ErrIngestNoPublisher},
		{name: "one publisher", status: 200, body: "1\n", wantCount: 1},
		{name: "several", status: 200, body: "3", wantCount: 3},
		// Documented by the module, never sent by this build, and still the
		// same outcome.
		{name: "404", status: 404, body: "", wantCount: 0, wantErr: ErrIngestNoPublisher},
		// A 200 that is not a count is not a disconnect: it is what a captive
		// portal, an error page or a misrouted proxy answers, and none of them
		// dropped anybody.
		{name: "html", status: 200, body: "<html>hello</html>", wantCount: 0, wantErr: ErrIngestControlUnavailable},
		{name: "empty", status: 200, body: "", wantCount: 0, wantErr: ErrIngestControlUnavailable},
		{name: "401", status: 401, body: "", wantCount: 0, wantErr: ErrIngestControlUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := controlServer(t, tc.status, tc.body)
			c := NewHTTPIngestController(srv.URL, "not-a-real-secret")
			n, err := c.DropPublisher(context.Background(), "8a0f1c2e-0000-4000-8000-000000000001")
			if n != tc.wantCount {
				t.Errorf("count = %d, want %d", n, tc.wantCount)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestDropPublisherRequestShape pins what actually goes on the wire: the ingest
// application (never `hls`, which would tear down the packaging push and leave
// the streamer connected), the stream ID as the name (never the raw key — the
// on-publish rename exists so the credential never has to leave the database),
// and the shared secret.
func TestDropPublisherRequestShape(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		_, _ = w.Write([]byte("1"))
	}))
	defer srv.Close()

	const id = "8a0f1c2e-0000-4000-8000-000000000002"
	c := NewHTTPIngestController(srv.URL+"/", "not-a-real-secret")
	if _, err := c.DropPublisher(context.Background(), id); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got == nil {
		t.Fatal("the control server was never called")
	}
	if got.URL.Path != "/control/drop/publisher" {
		t.Errorf("path = %q, want /control/drop/publisher", got.URL.Path)
	}
	if app := got.URL.Query().Get("app"); app != "live" {
		t.Errorf("app = %q, want live — dropping `hls` kills the packaging push and leaves the publisher connected", app)
	}
	if name := got.URL.Query().Get("name"); name != id {
		t.Errorf("name = %q, want the stream id %q", name, id)
	}
	if got.Header.Get(ingestSecretHeader) != "not-a-real-secret" {
		t.Errorf("%s = %q, want the shared secret", ingestSecretHeader, got.Header.Get(ingestSecretHeader))
	}
}

// TestProbeUsesStat: the readiness probe hits the stat page and never a control
// command — a probe that runs on every readiness tick must have no side effect,
// and a drop with a dummy name is a mutation aimed at whatever is named that way.
func TestProbeUsesStat(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte("<rtmp></rtmp>"))
	}))
	defer srv.Close()
	if err := NewHTTPIngestController(srv.URL, "not-a-real-secret").Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if path != "/stat" {
		t.Errorf("probe path = %q, want /stat", path)
	}
}

// TestDropPublisherBodyIsBounded: the count is a handful of bytes, and a
// misbehaving endpoint must not be able to stream into core's memory. A huge
// body is read up to the cap and then fails the parse rather than being trusted.
func TestDropPublisherBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("9", 64<<10)))
	}))
	defer srv.Close()
	n, err := NewHTTPIngestController(srv.URL, "not-a-real-secret").DropPublisher(context.Background(), "x")
	if !errors.Is(err, ErrIngestControlUnavailable) || n != 0 {
		t.Errorf("drop = (%d, %v), want (0, ErrIngestControlUnavailable)", n, err)
	}
}
