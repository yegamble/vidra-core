package setup

import (
	"fmt"
	"strconv"
	"strings"
)

// The external-terminator example (phase-1 item 12, VIDRA_TLS_MODE=external).
//
// WHY THIS FILE IS GENERATED AT ALL. `external` means the managed caddy does not
// run: the operator's own load balancer, CDN or nginx terminates TLS and proxies
// to this host. The routing it has to perform is NOT obvious, and getting it
// wrong is not a 404 an operator can debug from the outside — it is a sitemap of
// dead links (the frontend answering /api/*), a public /metrics endpoint, or an
// upload that dies at 1 MiB because nginx's default body limit is 1m. So the
// same engine that renders the Caddyfile writes down what the Caddyfile does, in
// the other syntax, from the same answers.
//
// IT IS AN EXAMPLE, and the .example suffix is load-bearing: nothing mounts it,
// nothing reloads it, and this repository cannot know where the operator's nginx
// reads its configuration from or which certificates it holds. What the file
// guarantees is that the ROUTING matches deploy/Caddyfile block for block — the
// part that is this project's business — with the TLS lines left as
// unmistakable placeholders.
//
// The routing is deliberately kept in the same ORDER as the Caddyfile, with the
// same four blocks, so the two can be diffed by eye when either changes:
//
//  1. /metrics                 -> 404 (never publish the scrape surface)
//  2. /api/v1/dev/*            -> 404 (defense in depth for the dev mail route)
//  3. the api's root surfaces  -> the api container
//  4. everything else          -> the Next.js frontend
const (
	// NginxExampleOutputPath is where `vidra setup` writes it, spelled here for
	// the same reason as CaddyOutputPath: the CLI, the installer, the deploy
	// scripts and the docs must not disagree about the path.
	NginxExampleOutputPath = "deploy/nginx-external.conf.example"

	// The upstreams are LOOPBACK addresses, not compose service names, and that
	// is the whole difference between this file and the Caddyfile: the operator's
	// nginx runs on the HOST, outside the compose network. docker-compose.prod.yml
	// publishes `127.0.0.1:${HTTP_PORT:-8080}:8080` and
	// `127.0.0.1:${FRONTEND_PORT:-3000}:3000`.
	//
	// The PORTS are read from the env file rather than baked, because HTTP_PORT
	// and FRONTEND_PORT are operator knobs the deployment template assigns
	// explicitly — and deploy.sh's own external-mode guidance prints the real
	// values. A hardcoded example is a config that silently points at nothing the
	// moment somebody moves a port, and disagrees with the other surface telling
	// them what to do.
	nginxUpstreamHost        = "127.0.0.1"
	nginxDefaultAPIPort      = "8080"
	nginxDefaultFrontendPort = "3000"

	// nginxBodyLimit mirrors the UPLOAD_MAX_SIZE default (2G). nginx's own
	// default is 1m, which rejects the second chunk of any real upload with a
	// 413 the api never sees — the single most common way a hand-written
	// external proxy breaks this application.
	nginxBodyLimit = "2g"

	// nginxStreamTimeout mirrors HTTP_STREAM_REQUEST_TIMEOUT's 1h default: the
	// deadline the api itself applies to resumable chunk PUTs and long media
	// reads. nginx's 60s default would cut a 2 GiB download at the edge and the
	// api would log a client disconnect for a client that never went anywhere.
	nginxStreamTimeout = "3600s"
)

// RenderNginxExternal renders the external-terminator example for an instance.
// Like RenderCaddyfile it is pure — answers in, bytes out — and deterministic:
// the same domain produces the same file, so a re-run of `vidra setup` rewrites
// it byte for byte and a diff means something changed.
//
// The only answer consulted is Answers.Domain. There is no template to read: the
// meta repo ships deploy/Caddyfile because that file is DEPLOYED and has to be
// reviewable on its own, while this one is a starting point for a configuration
// living somewhere this project never sees.
func RenderNginxExternal(a Answers, values map[string]string) ([]byte, error) {
	origin, err := normalizeOrigin(a.Domain)
	if err != nil {
		return nil, err
	}
	host := strings.TrimPrefix(origin, "https://")
	api := nginxUpstreamHost + ":" + nginxPort(values, "HTTP_PORT", nginxDefaultAPIPort)
	frontend := nginxUpstreamHost + ":" + nginxPort(values, "FRONTEND_PORT", nginxDefaultFrontendPort)
	return []byte(fmt.Sprintf(nginxExampleFormat, host, nginxBodyLimit, nginxStreamTimeout, api, frontend)), nil
}

