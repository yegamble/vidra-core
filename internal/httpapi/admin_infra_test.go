package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// infrastructure reads the admin infrastructure document as the instance's
// first (admin) account, and returns the decoded body plus the raw JSON — the
// raw form is what the secret-leak assertion greps.
func infrastructure(t *testing.T, srv *Server) (infrastructureResponse, string) {
	t.Helper()
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := getWithAuth(srv, "/api/v1/admin/infrastructure", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("infrastructure = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body infrastructureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body, rec.Body.String()
}

// featureNamed returns one discovery entry by key.
func featureNamed(t *testing.T, body infrastructureResponse, key string) infraFeature {
	t.Helper()
	for _, f := range body.Features {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("feature %q missing from %d features", key, len(body.Features))
	return infraFeature{}
}

func TestInfrastructureReportsTheDeployShape(t *testing.T) {
	srv := authServer(t)
	body, _ := infrastructure(t, srv)

	if body.Server.Environment != "test" {
		t.Errorf("environment = %q, want test", body.Server.Environment)
	}
	if body.Server.BodyLimit != "8M" || body.Server.UploadMaxBytes != 64000 {
		t.Errorf("server caps = %+v, want body_limit 8M and a 64K upload cap in bytes", body.Server)
	}
	if body.Server.RequestTimeoutSeconds != 30 || body.Server.StreamRequestTimeoutSeconds != 3600 {
		t.Errorf("server deadlines = %+v, want 30s general and 1h streaming", body.Server)
	}
	// The two lists marshal as [] rather than null, so a client never has to
	// distinguish "none" from "missing".
	if body.Networking.TrustedProxyCIDRs == nil || body.Networking.CORSAllowedOrigins == nil {
		t.Errorf("networking lists = %+v, want [] rather than null", body.Networking)
	}
	if len(body.Networking.CORSAllowedOrigins) != 1 || body.Networking.CORSAllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("cors_allowed_origins = %v, want the configured origin", body.Networking.CORSAllowedOrigins)
	}
	// The backups block is guidance and always populated — an operator reading
	// this page must not have to guess whether silence means "no backups".
	for name, note := range map[string]string{
		"schedule":   body.Backups.ScheduleNote,
		"staleness":  body.Backups.StalenessNote,
		"artifacts":  body.Backups.ArtifactsNote,
		"live state": body.Backups.LiveStateNote,
	} {
		if strings.TrimSpace(note) == "" {
			t.Errorf("backups %s note is empty", name)
		}
	}
	if !strings.Contains(body.Backups.LiveStateNote, "vidra doctor") {
		t.Errorf("the live-state note must name the tool that CAN answer: %q", body.Backups.LiveStateNote)
	}

	// A regular user cannot read it; anon is unauthorized. Deploy-time shape is
	// reconnaissance for anyone who is not running the instance.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := getWithAuth(srv, "/api/v1/admin/infrastructure", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin infrastructure = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/infrastructure", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon infrastructure = %d, want 401", rec.Code)
	}
}

// The guard that matters. Every secret this config can hold is set to a
// distinctive value, and none of them may appear anywhere in the response —
// including inside a note, which is free-form prose and therefore the easiest
// place for one to be interpolated by accident later.
func TestInfrastructureLeaksNoSecret(t *testing.T) {
	cfg := testConfig()
	cfg.DatabaseURL = "postgres://vidra:SECRET-PG-PASSWORD@db.internal:5432/vidra"
	cfg.RedisURL = "redis://:SECRET-REDIS-PASSWORD@redis.internal:6379/0"
	cfg.JWTSecret = "SECRET-JWT-SIGNING-KEY-0000000000000000"
	cfg.StorageBackend = "s3"
	cfg.StorageS3Endpoint = "objects.example:443"
	cfg.StorageS3Bucket = "vidra-media"
	cfg.StorageS3Region = "us-east-1"
	cfg.StorageS3AccessKey = "SECRET-S3-ACCESS-KEY"
	cfg.StorageS3SecretKey = "SECRET-S3-SECRET-KEY"
	cfg.MailEnabled = true
	cfg.SMTPHost = "SENTINEL-smtp-relay.internal"
	cfg.SMTPPort = 587
	cfg.SMTPFrom = "SENTINEL-noreply@example.test"
	cfg.SMTPUsername = "SECRET-SMTP-USERNAME"
	cfg.SMTPPassword = "SECRET-SMTP-PASSWORD"
	// Every other INTERNAL endpoint the config knows. None of these is a
	// credential, which is exactly why they need pinning: the struct's doc
	// comment says internal addresses stay out (naming internal hosts on an
	// admin page is free reconnaissance), and until now nothing enforced that
	// for anything but the DSNs. A field added later that helpfully reports
	// "your ClamAV is at ..." fails here.
	cfg.MalwareScanEnabled = true
	cfg.ClamAVAddr = "SENTINEL-clamav.internal:3310"
	cfg.WhisperEnabled = true
	cfg.WhisperEndpoint = "http://SENTINEL-whisper.internal:9000"
	cfg.LiveEnabled = true
	cfg.LiveRTMPURL = "rtmp://SENTINEL-ingest.internal/live"
	cfg.LiveHLSRoot = "/srv/SENTINEL-live-hls"
	cfg.IPFSEnabled = true
	cfg.IPFSAPIURL = "http://SENTINEL-kubo.internal:5001"
	cfg.IPFSGatewayURL = "http://SENTINEL-gateway.internal:8080"
	cfg.IPFSPrivateAPIURL = "http://SENTINEL-kubo-private.internal:5001"
	cfg.IPFSClusterAPIURL = "http://SENTINEL-cluster.internal:9094"
	cfg.FederationKeyKEK = "SECRET-FEDERATION-KEK"
	cfg.ATProtoKeyKEK = "SECRET-ATPROTO-KEK"
	cfg.MFAKeyKEK = "SECRET-MFA-KEK"
	cfg.LiveIngestSecret = "SECRET-LIVE-INGEST"
	cfg.SearchInternalSecret = "SECRET-SEARCH-INTERNAL"
	cfg.SearchServiceURL = "http://search.internal:8080"
	cfg.IPFSClusterToken = "SECRET-IPFS-CLUSTER-TOKEN"
	cfg.PeerTubeSourceDatabaseURL = "postgres://pt:SECRET-PEERTUBE-PASSWORD@pt.internal:5432/peertube"
	cfg.PeerTubeSourceS3AccessKey = "SECRET-PEERTUBE-S3-ACCESS-KEY"
	cfg.PeerTubeSourceS3SecretKey = "SECRET-PEERTUBE-S3-SECRET-KEY"
	cfg.OwnerClaimToken = "SECRET-OWNER-CLAIM-TOKEN"
	cfg.OTelExporterEndpoint = "otel-collector.internal:4317"
	// The two phase-4/5 secrets the CDN and DRM rows sit next to. Both rows
	// report PRESENCE only, and a page that grew a "your KEK is ..." field would
	// hand a content key to anyone who reaches an admin session.
	cfg.DRMProvider = "clearkey-test"
	cfg.DRMKeyKEK = "SECRET-DRM-KEY-KEK"
	cfg.DeliveryCDNBaseURL = "https://cdn.example.test/media"
	cfg.DeliveryCDNPurgeURL = "https://api.SENTINEL-cdn-control.internal/purge"
	cfg.DeliveryCDNPurgeToken = "SECRET-CDN-PURGE-TOKEN"

	srv := authServerWithConfig(t, cfg)
	body, rawJSON := infrastructure(t, srv)

	// Anything the config calls a secret, plus the internal endpoints that are
	// reconnaissance rather than credentials.
	for _, forbidden := range []string{
		"SECRET-PG-PASSWORD", "SECRET-REDIS-PASSWORD", "SECRET-JWT-SIGNING-KEY",
		"SECRET-S3-ACCESS-KEY", "SECRET-S3-SECRET-KEY",
		"SECRET-SMTP-USERNAME", "SECRET-SMTP-PASSWORD",
		"SECRET-FEDERATION-KEK", "SECRET-ATPROTO-KEK", "SECRET-MFA-KEK",
		"SECRET-LIVE-INGEST", "SECRET-SEARCH-INTERNAL", "SECRET-IPFS-CLUSTER-TOKEN",
		"SECRET-PEERTUBE-PASSWORD", "SECRET-PEERTUBE-S3-ACCESS-KEY", "SECRET-PEERTUBE-S3-SECRET-KEY",
		"SECRET-OWNER-CLAIM-TOKEN", "SECRET-DRM-KEY-KEK", "SECRET-CDN-PURGE-TOKEN",
		"db.internal", "redis.internal", "pt.internal", "search.internal",
		"otel-collector.internal", "SENTINEL-cdn-control.internal",
		// The internal endpoints. Not credentials, but the struct's doc comment
		// keeps them out and this is what holds it to that.
		"SENTINEL-smtp-relay.internal", "SENTINEL-noreply@example.test",
		"SENTINEL-clamav.internal", "SENTINEL-whisper.internal",
		"SENTINEL-ingest.internal", "SENTINEL-live-hls",
		"SENTINEL-kubo.internal", "SENTINEL-gateway.internal",
		"SENTINEL-kubo-private.internal", "SENTINEL-cluster.internal",
	} {
		if strings.Contains(rawJSON, forbidden) {
			t.Errorf("the infrastructure response leaks %q:\n%s", forbidden, rawJSON)
		}
	}

	// The non-secret coordinates ARE reported: the point of the page is that an
	// operator can see which bucket they are filling.
	if body.Storage.Backend != "s3" || body.Storage.S3Bucket != "vidra-media" ||
		body.Storage.S3Endpoint != "objects.example:443" || body.Storage.S3Region != "us-east-1" {
		t.Errorf("storage = %+v, want the s3 coordinates reported", body.Storage)
	}
}

// enabled and configured come apart, and the gap is the whole point: a switch
// that is on with its dependency missing is a feature that exists on paper.
func TestInfrastructureFeatureDiscovery(t *testing.T) {
	// A default deployment: almost everything off, so almost every feature
	// carries discovery copy naming the variable to set.
	srv := authServer(t)
	body, _ := infrastructure(t, srv)

	wantKeys := []string{
		"object_storage", "mail", "search", "federation", "atproto", "atproto_login",
		"malware_scan", "captions", "live", "ipfs", "cdn", "drm", "tracing", "metrics",
		"vp9_alternates",
	}
	if len(body.Features) != len(wantKeys) {
		t.Fatalf("features = %d entries, want %d", len(body.Features), len(wantKeys))
	}
	for i, key := range wantKeys {
		if body.Features[i].Key != key {
			t.Errorf("feature %d = %q, want %q (the order is part of the contract)", i, body.Features[i].Key, key)
		}
	}
	// DASH is produced (the default cmaf packager writes an MPD beside the HLS
	// playlists) and is still not a row: packaging format is a per-VIDEO
	// property with no switch to turn on, so no (enabled, configured) pair could
	// describe an instance serving both formats at once.
	for _, f := range body.Features {
		if f.Key == "dash" {
			t.Errorf("feature %q is reported; packaging format is per-video, not an optional subsystem", f.Key)
		}
	}

	if store := featureNamed(t, body, "object_storage"); store.Enabled || store.Configured {
		t.Errorf("object_storage on a local deployment = %+v, want off", store)
	} else if !strings.Contains(store.Note, "STORAGE_BACKEND=s3") {
		t.Errorf("object_storage note must name the variable to set: %q", store.Note)
	}
	// Every off feature says what turning it on would buy — an operator cannot
	// enable something they never knew shipped.
	for _, f := range body.Features {
		if !f.Enabled && strings.TrimSpace(f.Note) == "" {
			t.Errorf("feature %q is off with no note; discovery is the point of the list", f.Key)
		}
	}

	// The dangerous half, one switch at a time: on, with its dependency absent.
	for name, tc := range map[string]struct {
		mutate  func(*config.Config)
		key     string
		wantHas string
	}{
		"whisper without an endpoint": {
			func(c *config.Config) { c.WhisperEnabled = true },
			"captions", "WHISPER_ENDPOINT",
		},
		"clamav without an address": {
			func(c *config.Config) { c.MalwareScanEnabled = true },
			"malware_scan", "CLAMAV_ADDR",
		},
		"otel without a collector": {
			func(c *config.Config) { c.OTelEnabled = true },
			"tracing", "OTEL_EXPORTER_OTLP_ENDPOINT",
		},
		"atproto without a sealing key": {
			func(c *config.Config) { c.ATProtoEnabled = true },
			"atproto", "KEY_KEK",
		},
		"live without an ingest plane": {
			func(c *config.Config) { c.LiveEnabled = true },
			"live", "LIVE_RTMP_URL",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.LiveEnabled = false // the default-on toggle, so each case starts from off
			tc.mutate(cfg)
			got, _ := infrastructure(t, authServerWithConfig(t, cfg))
			f := featureNamed(t, got, tc.key)
			if !f.Enabled {
				t.Fatalf("%s = %+v, want enabled", tc.key, f)
			}
			if f.Configured {
				t.Fatalf("%s = %+v, want configured=false — the dependency is absent", tc.key, f)
			}
			if !strings.Contains(f.Note, tc.wantHas) {
				t.Errorf("%s note = %q, want it to name %s", tc.key, f.Note, tc.wantHas)
			}
		})
	}

	// A feature that is both on and complete says nothing: the success pill
	// already carries the whole message.
	cfg := testConfig()
	cfg.MetricsEnabled = true
	on, _ := infrastructure(t, authServerWithConfig(t, cfg))
	if m := featureNamed(t, on, "metrics"); !m.Enabled || !m.Configured || m.Note != "" {
		t.Errorf("metrics fully on = %+v, want enabled+configured with no note", m)
	}
}

