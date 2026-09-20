//go:build integration

// Integration coverage for admin-configurable outbound email (migration 0151)
// against a live PostgreSQL: the real mail_config table, the real sqlc queries,
// the real settings-version counter, and a real SMTP listener that receives the
// test message. Requires migrations applied. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/httpapi/ -run TestMailConfigIntegration
package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/mailconfig"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/settingsversion"
	"github.com/vidra/vidra-core/internal/store"
)

// --- a minimal SMTP relay ------------------------------------------------------
//
// It speaks the smallest dialogue a send needs and records what arrived. There
// is deliberately no TLS: the configuration under test uses `encryption: none`
// against 127.0.0.1, which is the one shape where cleartext is legitimate (and
// the one the transport's own validation allows without credentials).

type recordingRelay struct {
	ln net.Listener

	mu       sync.Mutex
	mailFrom string
	rcptTo   []string
	data     string
	received chan struct{}
}

func newRecordingRelay(t *testing.T) *recordingRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	r := &recordingRelay{ln: ln, received: make(chan struct{}, 4)}
	go r.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return r
}

func (r *recordingRelay) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(r.ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func (r *recordingRelay) serve() {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		go r.session(conn)
	}
}

func (r *recordingRelay) session(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 relay.test ESMTP")
	inData := false
	var body strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				r.mu.Lock()
				r.data = body.String()
				r.mu.Unlock()
				body.Reset()
				write("250 queued")
				select {
				case r.received <- struct{}{}:
				default:
				}
				continue
			}
			body.WriteString(line)
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			write("250-relay.test")
			write("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			r.mu.Lock()
			r.mailFrom = strings.TrimSpace(line[len("MAIL FROM:"):])
			r.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			r.mu.Lock()
			r.rcptTo = append(r.rcptTo, strings.TrimSpace(line[len("RCPT TO:"):]))
			r.mu.Unlock()
			write("250 ok")
		case cmd == "DATA":
			inData = true
			write("354 go ahead")
		case cmd == "QUIT":
			write("221 bye")
			return
		case strings.HasPrefix(cmd, "RSET"), strings.HasPrefix(cmd, "NOOP"):
			write("250 ok")
		default:
			write("250 ok")
		}
	}
}

func (r *recordingRelay) awaitMessage(t *testing.T) (from string, rcpt []string, data string) {
	t.Helper()
	select {
	case <-r.received:
	case <-time.After(10 * time.Second):
		t.Fatal("no message reached the relay")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mailFrom, append([]string(nil), r.rcptTo...), r.data
}

// --- the harness ---------------------------------------------------------------

type mailIntegrationEnv struct {
	store    *store.Store
	srv      *Server
	svc      *mailconfig.Service
	cipher   *secretbox.Cipher
	logs     *bytes.Buffer
	adminTok string
	modTok   string
	relay    *recordingRelay
	envHost  string
	envPort  int
}

func newKEK(t *testing.T) *secretbox.Cipher {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("kek: %v", err)
	}
	c, err := secretbox.NewCipher(kek)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return c
}

