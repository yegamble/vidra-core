package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/atproto"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/block"
	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/instancedocs"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/messaging"
	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/mute"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/peertubeimport"
	"github.com/vidra/vidra-core/internal/playersettings"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/qoe"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/rating"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/storagemigration"
	"github.com/vidra/vidra-core/internal/transcode"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/watchword"
)

// contractTestJWTSecret signs the tokens the contract guards mint. It lives here
// because fullRouteOptions builds the issuer that verifies them, and the status
// guard in openapi_status_contract_test.go must sign with the same key.
const contractTestJWTSecret = "contract-test-secret-contract-test-0"

// fullRouteOptions mounts every optional feature so the contract test enumerates
// the complete route surface. The wired dependencies are never invoked — only
// the routing table is inspected — so nil/zero collaborators are fine.
func fullRouteOptions() []Option {
	issuer := auth.NewTokenIssuer(contractTestJWTSecret, "vidra", "vidra", time.Minute)
	return []Option{
		WithAuthService(auth.NewService(contractAuthRepo{}, issuer, time.Hour), time.Minute),
		WithAccountService(account.NewService(nil, nil, nil)),
		WithOAuthService(auth.NewOAuthService(nil, nil, nil)),
		// ATProto identity login: always part of the contract, gated at request
		// time by ATPROTO_LOGIN_ENABLED (see cmd/api — always constructed).
		WithATProtoLoginService(auth.NewATProtoOAuthService(nil, nil, nil)),
		WithChannelService(channel.NewService(nil)),
		WithDonationService(donation.NewService(nil, "vidra.test")),
		WithVideoService(video.NewService(nil, nil)),
		WithCommentService(comment.NewService(nil)),
		WithRatingService(rating.NewService(nil)),
		WithNotificationService(notification.NewService(nil)),
		WithPlayerSettingsService(playersettings.NewService(nil)),
		WithPlaylistService(playlist.NewService(nil)),
		WithModerationService(moderation.NewService(nil)),
		WithMuteService(mute.NewService(nil)),
		WithBlockService(block.NewService(nil)),
		WithWatchWordService(watchword.NewService(nil)),
		WithAdminService(admin.NewService(nil)),
		WithAuditLog(audit.NewService(nil)),
		WithMessagingService(messaging.NewService(nil)),
		WithE2EEService(e2ee.NewService(nil)),
		WithLiveService(live.NewService(nil)),
		WithProfileImageService(profileimage.NewService(nil, nil)),
		WithQuotaService(quota.NewService(nil, 0)),
		WithTranscodeService(transcode.NewService(nil, nil)),
		WithUploadService(upload.NewService(nil, nil)),
		WithVideoImportService(videoimport.NewService(nil, nil, 0)),
		WithChannelSyncService(channelsync.NewService(nil, nil, nil)),
		WithCaptionJobService(captionjob.NewService(nil, nil, nil)),
		WithInstanceModerationService(instancemod.NewService(nil)),
		WithSettingsService(instancesettings.NewService(nil, instancesettings.Defaults{})),
		WithInstanceDocumentsService(instancedocs.NewService(nil)),
		WithRemoteVideoService(remotevideo.NewService(nil, nil)),
		WithMediaGCService(mediagc.NewService(nil, nil)),
		// Storage migration: always part of the contract. cmd/api wires it even
		// without a target backend so the read/cancel surface always exists;
		// Start answers 503 in that configuration.
		WithStorageMigrationService(storagemigration.NewService(nil, nil, nil, storagemigration.Config{})),
		WithJobStatusService(jobstatus.NewService(nil)),
		// Playback QoE: always part of the contract. cmd/api wires it
		// unconditionally (it needs only the database), and the
		// qoe_collection_enabled setting gates collection at request time
		// rather than making the route come and go.
		WithQoEService(qoe.NewService(nil, nil), nil, nil),
		WithPeerTubeImportService(peertubeimport.NewService(nil)),
		// Mounts the REST remote-follow routes. The AP root routes stay excluded
		// from the drift guard: they additionally require cfg.FederationEnabled,
		// which testConfig leaves false.
		WithFederationService(federation.NewService(nil)),
		// Mounts the REST /me/atproto link routes (P10.2). Always part of the
		// contract; ATPROTO_ENABLED only gates them at request time.
		WithATProtoService(atproto.NewService(nil)),
	}
}