// The multi-node floor an operator cannot see from inside the product
// (phase-5): how big this process's slice of the database's connection budget
// is, and whether a deploy will reset live connections behind a load balancer.
func TestInfrastructureReportsPoolAndDrainConfig(t *testing.T) {
	cfg := testConfig()
	cfg.DBMaxConns = 24
	cfg.DBMinConns = 4
	cfg.DBConnMaxLifetime = 90 * time.Minute
	cfg.DBConnMaxIdleTime = 5 * time.Minute
	cfg.HTTPDrainDelay = 12 * time.Second

	body, _ := infrastructure(t, authServerWithConfig(t, cfg))

	srvBlock := body.Server
	if srvBlock.DBMaxConns != 24 || srvBlock.DBMinConns != 4 {
		t.Errorf("pool sizing = %+v, want max 24 / min 4", srvBlock)
	}
	if srvBlock.DBConnMaxLifetimeSeconds != 5400 || srvBlock.DBConnMaxIdleTimeSeconds != 300 {
		t.Errorf("conn lifetimes = %+v, want 5400s lifetime and 300s idle", srvBlock)
	}
	if srvBlock.DrainDelaySeconds != 12 {
		t.Errorf("drain_delay_seconds = %d, want 12", srvBlock.DrainDelaySeconds)
	}

	// The single-node default is zero, and zero is a REPORTED value rather than
	// a missing one: behind a load balancer it is the difference between a
	// clean deploy and connection resets on live requests.
	def, _ := infrastructure(t, authServer(t))
	if def.Server.DrainDelaySeconds != 0 {
		t.Errorf("default drain_delay_seconds = %d, want 0", def.Server.DrainDelaySeconds)
	}
}

