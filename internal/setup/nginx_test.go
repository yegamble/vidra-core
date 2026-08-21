package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The whole external-terminator example in one artifact, reviewed the way the
// Caddyfile goldens are: this file is the routing an operator's own proxy will
// perform, so a diff here is a change to how a deployment behaves at its front
// door. UPDATE_GOLDEN=1 rewrites it.
func TestRenderNginxExternalGolden(t *testing.T) {
	got, err := RenderNginxExternal(Answers{Domain: "video.example.org"}, nil)
	if err != nil {
		t.Fatalf("RenderNginxExternal: %v", err)
	}
	path := filepath.Join("testdata", "nginx-external.conf.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden — regenerate with UPDATE_GOLDEN=1 go test ./internal/setup -run RenderNginxExternalGolden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("rendered nginx example drifted from golden\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// The routing has to mirror deploy/Caddyfile block for block. These are the four
// blocks, and each one is a real failure if it goes missing: a public /metrics,
// a live password-reset token endpoint, the frontend answering /api/* (a sitemap
// of dead links), or nginx's 1m body default killing every upload.
func TestRenderNginxExternalMirrorsTheCaddyfileRouting(t *testing.T) {
	got, err := RenderNginxExternal(Answers{Domain: "video.example.org"}, nil)
	if err != nil {
		t.Fatalf("RenderNginxExternal: %v", err)
	}
	out := string(got)
	for _, want := range []string{
		"location = /metrics {\n\t\treturn 404;",
		"location ^~ /api/v1/dev/ {\n\t\treturn 404;",
		"location ^~ /api/ {",
		"proxy_pass http://" + nginxUpstreamHost + ":" + nginxDefaultAPIPort + ";",
		"proxy_pass http://" + nginxUpstreamHost + ":" + nginxDefaultFrontendPort + ";",
		"client_max_body_size " + nginxBodyLimit + ";",
		"proxy_request_buffering off;",
		"proxy_read_timeout " + nginxStreamTimeout + ";",
		"proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
		"proxy_set_header Connection $connection_upgrade;",
		"server_name video.example.org;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the generated nginx example is missing %q:\n%s", want, out)
		}
	}
	// Every root-level api route the Caddyfile hands to api:8080 must be routed
	// here too. A missing one is a 404 on federation, discovery or a feed that
	// nothing else in the stack would explain.
	for _, path := range []string{
		"/healthz", "/readyz", "/version", "/inbox", "/nodeinfo/2.1",
		"/.well-known/nodeinfo", "/.well-known/webfinger",
		"/services/oembed", "/sitemap.xml",
	} {
		if !strings.Contains(out, "location = "+path+" {") {
			t.Errorf("the api route %s is not proxied to the api:\n%s", path, out)
		}
	}
	for _, prefix := range []string{"/accounts/", "/video-channels/", "/feeds/"} {
		if !strings.Contains(out, "location ^~ "+prefix+" {") {
			t.Errorf("the api prefix %s is not proxied to the api:\n%s", prefix, out)
		}
	}
	// /.well-known/acme-challenge must NOT be routed to the api: it belongs to
	// whatever renews the terminator's certificate.
	if strings.Contains(out, "acme-challenge/ {") {
		t.Errorf("the ACME challenge path was routed away from the proxy that renews the certificate:\n%s", out)
	}
	// The variable an external terminator on a public address needs, named where
	// the operator wiring that terminator will read it.
	if !strings.Contains(out, "TRUSTED_PROXY_CIDRS") {
		t.Errorf("the example does not mention TRUSTED_PROXY_CIDRS, the one env variable a public-address terminator needs:\n%s", out)
	}
	// gzip is scoped to the frontend block only: compressing the api's HLS
	// segments and range-served media is worthless and breaks seeking.
	if before, _, ok := strings.Cut(out, "location / {"); ok && strings.Contains(before, "gzip on;") {
		t.Errorf("gzip is enabled outside the frontend block:\n%s", out)
	}
}