func setupMailIntegration(t *testing.T) *mailIntegrationEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	// The document is a SINGLETON, so a rerun against a shared dev database must
	// start from a known state and leave one behind.
	clean := func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		if _, err := st.Pool.Exec(cctx, `DELETE FROM mail_config`); err != nil {
			t.Errorf("clean mail_config: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)

	relay := newRecordingRelay(t)
	envHost, envPort := relay.hostPort(t)

	cipher := newKEK(t)
	svc := mailconfig.NewService(st.Queries(), cipher, mailconfig.EnvConfig{
		// An ENVIRONMENT relay that is genuinely reachable, so the DELETE case
		// can prove the revert delivers rather than merely re-labelling.
		Enabled: true, Host: envHost, Port: envPort, From: "env@vidra.test",
		InstanceName: "Vidra Test",
	}, mailconfig.WithVersionBump(settingsversion.BumpFunc(st.Queries())))
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load mail config: %v", err)
	}

	var logs bytes.Buffer
	cfg := testConfig()
	cfg.InstanceContactEmail = "ops@example.test"
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(st.Queries(), issuer, 720*time.Hour)
	// ONE composer over the service resolver — the production wiring.
	composer := mail.NewComposer(mail.ComposerConfig{InstanceName: cfg.InstanceName}, svc)
	srv := New(cfg, st, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithMailConfigService(svc),
		WithContactMailer(composer),
		WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)

	suffix := uuid.New().String()[:8]
	admin := registerTokens(t, srv, fmt.Sprintf(
		`{"username":"mc-adm-%s","email":"mc-adm-%s@example.test","password":"supersecret"}`, suffix, suffix))
	mod := registerTokens(t, srv, fmt.Sprintf(
		`{"username":"mc-mod-%s","email":"mc-mod-%s@example.test","password":"supersecret"}`, suffix, suffix))
	for _, id := range []string{admin.User.ID, mod.User.ID} {
		uid := uuid.MustParse(id)
		t.Cleanup(func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer ccancel()
			if _, err := st.Pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
	}
	// The role is read off the account ROW on every request, so a direct update
	// is exactly what a promotion through the admin route leaves behind.
	//
	// BOTH accounts are promoted explicitly. registerTokens hands back an admin
	// only on an EMPTY instance (the first-run owner claim); in the integration
	// lane other tests have already registered users on the shared database, so
	// "mc-adm" arrives as a plain user and every PUT/DELETE answered 403 — the
	// suite passed only when it happened to be the first registrant.
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, uuid.MustParse(admin.User.ID)); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET role = 'moderator' WHERE id = $1`, uuid.MustParse(mod.User.ID)); err != nil {
		t.Fatalf("promote moderator: %v", err)
	}

	return &mailIntegrationEnv{
		store: st, srv: srv, svc: svc, cipher: cipher, logs: &logs,
		adminTok: admin.Token, modTok: mod.Token,
		relay: relay, envHost: envHost, envPort: envPort,
	}
}

// smtpConfigBody is a document pointed at the recording relay. `none` +
// anonymous is the one cleartext shape the transport allows, and it is correct
// here: the relay is on loopback.
func (e *mailIntegrationEnv) smtpConfigBody(password string) string {
	secret := ""
	if password != "" {
		secret = `,"password":"` + password + `"`
	}
	return fmt.Sprintf(
		`{"transport":"smtp","from_address":"no-reply@vidra.test","from_name":"Vidra","smtp":{"host":%q,"port":%d,"username":"","encryption":"none"%s}}`,
		e.envHost, e.envPort, secret)
}

func (e *mailIntegrationEnv) state(t *testing.T) mailconfig.State {
	t.Helper()
	rec := getWithAuth(e.srv, mailConfigPath, e.adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mail-config = %d; body=%s", rec.Code, rec.Body.String())
	}
	var st mailconfig.State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	return st
}

func (e *mailIntegrationEnv) instanceMailFeature(t *testing.T) bool {
	t.Helper()
	rec := getWithAuth(e.srv, "/api/v1/instance", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /instance = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Features struct {
			Mail bool `json:"mail"`
		} `json:"features"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal instance: %v", err)
	}
	return body.Features.Mail
}

// --- (a) PUT then a test send that actually arrives ----------------------------

