package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The RTMP ingest's HTTP control surface — the ONE outbound path core has to the
// media server, and the only way to make a termination reach the publisher.
//
// Why it has to exist at all. Ending a broadcast in Postgres is a SERVER-SIDE
// close: /live/{id}/hls 404s, the stream leaves every listing, and the watch
// page goes dark. It does nothing to the publisher, who stays connected and
// keeps writing segments and a recording to the operator's disk — A26 measured
// exactly that on the duration watchdog, and SweepOverdueLive's own comment
// says so. A moderator ending an instance-damaging broadcast needs the socket
// closed, not just the audience removed.
//
// The contract, in full:
//
//	GET  <base>/control/drop/publisher?app=<app>&name=<stream-id>   → drop
//	GET  <base>/stat                                               → probe
//	both authenticated with the X-Ingest-Secret header, the SAME shared secret
//	the ingest hooks already use (LIVE_INGEST_SECRET).
//
// `name` is the STREAM ID, not the stream key, and that is a property of the
// shipped ingest config rather than a convenience: the on-publish hook renames
// the session to the bare id (deploy/media/nginx.conf.template), so after the
// first frame every session on the `live` application is named by its id. The
// raw key therefore never has to leave the database to terminate a broadcast —
// which is the whole reason the rename exists.
//
// The application dropped is `live`, the INGEST application, never `hls`.
// Dropping `hls` would tear down the loopback packaging push and leave the
// streamer connected and recording — the audience would go dark and the
// publisher would not even notice. Dropping `live` ends the source, which tears
// down the push, which fires the `hls` application's on_publish_done — i.e. the
// ORDINARY stop path, which is what finalises the recording into the replay.
// That is why termination does not run the replay itself.
//
// SSRF posture. This is an operator-configured infrastructure endpoint, the same
// class as SEARCH_SERVICE_URL and IPFS_API_URL, and it is EXPECTED to be a
// private compose address (`http://rtmp:8082`) — running it through the
// public-address guard in internal/urlsafety would reject every correct value.
// What is guarded instead is the thing that could actually be steered: nothing
// request-derived reaches the URL except the two query parameters, and both are
// url.Values-escaped, the app is a package constant, and the name is a UUID's
// string form. The base URL's scheme/host/credentials are validated at BOOT
// (internal/config), so a malformed one fails the process rather than one
// moderator's click.

// ingestAppLive is the nginx-rtmp application a streamer publishes to, and the
// only one a drop is ever aimed at. It matches `application live` in
// deploy/media/nginx.conf.template; changing one without the other silently
// turns every termination into a no-op, which is why the test asserts the
// literal.
const ingestAppLive = "live"

// ingestSecretHeader is the shared-secret header the ingest's control location
// checks, identical to the one its hooks present to the api. One header name in
// one direction each way, so an operator rotating LIVE_INGEST_SECRET rotates
// both halves at once.
const ingestSecretHeader = "X-Ingest-Secret" //nolint:gosec // header NAME, not a credential

// Control timeouts. A drop is a moderator waiting on a click, so it gets a real
// budget; a probe rides /readyz and must never be the slow thing on that page.
const (
	dropTimeout  = 5 * time.Second
	probeTimeout = 3 * time.Second
)

// ErrIngestControlUnavailable means the ingest's control surface did not answer,
// or answered with a failure. It is deliberately DISTINCT from "the stream was
// not terminated": by the time a caller sees it the state flip and the key
// rotation have already landed, so the broadcast is off the air and the
// publisher cannot re-authenticate — what failed is only the disconnect of the
// socket they are already holding.
var ErrIngestControlUnavailable = errors.New("live: ingest control unavailable")

// ErrIngestNoPublisher means the ingest accepted the request and reported that
// there is no publisher under that name. Not a failure: the publisher had
// already gone, which is the outcome a drop was asking for.
var ErrIngestNoPublisher = errors.New("live: no publisher on the ingest")