// Same answers, same bytes — the file is copied and diffed by hand, so a re-run
// that reshuffled it would look like a change nobody made.
func TestRenderNginxExternalIsDeterministic(t *testing.T) {
	a := Answers{Domain: "https://VIDEO.Example.ORG/"}
	first, err := RenderNginxExternal(a, nil)
	if err != nil {
		t.Fatalf("RenderNginxExternal: %v", err)
	}
	second, err := RenderNginxExternal(a, nil)
	if err != nil {
		t.Fatalf("RenderNginxExternal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the nginx example rendered differently twice")
	}
	// And the host is normalised exactly as the Caddyfile's site address is: the
	// two files describe the same origin, whichever one a deployment uses.
	if !strings.Contains(string(first), "server_name video.example.org;") {
		t.Errorf("the domain answer was not normalised into the server_name:\n%s", first)
	}
}

// external is the mode with NO Caddyfile, and asking for one has to fail loudly
// rather than produce a file nothing mounts — one that would order a certificate
// for a hostname the operator's own terminator already holds one for.
func TestRenderCaddyfileRefusesTheExternalMode(t *testing.T) {
	_, err := RenderCaddyfile(caddyTemplate(t), Answers{Domain: "video.example.org", TLSMode: TLSModeExternal})
	if err == nil {
		t.Fatal("RenderCaddyfile rendered a Caddyfile for VIDRA_TLS_MODE=external")
	}
	for _, want := range []string{TLSModeExternal, NginxExampleOutputPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q, so it does not say what to do instead", err, want)
		}
	}
	if !SkipsManagedCaddy(TLSModeExternal) {
		t.Error("SkipsManagedCaddy(external) = false")
	}
	// Every other mode keeps its Caddyfile, plain-http included: it is the mode
	// that changes the SCHEME, not the one that removes the proxy.
	for _, mode := range []string{TLSModeACME, TLSModeACMEStaging, TLSModeInternal, TLSModePlainHTTP, ""} {
		if SkipsManagedCaddy(mode) {
			t.Errorf("SkipsManagedCaddy(%q) = true; only external has no managed proxy", mode)
		}
	}
}

// HTTP_PORT and FRONTEND_PORT are operator knobs — the deployment template
// assigns them explicitly and deploy.sh's external-mode guidance prints the real
// values — so an example that hardcoded 8080/3000 would point at nothing the
// moment somebody moved a port, and would contradict the other surface telling
// them what to do.
func TestRenderNginxExternalFollowsThePublishedPorts(t *testing.T) {
	got, err := RenderNginxExternal(Answers{Domain: "video.example.org"},
		map[string]string{"HTTP_PORT": "18080", "FRONTEND_PORT": "13000"})
	if err != nil {
		t.Fatalf("RenderNginxExternal: %v", err)
	}
	out := string(got)
	for _, want := range []string{"proxy_pass http://127.0.0.1:18080;", "proxy_pass http://127.0.0.1:13000;"} {
		if !strings.Contains(out, want) {
			t.Errorf("the example does not follow the published ports (missing %q):\n%s", want, out)
		}
	}
	if strings.Contains(out, "127.0.0.1:8080") || strings.Contains(out, "127.0.0.1:3000") {
		t.Errorf("the example still points at the default ports:\n%s", out)
	}

	// A value compose itself would reject falls back to the default rather than
	// writing an nginx that refuses to start. `vidra setup --check` reports the
	// bad value through the api's own validation; this file is not the place to
	// discover it.
	for _, bad := range []string{"eighty-eighty", "0", "99999", "  "} {
		b, err := RenderNginxExternal(Answers{Domain: "video.example.org"}, map[string]string{"HTTP_PORT": bad})
		if err != nil {
			t.Fatalf("RenderNginxExternal(HTTP_PORT=%q): %v", bad, err)
		}
		if !strings.Contains(string(b), "proxy_pass http://127.0.0.1:8080;") {
			t.Errorf("HTTP_PORT=%q did not fall back to the compose default:\n%s", bad, b)
		}
	}
}