// nginxPort reads a published-port knob out of the resolved env, falling back to
// the compose default. A value that is not a plain port number is IGNORED rather
// than written into the file: compose would reject it too, and an nginx that
// refuses to start is a worse answer than an example pointing at the default the
// operator has probably not moved. `vidra setup --check` reports the bad value
// itself, through the api's own validation.
func nginxPort(values map[string]string, key, def string) string {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 65535 {
		return def
	}
	return v
}

// nginxExampleFormat carries every comment an operator needs at the line they
// need it on. It is long, and that is the point: this file is read once, by
// somebody wiring a proxy they already own, and every question they would
// otherwise answer by guessing is a way to break uploads or leak /metrics.
const nginxExampleFormat = `# ------------------------------------------------------------------------------
# GENERATED by ` + "`vidra setup`" + ` for VIDRA_TLS_MODE=external — an EXAMPLE, not a
# deployed file. Nothing mounts or reloads it.
# ------------------------------------------------------------------------------
# Copy it into your own nginx (sites-available, a conf.d drop-in, wherever that
# proxy keeps its configuration), fill in the two TLS lines, and reload. Re-run
# ` + "`vidra setup`" + ` to regenerate it; anything written here is lost when that
# happens, so keep your edits in the copy.
#
# WHAT IT IS FOR. VIDRA_TLS_MODE=external means the managed Caddy does not run at
# all: the deploy drops it from the compose profiles, and THIS proxy is the
# instance's only front door. The routing below mirrors deploy/Caddyfile block
# for block — same four blocks, same order — because the split between the api
# and the frontend is not a preference, it is what makes PUBLIC_BASE_URL
# simultaneously correct for watch links and for OAuth/federation identity.
#
# THE PER-IP RATE LIMITS. The api keys every per-IP budget (login, password
# reset, the video-password unlock, media reads) on the nearest UNTRUSTED hop of
# X-Forwarded-For. Loopback and RFC1918/ULA/link-local addresses are trusted by
# default, so a proxy on this same host needs nothing. A terminator on a PUBLIC
# address — a cloud load balancer, a CDN edge, an nginx on another machine — is
# NOT trusted by default, and the consequence is that every visitor behind it
# shares ONE login budget and the instance starts 429ing strangers. Name that
# hop's network in the env file to fix it:
#
#   TRUSTED_PROXY_CIDRS=203.0.113.7/32,2001:db8::/32
#
# Only list networks you control. Trusting an address means believing the client
# IP it claims, so a network with anybody else on it hands them the ability to
# forge one.
#
# AFTER RELOADING, check the edge from outside and then run ` + "`vidra doctor`" + `,
# which reports what the api sees rather than what this file says.

# The WebSocket upgrade map. It belongs to the http{} context, NOT inside a
# server block — if your nginx already defines one (it often does), delete this
# and keep theirs, since a duplicate map is a configuration nginx refuses.
map $http_upgrade $connection_upgrade {
	default upgrade;
	''      close;
}

# Plain HTTP -> HTTPS. Keep /.well-known/acme-challenge/ served locally if THIS
# nginx is what renews the certificate.
server {
	listen 80;
	listen [::]:80;
	server_name %[1]s;
	return 308 https://$host$request_uri;
}

server {
	listen 443 ssl;
	listen [::]:443 ssl;
	server_name %[1]s;

	# HTTP/2 is deliberately NOT switched on for you, because the two spellings
	# are not interchangeable and the wrong one is a proxy that refuses to
	# start: nginx >= 1.25.1 wants the http2 directive below, and anything older
	# wants http2 as a parameter of the listen lines above. Check nginx -v and
	# add the one that matches.
	# http2 on;

	# ---------------------------------------------------------------------
	# TLS. THESE TWO LINES ARE PLACEHOLDERS — point them at the certificate
	# this proxy already holds for %[1]s (certbot writes them under
	# /etc/letsencrypt/live/<domain>/). Everything else in this file is
	# generated and correct as it stands.
	ssl_certificate     /etc/ssl/certs/%[1]s.crt;
	ssl_certificate_key /etc/ssl/private/%[1]s.key;

	# ---------------------------------------------------------------------
	# Upload sizing. nginx defaults to client_max_body_size 1m, which rejects
	# the second chunk of every real upload with a 413 the api never sees.
	# Match the deployment's UPLOAD_MAX_SIZE (default 2G) or exceed it.
	client_max_body_size %[2]s;
	# Do NOT buffer request bodies to disk: a 2 GiB upload would be written to
	# the proxy's temp directory in full before the api saw a byte of it, which
	# doubles the disk and breaks resumable progress entirely.
	proxy_request_buffering off;

	# The api applies HTTP_STREAM_REQUEST_TIMEOUT (default 1h) to chunk PUTs
	# and media reads; nginx's 60s default would cut them at the edge first.
	proxy_read_timeout %[3]s;
	proxy_send_timeout %[3]s;

	# What the api needs to know about the original request. Without
	# X-Forwarded-For every visitor is this proxy for rate-limiting purposes;
	# without X-Forwarded-Proto the api cannot tell an https request from an
	# http one. $proxy_add_x_forwarded_for APPENDS the peer to any inbound
	# chain, which is what the api's right-to-left walk expects.
	proxy_set_header Host              $host;
	proxy_set_header X-Real-IP         $remote_addr;
	proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
	proxy_set_header X-Forwarded-Proto $scheme;
	proxy_set_header X-Forwarded-Host  $host;
	proxy_http_version 1.1;
	proxy_set_header Upgrade    $http_upgrade;
	proxy_set_header Connection $connection_upgrade;

	# ---------------------------------------------------------------------
	# 1. Hard 404 for the Prometheus scrape.
	#
	# The api root-mounts /metrics with NO authentication when
	# METRICS_ENABLED=true. This block means flipping that switch later can
	# never publish request rates, route templates and queue depths to the
	# internet. Scrape it from inside the compose network instead.
	location = /metrics {
		return 404;
	}

	# ---------------------------------------------------------------------
	# 2. Defense in depth for the dev-only mail-capture route.
	#
	# GET /api/v1/dev/email-token returns a live password-reset token for ANY
	# address. The api already refuses to register the route in production;
	# this costs nothing and survives a refactor of that gate.
	location ^~ /api/v1/dev/ {
		return 404;
	}

	# ---------------------------------------------------------------------
	# 3. Backend surfaces -> vidra-core.
	#
	# The list is the api's root-level routes — everything it serves OUTSIDE
	# /api/v1. Several are conditional (federation discovery, the distribution
	# surfaces); routing them unconditionally is harmless, the api answers 404
	# when they are not mounted.
	#
	# NOTE on /.well-known: only the two paths the api serves are listed, NOT
	# the whole namespace — /.well-known/acme-challenge/ must stay with
	# whatever renews this proxy's certificate.
	#
	# NO gzip in these blocks. The api is the media origin: it serves HLS
	# segments, progressive MP4/M4A and original downloads with
	# Accept-Ranges/206. Compressing already-compressed media is worthless and
	# is the classic way to break Range requests and seeking.
	location ^~ /api/ {
		# Server-Sent Events (GET /api/v1/admin/jobs/events) need the response
		# streamed, not buffered; the same setting is what keeps a long media
		# read flowing instead of filling a proxy buffer.
		proxy_buffering off;
		proxy_pass http://%[4]s;
	}
	location ^~ /accounts/ {
		proxy_pass http://%[4]s;
	}
	location ^~ /video-channels/ {
		proxy_pass http://%[4]s;
	}
	location ^~ /feeds/ {
		proxy_pass http://%[4]s;
	}
	location = /healthz {
		proxy_pass http://%[4]s;
	}
	location = /readyz {
		proxy_pass http://%[4]s;
	}
	location = /version {
		proxy_pass http://%[4]s;
	}
	location = /inbox {
		proxy_pass http://%[4]s;
	}
	location = /nodeinfo/2.1 {
		proxy_pass http://%[4]s;
	}
	location = /.well-known/nodeinfo {
		proxy_pass http://%[4]s;
	}
	location = /.well-known/webfinger {
		proxy_pass http://%[4]s;
	}
	location = /services/oembed {
		proxy_pass http://%[4]s;
	}
	location = /sitemap.xml {
		proxy_pass http://%[4]s;
	}

	# ---------------------------------------------------------------------
	# 4. Everything else -> the Next.js frontend.
	#
	# The fallthrough, and the only place compression is enabled: HTML, JS,
	# CSS and the JSON payloads of server actions.
	location / {
		gzip on;
		gzip_proxied any;
		gzip_types text/plain text/css text/javascript application/javascript application/json image/svg+xml;
		gzip_min_length 1024;
		proxy_pass http://%[5]s;
	}
}
`