// infraServerWithSettings is authServerWithConfig plus a loaded instance-settings
// overlay, so a test can flip the runtime half of the CDN pair the way an admin
// would. It is the only feature row that reads the overlay at all.
func infraServerWithSettings(t *testing.T, cfg *config.Config, cdnOn bool) *Server {
	t.Helper()
	svc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	if cdnOn {
		if err := svc.Apply(context.Background(), map[string]instancesettings.Update{
			instancesettings.KeyDeliveryCDNEnabled: {Value: "true"},
		}, uuid.Nil); err != nil {
			t.Fatalf("enable delivery_cdn_enabled: %v", err)
		}
	}
	srv := authServerWithConfig(t, cfg)
	WithSettingsService(svc)(srv)
	return srv
}

// The CDN row is the one whose two halves live in different places — the base
// URL is boot config, the switch is a runtime setting — so all four quadrants
// are real states an operator can be in, and each needs its own true sentence.
func TestInfrastructureCDNFeature(t *testing.T) {
	const base = "https://cdn.example.test/media"

	t.Run("nothing configured", func(t *testing.T) {
		body, _ := infrastructure(t, authServer(t))
		f := featureNamed(t, body, "cdn")
		if f.Enabled || f.Configured {
			t.Fatalf("cdn on a default install = %+v, want off", f)
		}
		if !strings.Contains(f.Note, "DELIVERY_CDN_BASE_URL") {
			t.Errorf("cdn note = %q, want it to name the variable to set", f.Note)
		}
	})

	// Wired and not switched on is the SHIPPED sequence, not a mistake: the
	// generic discovery note would tell this operator to set a variable they
	// have already set.
	t.Run("wired but switched off", func(t *testing.T) {
		cfg := testConfig()
		cfg.DeliveryCDNBaseURL = base
		body, _ := infrastructure(t, infraServerWithSettings(t, cfg, false))
		f := featureNamed(t, body, "cdn")
		if f.Enabled || !f.Configured {
			t.Fatalf("cdn wired with the toggle off = %+v, want configured and not enabled", f)
		}
		if strings.Contains(f.Note, "Point DELIVERY_CDN_BASE_URL") {
			t.Errorf("cdn note tells an operator to set a variable they already set: %q", f.Note)
		}
		if !strings.Contains(f.Note, "delivery_cdn_enabled") {
			t.Errorf("cdn note = %q, want it to name the switch that is still off", f.Note)
		}
	})

	// The dangerous half: the switch is on and no edge exists, so nothing is
	// being offloaded and the page must say so rather than show a success pill.
	t.Run("switched on with no edge", func(t *testing.T) {
		body, _ := infrastructure(t, infraServerWithSettings(t, testConfig(), true))
		f := featureNamed(t, body, "cdn")
		if !f.Enabled || f.Configured {
			t.Fatalf("cdn toggled on with no base URL = %+v, want enabled and not configured", f)
		}
		if !strings.Contains(f.Note, "DELIVERY_CDN_BASE_URL") {
			t.Errorf("cdn note = %q, want it to name the missing half", f.Note)
		}
	})

	t.Run("fully on", func(t *testing.T) {
		cfg := testConfig()
		cfg.DeliveryCDNBaseURL = base
		body, _ := infrastructure(t, infraServerWithSettings(t, cfg, true))
		f := featureNamed(t, body, "cdn")
		if !f.Enabled || !f.Configured || f.Note != "" {
			t.Fatalf("cdn fully on = %+v, want enabled+configured with no note", f)
		}
	})
}