// TestOpenAPIContract is the documentation stop guard: it fails the build when
// the routes registered on the Echo router diverge from the operations declared
// in api/openapi.yaml. Add a route without documenting it, or document a path
// with no route behind it, and this test goes red. Keep code and contract in
// lock-step in the same change.
//
// The spec is parsed by indentation (see api/openapi.yaml for the required
// shape) rather than a YAML library, so the test adds no dependency.
func TestOpenAPIContract(t *testing.T) {
	specPath := filepath.Join("..", "..", "api", "openapi.yaml")

	declared := declaredOperations(t, specPath)
	registered := registeredOperations(t)

	for op := range registered {
		if !declared[op] {
			t.Errorf("route %q is registered but NOT documented in api/openapi.yaml — document it in the same change", op)
		}
	}
	for op := range declared {
		if !registered[op] {
			t.Errorf("api/openapi.yaml documents %q but no route is registered — remove it from the spec or restore the route", op)
		}
	}

	if t.Failed() {
		t.Logf("registered routes:\n  %s", strings.Join(sortedKeys(registered), "\n  "))
		t.Logf("documented operations:\n  %s", strings.Join(sortedKeys(declared), "\n  "))
	}
}

// echoParam matches an Echo path parameter (":id") so it can be normalised to
// the OpenAPI form ("{id}") before comparison.
var echoParam = regexp.MustCompile(`:([^/]+)`)

// registeredOperations returns the live set of "METHOD /path" operations from
// the Echo router, with path parameters normalised to OpenAPI braces.
func registeredOperations(t *testing.T) map[string]bool {
	t.Helper()
	// Construct the server with every optional feature mounted so the test sees
	// the full route surface (auth routes are conditional on an auth service).
	// The dependencies are never invoked — only the routing table is read.
	// testConfig() leaves the four ROUTE-GATING config predicates off, so the
	// routes behind them are absent here on purpose — see
	// TestNonContractRoutesAreEnumerated for the inventory of what that hides.
	return routeOperations(New(testConfig(), nil, nil, fullRouteOptions()...))
}

// routeOperations reads one server's routing table into the "METHOD /path" set
// the contract guards compare against.
func routeOperations(srv *Server) map[string]bool {
	httpMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "HEAD": true, "OPTIONS": true,
	}
	ops := map[string]bool{}
	for _, r := range srv.Handler().Routes() {
		if !httpMethods[r.Method] || strings.Contains(r.Path, "*") {
			continue // skip Echo's internal/wildcard routes
		}
		ops[r.Method+" "+echoParam.ReplaceAllString(r.Path, "{$1}")] = true
	}
	return ops
}

var specMethod = regexp.MustCompile(`^(get|post|put|patch|delete|head|options):\s*$`)

// declaredOperations parses api/openapi.yaml by indentation and returns the set
// of "METHOD /path" operations it declares.
func declaredOperations(t *testing.T, specPath string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec at %s: %v", specPath, err)
	}

	ops := map[string]bool{}
	inPaths := false
	current := ""
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			inPaths = trimmed == "paths:"
			current = ""
		case !inPaths:
			// outside the paths block
		case indent == 2 && strings.HasPrefix(trimmed, "/") && strings.HasSuffix(trimmed, ":"):
			current = strings.TrimSuffix(trimmed, ":")
		case indent == 4 && current != "":
			if m := specMethod.FindStringSubmatch(trimmed); m != nil {
				ops[strings.ToUpper(m[1])+" "+current] = true
			}
		}
	}
	if len(ops) == 0 {
		t.Fatalf("no operations parsed from %s — check the file's indentation shape", specPath)
	}
	return ops
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// knownNonContractRoutes is the explicit inventory of routes a fully configured
// server registers that api/openapi.yaml deliberately does NOT document. Every
// entry carries the reason it sits outside the REST contract.
//
// It exists because the exclusions used to be IMPLICIT. registeredOperations
// builds the server with testConfig(), which sets no FederationEnabled, no
// PublicBaseURL, no metrics registry and no dev mail capture, so every route
// behind one of those four predicates never registers and TestOpenAPIContract
// has never seen one. That is a blind zone, not a decision: nothing failed when
// a route entered or left it, and two different canonical watch-URL shapes once
// coexisted inside it unnoticed. Writing the set down turns "invisible" into
// "reviewed" — add one more gated route and the build stays red until someone
// states, here, why it is not in the product contract.
//
// Adding an entry is not a rubber stamp. The bar is that the surface is not a
// REST product API a generated client should ever call: a fediverse/ActivityPub
// contract, an operations scrape, a syndication format, or a development seam.
var knownNonContractRoutes = map[string]string{
	// METRICS_ENABLED. A Prometheus scrape for operators, root-mounted and
	// unthrottled like the health probes. Not a product API; network-scope it.
	"GET /metrics": "operations scrape",

	// FEDERATION_ENABLED (+ a wired federation service). The ActivityPub /
	// NodeInfo / WebFinger contract: its shapes are fixed by the fediverse
	// specs, its callers are other servers, and its media type is AP JSON-LD.
	// Documenting it here would push routes no browser client may call into
	// the frontend's generated client. See .ralph/specs/federation.md §4-5.
	"GET /.well-known/nodeinfo":              "fediverse discovery",
	"GET /.well-known/webfinger":             "fediverse discovery",
	"GET /nodeinfo/2.1":                      "fediverse discovery",
	"GET /accounts/{handle}":                 "ActivityPub actor",
	"GET /video-channels/{handle}":           "ActivityPub actor",
	"POST /inbox":                            "ActivityPub shared inbox",
	"POST /accounts/{handle}/inbox":          "ActivityPub inbox",
	"POST /video-channels/{handle}/inbox":    "ActivityPub inbox",
	"GET /accounts/{handle}/followers":       "ActivityPub collection",
	"GET /accounts/{handle}/following":       "ActivityPub collection",
	"GET /accounts/{handle}/outbox":          "ActivityPub collection",
	"GET /video-channels/{handle}/followers": "ActivityPub collection",
	"GET /video-channels/{handle}/following": "ActivityPub collection",
	"GET /video-channels/{handle}/outbox":    "ActivityPub collection",

	// PUBLIC_BASE_URL (+ wired video/channel services). Syndication formats
	// answering XML/oEmbed JSON to feed readers, crawlers and embed resolvers,
	// built from absolute public URLs. See internal/httpapi/distribution.go.
	"GET /feeds/videos.xml": "syndication feed",
	"GET /services/oembed":  "oEmbed provider",
	"GET /sitemap.xml":      "crawler sitemap",

	// DEV_MAIL_CAPTURE_ENABLED, and refused outright when Environment is
	// "production". A development seam for reading account-security tokens
	// without a mail relay. It is deliberately absent from the contract: a
	// public spec entry would advertise it to every generated client and every
	// reader of the spec, which is strictly worse than the /api/v1 namespace
	// inconsistency that documenting it would tidy up.
	"GET /api/v1/dev/email-token": "development-only seam",
}