// IngestController is the ingest control surface as the live service needs it.
// An interface so a termination can be tested against a fake that records the
// call and returns any outcome, with no nginx anywhere — and so an instance
// with no control URL wires nil and degrades rather than failing.
type IngestController interface {
	// DropPublisher disconnects the RTMP publisher of streamName on the ingest
	// application. Returns ErrIngestNoPublisher when there was none, and
	// ErrIngestControlUnavailable when the ingest could not be reached.
	DropPublisher(ctx context.Context, streamName string) error
	// Probe reports whether the ingest is answering at all. nil means alive.
	Probe(ctx context.Context) error
}

// HTTPIngestController talks to nginx-rtmp's control + stat modules over HTTP.
type HTTPIngestController struct {
	base   string
	secret string
	client *http.Client
	app    string
}

// NewHTTPIngestController builds a controller for the ingest at base (e.g.
// "http://rtmp:8082") authenticated with secret. It returns nil when either is
// empty — a nil *HTTPIngestController is a valid IngestController whose calls
// report "not configured", so wiring stays unconditional and an instance
// without a control surface simply degrades.
func NewHTTPIngestController(base, secret string) *HTTPIngestController {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(secret) == "" {
		return nil
	}
	return &HTTPIngestController{
		base:   base,
		secret: secret,
		app:    ingestAppLive,
		// A dedicated client with no redirect following: the control surface
		// answers in one hop, and a 302 from a misconfigured proxy must not
		// carry the shared secret to a third host.
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Configured reports whether this controller can actually reach an ingest. It is
// nil-safe, so callers can ask without a nil check of their own.
func (c *HTTPIngestController) Configured() bool { return c != nil }

// DropPublisher issues nginx-rtmp's drop/publisher control command.
//
// The module answers 200 with the number of dropped connections in the body, and
// 404 when the name matches no publisher. A 404 is reported as
// ErrIngestNoPublisher rather than as an error, because "there is no publisher"
// is the state a drop was trying to reach.
func (c *HTTPIngestController) DropPublisher(ctx context.Context, streamName string) error {
	if c == nil {
		return ErrIngestControlUnavailable
	}
	q := url.Values{}
	q.Set("app", c.app)
	q.Set("name", streamName)
	target := c.base + "/control/drop/publisher?" + q.Encode()

	ctx, cancel := context.WithTimeout(ctx, dropTimeout)
	defer cancel()
	status, err := c.do(ctx, target)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusNotFound:
		return ErrIngestNoPublisher
	case status >= 200 && status < 300:
		return nil
	default:
		return fmt.Errorf("%w: drop answered %d", ErrIngestControlUnavailable, status)
	}
}

// Probe asks the ingest's stat page whether it is alive. Any 2xx is alive; a
// transport failure, a timeout or a non-2xx is not.
//
// It deliberately probes /stat and not the control command with a dummy name: a
// probe that runs on every readiness tick must have no side effect at all, and a
// drop request — even one that matches nothing — is a mutation aimed at whatever
// happens to be named that way.
func (c *HTTPIngestController) Probe(ctx context.Context) error {
	if c == nil {
		return ErrIngestControlUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	status, err := c.do(ctx, c.base+"/stat")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%w: stat answered %d", ErrIngestControlUnavailable, status)
	}
	return nil
}

// do performs one authenticated GET and returns its status code. The body is
// drained and discarded up to a small cap so the connection is reusable without
// letting a misbehaving endpoint stream into core's memory.
func (c *HTTPIngestController) do(ctx context.Context, target string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrIngestControlUnavailable, err)
	}
	req.Header.Set(ingestSecretHeader, c.secret)
	resp, err := c.client.Do(req)
	if err != nil {
		// The error is wrapped, not returned raw: it can carry the control URL,
		// and a control URL in a 5xx body would tell an anonymous caller where
		// the instance's ingest lives.
		return 0, fmt.Errorf("%w: %v", ErrIngestControlUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, nil
}