// DRM reports the provider's state and the PRESENCE of its sealing key, never
// the key. The row's honesty problem is the opposite of the others': the only
// provider this build ships protects nothing, and a green pill next to the word
// DRM is a sentence an operator will repeat to somebody else.
func TestInfrastructureDRMFeature(t *testing.T) {
	off, _ := infrastructure(t, authServer(t))
	f := featureNamed(t, off, "drm")
	if f.Enabled || f.Configured {
		t.Fatalf("drm with no provider = %+v, want off", f)
	}
	for _, want := range []string{"DRM_PROVIDER=clearkey-test", "TEST"} {
		if !strings.Contains(f.Note, want) {
			t.Errorf("drm note = %q, want it to contain %q — the only provider is test-grade", f.Note, want)
		}
	}

	cfg := testConfig()
	cfg.DRMProvider = "clearkey-test"
	cfg.DRMKeyKEK = "c2VjcmV0LXRlc3Qta2V5LXRoaXJ0eS10d28tYnl0ZQ=="
	on, _ := infrastructure(t, authServerWithConfig(t, cfg))
	f = featureNamed(t, on, "drm")
	if !f.Enabled || !f.Configured {
		t.Fatalf("drm with a provider and a KEK = %+v, want enabled+configured", f)
	}
	// The ACTIVE state must carry the warning too. enabled+configured is the one
	// quadrant the generic notes stay silent for — the success pill is normally
	// the whole message — and for the test provider that silence is the lie: the
	// pill would read "Active — DRM content protection" while no media byte is
	// encrypted and the content key travels to every viewer in the clear.
	for _, want := range []string{"TEST", "encrypt"} {
		if !strings.Contains(f.Note, want) {
			t.Errorf("active clearkey-test drm note = %q, want it to contain %q — the green pill needs the caveat next to it", f.Note, want)
		}
	}
}