// TestNonContractRoutesAreEnumerated closes the flag-gated blind spot in
// TestOpenAPIContract. It builds the server twice — once as the contract guard
// does, once with every route-gating config predicate satisfied — and asserts
// the difference is exactly knownNonContractRoutes.
//
// It also asserts every excluded route stays UNdocumented, so the list can
// never become a way to smuggle a documented-but-unregistered path past the
// guard above (which only ever sees the default server's routes).
func TestNonContractRoutesAreEnumerated(t *testing.T) {
	contract := registeredOperations(t)
	gated := allGatesOpenOperations(t)
	declared := declaredOperations(t, filepath.Join("..", "..", "api", "openapi.yaml"))

	excluded := map[string]bool{}
	for op := range gated {
		if !contract[op] {
			excluded[op] = true
		}
	}

	for op := range excluded {
		if _, known := knownNonContractRoutes[op]; !known {
			t.Errorf("route %q registers only behind a config gate, so TestOpenAPIContract cannot see it, and it is not in knownNonContractRoutes — either document it in api/openapi.yaml and mount it unconditionally, or add it to the list with the reason it is outside the REST contract", op)
		}
	}
	for op := range knownNonContractRoutes {
		if !excluded[op] {
			t.Errorf("knownNonContractRoutes lists %q but no such config-gated route exists — it was renamed, deleted, or is now mounted unconditionally; drop it from the list in the same change", op)
		}
		if declared[op] {
			t.Errorf("knownNonContractRoutes lists %q as outside the REST contract, but api/openapi.yaml documents it — an operation is either in the contract and mounted unconditionally, or excluded and undocumented", op)
		}
	}

	if t.Failed() {
		t.Logf("routes registered only behind a config gate:\n  %s", strings.Join(sortedKeys(excluded), "\n  "))
	}
}

// allGatesOpenOperations returns the route set of a server built with the four
// predicates that make a route come and go all satisfied: FederationEnabled
// (the ActivityPub/NodeInfo root routes), PublicBaseURL (the feeds/oEmbed/
// sitemap distribution routes), a wired metrics registry (the /metrics scrape),
// and the development-only mail capture (/api/v1/dev/email-token). Everything
// else is fullRouteOptions, so the difference against registeredOperations is
// precisely the config-gated surface.
func allGatesOpenOperations(t *testing.T) map[string]bool {
	t.Helper()
	cfg := testConfig()
	cfg.FederationEnabled = true
	cfg.MetricsEnabled = true
	cfg.PublicBaseURL = "https://vidra.test"
	opts := append(fullRouteOptions(),
		WithMetrics(observability.NewMetrics()),
		WithDevMailCapture(auth.NewCaptureMailer()),
	)
	return routeOperations(New(cfg, nil, nil, opts...))
}
