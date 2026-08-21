package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/config"
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
	cfg.SMTPHost = "smtp.example"
	cfg.SMTPPort = 587
	cfg.SMTPFrom = "noreply@example.test"
	cfg.SMTPUsername = "SECRET-SMTP-USERNAME"
	cfg.SMTPPassword = "SECRET-SMTP-PASSWORD"
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
		"SECRET-OWNER-CLAIM-TOKEN",
		"db.internal", "redis.internal", "pt.internal", "search.internal",
		"otel-collector.internal",
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
		"malware_scan", "captions", "live", "ipfs", "tracing", "metrics", "vp9_alternates",
	}
	if len(body.Features) != len(wantKeys) {
		t.Fatalf("features = %d entries, want %d", len(body.Features), len(wantKeys))
	}
	for i, key := range wantKeys {
		if body.Features[i].Key != key {
			t.Errorf("feature %d = %q, want %q (the order is part of the contract)", i, body.Features[i].Key, key)
		}
	}
	// DASH and CDN do not exist in vidra. Listing them would send an operator
	// hunting for switches that were never built.
	for _, f := range body.Features {
		if f.Key == "dash" || f.Key == "cdn" {
			t.Errorf("feature %q is reported but does not exist in this codebase", f.Key)
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
}