// TestMailConfigIntegrationSavedTransportDelivers is the end-to-end claim the
// whole feature rests on: a transport saved through the admin API, against a
// real database, is the transport a message actually goes out over — with no
// restart between the two.
func TestMailConfigIntegrationSavedTransportDelivers(t *testing.T) {
	env := setupMailIntegration(t)

	rec := doJSON(env.srv, http.MethodPut, mailConfigPath, env.adminTok, env.smtpConfigBody(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	if st := env.state(t); st.Source != mailconfig.SourceDatabase {
		t.Fatalf("source = %q, want database", st.Source)
	}

	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/admin/mail/test", env.adminTok, `{}`); rec.Code != http.StatusAccepted {
		t.Fatalf("mail test = %d; body=%s", rec.Code, rec.Body.String())
	}
	from, rcpt, data := env.relay.awaitMessage(t)
	if !strings.Contains(from, "no-reply@vidra.test") {
		t.Errorf("MAIL FROM = %q, want the configured sender", from)
	}
	if len(rcpt) != 1 || !strings.Contains(rcpt[0], "ops@example.test") {
		t.Errorf("RCPT TO = %v, want the instance contact address and nothing else", rcpt)
	}
	if !strings.Contains(data, "Test message from") {
		t.Errorf("the relay did not receive the test message body:\n%s", data)
	}
}

// --- (b) a second Service picks the change up through the counter ---------------

// TestMailConfigIntegrationSecondProcessPicksUpTheChange is the CROSS-PROCESS
// path: every api and worker process holds its own resolved transport, so a
// save on one replica must reach the others or half the fleet keeps sending
// through the old relay with nothing failing and nothing logged.
func TestMailConfigIntegrationSecondProcessPicksUpTheChange(t *testing.T) {
	env := setupMailIntegration(t)

	// A second service over the SAME database, primed the way a replica is at
	// boot, and polling the same counter.
	other := mailconfig.NewService(env.store.Queries(), env.cipher, mailconfig.EnvConfig{
		Enabled: true, Host: env.envHost, Port: env.envPort, From: "env@vidra.test",
	})
	ctx := context.Background()
	if err := other.Load(ctx); err != nil {
		t.Fatalf("second service load: %v", err)
	}
	poller := settingsversion.New(env.store.Queries(), time.Second,
		settingsversion.Cache{Name: "mail config", Reload: other.Load})
	if err := poller.Prime(ctx); err != nil {
		t.Fatalf("prime: %v", err)
	}
	if got := other.Source(); got != mailconfig.SourceEnvironment {
		t.Fatalf("the second process started on %q, want environment", got)
	}

	if rec := doJSON(env.srv, http.MethodPut, mailConfigPath, env.adminTok, env.smtpConfigBody("")); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Before the poll it is still on the old configuration — which is the point:
	// what closes the gap is the counter, not shared memory.
	if got := other.Source(); got != mailconfig.SourceEnvironment {
		t.Fatalf("the second process changed without polling (%q) — this test would prove nothing", got)
	}
	changed, err := poller.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !changed {
		t.Fatal("the settings-version counter did not move; a save on one replica is invisible to the rest")
	}
	if got := other.Source(); got != mailconfig.SourceDatabase {
		t.Errorf("the second process is on %q after the poll, want database", got)
	}
	st := other.Status()
	if st.Config == nil || st.Config.SMTP.Host != env.envHost {
		t.Errorf("the second process resolved %+v, want the saved relay", st.Config)
	}

	// And the reset propagates the same way.
	if rec := doJSON(env.srv, http.MethodDelete, mailConfigPath, env.adminTok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := poller.Tick(ctx); err != nil {
		t.Fatalf("tick after reset: %v", err)
	}
	if got := other.Source(); got != mailconfig.SourceEnvironment {
		t.Errorf("the second process is on %q after a reset, want environment", got)
	}
}

// --- (c) a wrong KEK is a reported state, not an error exit --------------------

// TestMailConfigIntegrationWrongKEKIsUndecryptableNotAnError covers the KEK
// rotation. Loading must not fail (a failed Load leaves the previous snapshot in
// place on every poll and hides the breakage), the instance must stay
// CONFIGURED (falling back to the environment relay would redirect this
// instance's password resets), and the GET must say so.
func TestMailConfigIntegrationWrongKEKIsUndecryptableNotAnError(t *testing.T) {
	env := setupMailIntegration(t)

	const sentinel = "SENTINEL-INTEGRATION-RELAY-PASSWORD-5a9e"
	body := fmt.Sprintf(
		`{"transport":"smtp","from_address":"no-reply@vidra.test","from_name":"Vidra","smtp":{"host":%q,"port":%d,"username":"mailer","encryption":"starttls","password":%q}}`,
		"relay.example.test", 587, sentinel)
	if rec := doJSON(env.srv, http.MethodPut, mailConfigPath, env.adminTok, body); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}

	// The stored column holds ciphertext, not the credential.
	var stored string
	if err := env.store.Pool.QueryRow(context.Background(), `SELECT secret FROM mail_config WHERE id`).Scan(&stored); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if strings.Contains(stored, sentinel) || !secretbox.IsSealed(stored) {
		t.Fatalf("the credential is not sealed at rest: %q", stored)
	}

	// A process with a DIFFERENT KEK over the same row.
	rotated := mailconfig.NewService(env.store.Queries(), newKEK(t), mailconfig.EnvConfig{
		Enabled: true, Host: env.envHost, Port: env.envPort, From: "env@vidra.test",
	})
	if err := rotated.Load(context.Background()); err != nil {
		t.Fatalf("Load with a rotated KEK returned an error; it must report the state: %v", err)
	}
	st := rotated.Status()
	if st.SecretStatus != mailconfig.SecretStatusUndecryptable {
		t.Errorf("secret_status = %q, want undecryptable", st.SecretStatus)
	}
	if st.Source != mailconfig.SourceDatabase || !rotated.Available() {
		t.Errorf("state = %+v, available=%v — the instance is configured-but-broken, not unconfigured", st, rotated.Available())
	}

	// The GET on a server wired to THAT process reports it.
	var logs bytes.Buffer
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	brokenSrv := New(testConfig(), env.store, nil,
		WithAuthService(auth.NewService(env.store.Queries(), issuer, 720*time.Hour), 15*time.Minute),
		WithMailConfigService(rotated),
		WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)
	rec := getWithAuth(brokenSrv, mailConfigPath, env.adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET on the rotated-KEK process = %d (a 500 here is the bug this closes); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), mailconfig.SecretStatusUndecryptable) {
		t.Errorf("the GET does not report the undecryptable state:\n%s", rec.Body.String())
	}
	// And the sentinel is nowhere: not in either response, not in the logs.
	for name, body := range map[string]string{
		"the rotated-KEK GET": rec.Body.String(),
		"the healthy GET":     getWithAuth(env.srv, mailConfigPath, env.adminTok).Body.String(),
		"the server log":      logs.String(),
		"the harness log":     env.logs.String(),
	} {
		if strings.Contains(body, sentinel) {
			t.Errorf("%s leaks the stored credential:\n%s", name, body)
		}
	}
	// Nor in any audit row the save wrote.
	assertNoAuditRowCarries(t, env, sentinel)
}