// The backups block flips to the managed-database story on the operator's
// declaration, because the two are opposite advice and the api cannot tell them
// apart from the DSN.
func TestInfrastructureBackupsFollowExternalPostgres(t *testing.T) {
	local, _ := infrastructure(t, authServer(t))
	if local.Backups.ExternalPostgres {
		t.Error("external_postgres = true by default")
	}
	if !strings.Contains(local.Backups.ScheduleNote, "deploy/backup.sh") {
		t.Errorf("self-hosted schedule note = %q, want it to name the script", local.Backups.ScheduleNote)
	}

	cfg := testConfig()
	cfg.ExternalPostgres = true
	external, _ := infrastructure(t, authServerWithConfig(t, cfg))
	if !external.Backups.ExternalPostgres {
		t.Fatal("external_postgres = false with VIDRA_EXTERNAL_POSTGRES set")
	}
	if !strings.Contains(external.Backups.ScheduleNote, "provider") {
		t.Errorf("managed-database schedule note = %q, want it to point at the provider's snapshots", external.Backups.ScheduleNote)
	}
	// The rest of the contract still applies — a managed database does not make
	// media back itself up.
	if !strings.Contains(external.Backups.ArtifactsNote, "MEDIA") {
		t.Errorf("artifacts note = %q, want it to still call out media", external.Backups.ArtifactsNote)
	}
}