// assertNoAuditRowCarries reads back every persisted audit row and fails if the
// sentinel appears in any of them. audit_log is the ledger an operator exports;
// a credential in it is a credential in every backup of it.
func assertNoAuditRowCarries(t *testing.T, env *mailIntegrationEnv, sentinel string) {
	t.Helper()
	rows, err := env.store.Pool.Query(context.Background(),
		`SELECT action, coalesce(reason,''), coalesce(metadata::text,'') FROM audit_log WHERE action LIKE 'admin.mail%'`)
	if err != nil {
		// The audit log is written best-effort and this server wires no audit
		// service, so an empty or absent set is not itself a failure — the log
		// capture above is the assertion that carries the weight.
		t.Logf("audit_log not readable (%v); the log-capture assertion still stands", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var action, reason, metadata string
		if err := rows.Scan(&action, &reason, &metadata); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		if strings.Contains(reason, sentinel) || strings.Contains(metadata, sentinel) {
			t.Errorf("audit row %q carries the credential: reason=%q metadata=%q", action, reason, metadata)
		}
	}
}

// --- (d) DELETE reverts to the environment -------------------------------------

func TestMailConfigIntegrationResetRevertsToTheEnvironment(t *testing.T) {
	env := setupMailIntegration(t)

	if rec := doJSON(env.srv, http.MethodPut, mailConfigPath, env.adminTok, env.smtpConfigBody("")); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	if st := env.state(t); st.Source != mailconfig.SourceDatabase {
		t.Fatalf("source after PUT = %q, want database", st.Source)
	}

	if rec := doJSON(env.srv, http.MethodDelete, mailConfigPath, env.adminTok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d; body=%s", rec.Code, rec.Body.String())
	}
	st := env.state(t)
	if st.Source != mailconfig.SourceEnvironment {
		t.Errorf("source after DELETE = %q, want environment", st.Source)
	}
	if st.Config != nil {
		t.Errorf("config after DELETE = %+v, want null", st.Config)
	}
	// The row is gone from the table, not merely hidden.
	var n int
	if err := env.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM mail_config`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("mail_config still holds %d row(s) after a reset", n)
	}
	// And the environment path DELIVERS — the revert is a working configuration,
	// not a label.
	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/admin/mail/test", env.adminTok, `{}`); rec.Code != http.StatusAccepted {
		t.Fatalf("mail test after reset = %d; body=%s", rec.Code, rec.Body.String())
	}
	if _, _, data := env.relay.awaitMessage(t); !strings.Contains(data, "Test message from") {
		t.Error("the environment relay received no message after the reset")
	}
	if ev := findAudit(auditEvents(t, env.logs), observability.ActionAdminMailConfigReset, observability.ResultSuccess); ev == nil {
		t.Error("the reset was not audited")
	}
}

// --- (e) features.mail flips with no restart -----------------------------------

// TestMailConfigIntegrationInstanceFeatureFlipsLive is the live-capability
// claim. Before this change "can this instance send email" was a BOOT fact in
// three places, so an admin who configured mail got a working mailer and a
// product that still believed it had none.
func TestMailConfigIntegrationInstanceFeatureFlipsLive(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	env := setupMailIntegration(t)

	// A server whose service has NO environment relay, so the starting answer
	// is an honest "this instance cannot send email".
	ctx := context.Background()
	bare := mailconfig.NewService(env.store.Queries(), env.cipher, mailconfig.EnvConfig{})
	if err := bare.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	srv := New(testConfig(), env.store, nil,
		WithAuthService(auth.NewService(env.store.Queries(), issuer, 720*time.Hour), 15*time.Minute),
		WithMailConfigService(bare),
		WithContactMailer(mail.NewComposer(mail.ComposerConfig{}, bare)),
	)
	live := &mailIntegrationEnv{store: env.store, srv: srv, svc: bare, adminTok: env.adminTok,
		envHost: env.envHost, envPort: env.envPort}

	before := live.instanceMailFeature(t)
	if before {
		t.Fatal("features.mail is true before anything is configured")
	}
	beforeETag := getWithAuth(srv, "/api/v1/instance", "").Header().Get("ETag")

	if rec := doJSON(srv, http.MethodPut, mailConfigPath, env.adminTok, live.smtpConfigBody("")); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	if !live.instanceMailFeature(t) {
		t.Error("features.mail is still false after a transport was configured — the capability is still a boot fact")
	}
	// The ETag is a hash of the SERIALIZED document, so a flipped flag moves it
	// by construction. Asserted anyway: a future refactor that computed it from
	// a narrower set of inputs would hand every client a 304 and freeze the old
	// answer in their cache, which is the same bug one layer out.
	afterETag := getWithAuth(srv, "/api/v1/instance", "").Header().Get("ETag")
	if beforeETag == "" || afterETag == "" || beforeETag == afterETag {
		t.Errorf("ETag did not move across the flip (%q -> %q); a cached /instance would keep serving features.mail=false", beforeETag, afterETag)
	}
	// A conditional request with the OLD tag must not be answered 304.
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	req.Header.Set("If-None-Match", beforeETag)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusNotModified {
		t.Error("the pre-flip ETag still validates; clients would cache features.mail=false indefinitely")
	}
}

// --- (f) moderator refusal, against the real store -----------------------------

func TestMailConfigIntegrationModeratorIsRefused(t *testing.T) {
	env := setupMailIntegration(t)
	for _, verb := range []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, env.smtpConfigBody("")},
		{http.MethodDelete, ""},
	} {
		if rec := doJSON(env.srv, verb.method, mailConfigPath, env.modTok, verb.body); rec.Code != http.StatusForbidden {
			t.Errorf("moderator %s = %d, want 403; body=%s", verb.method, rec.Code, rec.Body.String())
		}
		if rec := doJSON(env.srv, verb.method, mailConfigPath, "", verb.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d, want 401; body=%s", verb.method, rec.Code, rec.Body.String())
		}
	}
	// Nothing was written.
	var n int
	if err := env.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM mail_config`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("mail_config holds %d row(s) after refused writes", n)
	}
}