// A deployment with no mail path at all still answers; the capture seam counts
// as one (it is how the dev/e2e stack sends), which is why enabled and
// configured are separate questions here too.
func TestInfrastructureMailCapability(t *testing.T) {
	off, _ := infrastructure(t, authServer(t))
	if m := featureNamed(t, off, "mail"); m.Enabled || m.Configured {
		t.Errorf("mail with no mailer = %+v, want off", m)
	}

	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	srv := New(testConfig(), nil, nil,
		WithAuthService(svc, 15*time.Minute),
		WithContactMailer(auth.NewCaptureMailer()),
	)
	captured, _ := infrastructure(t, srv)
	m := featureNamed(t, captured, "mail")
	if !m.Enabled {
		t.Errorf("mail with the capture seam = %+v, want enabled (it is an outbound path)", m)
	}
	if m.Configured {
		t.Errorf("mail = %+v, want configured=false — the capture seam is not an SMTP relay", m)
	}
	if !strings.Contains(m.Note, "SMTP_HOST") {
		t.Errorf("mail note = %q, want it to name the relay variables", m.Note)
	}
	// The quadrant the generic notes get wrong. enabled+unconfigured normally
	// means "MAIL_ENABLED is set but the relay is incomplete" — with the capture
	// seam BOTH halves of that are false, and a page whose job is to be honest
	// about the deployment must not open with a false sentence.
	if strings.Contains(m.Note, "MAIL_ENABLED is set") {
		t.Errorf("mail note claims MAIL_ENABLED is set on a capture-seam deployment: %q", m.Note)
	}
	if !strings.Contains(m.Note, "CAPTURED") {
		t.Errorf("mail note = %q, want it to say messages are captured rather than delivered", m.Note)
	}

	// The override is scoped to that quadrant and nothing else: a real relay
	// reports configured with nothing to say. (There is no MAIL_ENABLED-with-no-
	// relay case to test here — config refuses to boot in that state, which is
	// why the generic misconfigured note for mail does not exist.)
	full := testConfig()
	full.MailEnabled = true
	full.SMTPHost = "smtp.example"
	full.SMTPPort = 587
	full.SMTPFrom = "noreply@example.test"
	relaySrv := New(full, nil, nil,
		WithAuthService(auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour), 15*time.Minute),
		WithContactMailer(auth.NewCaptureMailer()),
	)
	wired, _ := infrastructure(t, relaySrv)
	if rm := featureNamed(t, wired, "mail"); !rm.Enabled || !rm.Configured || rm.Note != "" {
		t.Errorf("mail with a configured relay = %+v, want enabled+configured with no note", rm)
	}
}
