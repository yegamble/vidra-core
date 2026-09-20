// Package mailconfig owns the instance's outbound-mail configuration: the
// admin-panel document, the credential sealed at rest, and the transport the
// mailer resolves for every send.
//
// WHY IT IS NOT AN INSTANCE SETTING. internal/instancesettings is the registry
// for admin-editable configuration, and its doctrine is explicit: secrets never
// live in it (migration 0065 says so on the record), every value is TEXT, GET
// echoes stored values back, and the audit trail records key names. An SMTP
// password or a provider API key satisfies none of those. So mail gets a
// dedicated single-row document (migration 0151) with a write-only secret
// column and endpoints that report only whether a credential is stored — the
// same shape the IPFS control document uses for a configuration that must be
// saved whole or not at all.
//
// PRECEDENCE: the database document, then the environment (MAIL_ENABLED +
// SMTP_*), then nothing. DELETE removes the document and the environment path
// takes over again, behaving byte-for-byte as it did before this package
// existed. DEV_MAIL_CAPTURE_ENABLED wins over all of it, because on a developer
// machine nothing is delivered at all and any other answer on the admin page
// would be a lie.
//
// RUNTIME, NOT BOOT. The mailer holds this service as its mail.Resolver and
// asks it per send, so an admin who configures a transport does not restart
// anything. Other replicas learn about the write through the settings-version
// counter (internal/settingsversion), which calls Load within one poll interval
// — the same mechanism the instance-settings overlay, the documents and the
// branding images already ride.
package mailconfig

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrSecretsKeyMissing means a save carried a credential on a deployment with
// no KEK to seal it with. It is refused rather than stored in the clear:
// "secrets are stored sealed or not at all" is the whole reason this table may
// hold one. The endpoint renders it as a typed 409 so the panel can say which
// variable to set instead of showing a generic failure.
var ErrSecretsKeyMissing = errors.New("mailconfig: no key-encryption key is configured, so a credential cannot be stored")

// probeTTL bounds how often /admin/system actually touches the transport.
//
// The page is a poll target and the probe is now an OUTBOUND NETWORK CALL — a
// relay handshake, or a GET against a vendor API that counts toward the
// account's rate limit. Without a cache, two admins with the page open would
// hammer a provider's API on every refresh until the key is throttled, which
// surfaces days later as "password resets stopped working". One minute is short
// enough that an operator fixing a relay sees the change on their next couple of
// refreshes, and long enough that the page cannot be turned into a load
// generator. Every Load (boot, a save, a reset, another replica's write)
// invalidates it, so a configuration CHANGE is never masked by the cache.
const probeTTL = time.Minute

// Repository is the database access this service needs. *sqlcgen.Queries
// satisfies it; tests substitute an in-memory fake.
type Repository interface {
	GetMailConfig(ctx context.Context) (sqlcgen.MailConfig, error)
	UpsertMailConfig(ctx context.Context, arg sqlcgen.UpsertMailConfigParams) (sqlcgen.MailConfig, error)
	DeleteMailConfig(ctx context.Context) (int64, error)
}

// EnvConfig is the deployment's environment-configured mail path
// (MAIL_ENABLED + SMTP_*), which the document overrides and a DELETE reverts
// to. It is passed in rather than read from config so this package has no
// dependency on the config loader.
type EnvConfig struct {
	Enabled       bool
	Host          string
	Port          int
	Username      string
	Password      string
	From          string
	InstanceName  string
	PublicBaseURL string
}

func (e EnvConfig) configured() bool {
	return e.Enabled && strings.TrimSpace(e.Host) != "" && strings.TrimSpace(e.From) != ""
}

// Option customises the service.
type Option func(*Service)

// WithVersionBump wires the cross-replica invalidation counter. Save and Reset
// advance it so every other process reloads within one poll interval; without
// it a change takes effect only on the replica that served the write.
func WithVersionBump(bump func(context.Context) error) Option {
	return func(s *Service) { s.bump = bump }
}

// WithDevCapture declares that DEV_MAIL_CAPTURE_ENABLED is on. The capture
// mailer is installed by cmd/api and wins over whatever this service resolves,
// so the service does not deliver anything differently — it reports the truth
// on the admin surfaces, and counts as an outbound path for the live capability.
func WithDevCapture(on bool) Option {
	return func(s *Service) { s.devCapture = on }
}

// Service owns the configuration document and the transport it resolves to.
// Safe for concurrent use: reads go through one atomic pointer, so a send never
// blocks on a save.
type Service struct {
	repo       Repository
	cipher     *secretbox.Cipher
	bump       func(context.Context) error
	env        EnvConfig
	envKind    mail.Transport
	devCapture bool

	cur atomic.Pointer[snapshot]

	probeMu   sync.Mutex
	probeAt   time.Time
	probeSeen *ProbeReport
}

// snapshot is one consistent view of "where mail goes right now". It is
// replaced wholesale by Load, never mutated.
type snapshot struct {
	source       string
	doc          *Document
	transport    mail.Transport
	from         netmail.Address
	replyTo      string
	secretStatus string
	// smtpHost is the relay the active transport dials, "" for the HTTPS
	// providers. The status page needs it to decide whether an unencrypted
	// session is worth warning about (a relay on loopback is not).
	smtpHost  string
	updatedAt string
}

// NewService builds the service. cipher may be nil (a deployment with no KEK):
// reads still work and an anonymous SMTP relay can still be saved, but a save
// carrying a credential is refused with ErrSecretsKeyMissing rather than
// storing one in the clear.
//
// Nothing is read from the database here. Call Load once at boot, after which
// the poller keeps it current.
func NewService(repo Repository, cipher *secretbox.Cipher, env EnvConfig, opts ...Option) *Service {
	s := &Service{repo: repo, cipher: cipher, env: env}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if env.configured() {
		s.envKind = mail.NewEnvironmentTransport(mail.Config{
			Host:          env.Host,
			Port:          env.Port,
			Username:      env.Username,
			Password:      env.Password,
			From:          env.From,
			InstanceName:  env.InstanceName,
			PublicBaseURL: env.PublicBaseURL,
		})
	}
	s.cur.Store(s.environmentSnapshot())
	return s
}

// environmentSnapshot is the state with no document: the environment path when
// one is configured, otherwise nothing at all.
func (s *Service) environmentSnapshot() *snapshot {
	if s.envKind == nil {
		return &snapshot{source: SourceNone, secretStatus: SecretStatusNone}
	}
	return &snapshot{
		source:       SourceEnvironment,
		transport:    s.envKind,
		from:         netmail.Address{Address: s.env.From},
		secretStatus: SecretStatusNone,
		smtpHost:     s.env.Host,
	}
}

// Load reads the document and rebuilds the resolved transport. It is the boot
// loader AND the settingsversion.Cache reload hook, so a write on any replica
// reaches this one through exactly the same code path a restart would take.
//
// A row whose credential cannot be unsealed is NOT an error: see
// SecretStatusUndecryptable. Returning an error here would leave the previous
// snapshot in place on every poll and hide the breakage entirely.
func (s *Service) Load(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	row, err := s.repo.GetMailConfig(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.replace(s.environmentSnapshot())
			return nil
		}
		return fmt.Errorf("mailconfig: load: %w", err)
	}
	s.replace(s.snapshotFor(row))
	return nil
}

// snapshotFor turns one stored row into the state the resolver serves.
func (s *Service) snapshotFor(row sqlcgen.MailConfig) *snapshot {
	settings := decodeSettings(row.Settings)
	hasSecret := row.Secret != ""
	snap := &snapshot{
		source:       SourceDatabase,
		doc:          settings.document(row.Transport, row.FromAddress, row.FromName, row.ReplyTo, hasSecret),
		from:         netmail.Address{Name: row.FromName, Address: row.FromAddress},
		replyTo:      row.ReplyTo,
		secretStatus: SecretStatusNone,
		smtpHost:     settings.Host,
		updatedAt:    row.UpdatedAt.UTC().Format(time.RFC3339),
	}

	secret := ""
	if hasSecret {
		plain, err := s.open(row.Secret)
		if err != nil {
			// Configured, and broken. The transport is a stub that fails every
			// send with the typed reason, so the capability stays true (mail IS
			// configured), the admin page says exactly what is wrong, and
			// nothing silently falls back to the environment relay — which
			// would send this instance's password resets somewhere the operator
			// did not choose.
			snap.secretStatus = SecretStatusUndecryptable
			snap.transport = brokenTransport{kind: row.Transport}
			return snap
		}
		snap.secretStatus = SecretStatusOK
		secret = plain
	}

	transport, err := mail.NewTransport(row.Transport, settings.transportSettings(), secret)
	if err != nil {
		// A stored document that no longer validates (a hand-edited row, or a
		// rule tightened in a later release). Same treatment as an unopenable
		// secret: configured, failing loudly, never silently redirected.
		snap.transport = brokenTransport{kind: row.Transport, reason: mail.ReasonRejected}
		return snap
	}
	snap.transport = transport
	return snap
}

// replace installs a new snapshot and drops the cached probe: a configuration
// change must never be masked by a status the previous transport produced.
func (s *Service) replace(next *snapshot) {
	s.cur.Store(next)
	s.probeMu.Lock()
	s.probeSeen = nil
	s.probeAt = time.Time{}
	s.probeMu.Unlock()
}

func (s *Service) open(sealed string) (string, error) {
	if !secretbox.IsSealed(sealed) {
		// The service never writes an unsealed secret, so a raw value is a row
		// this code did not produce. Refusing it is the fail-closed half of
		// "sealed or not at all".
		return "", errors.New("mailconfig: stored credential is not sealed")
	}
	if s.cipher == nil {
		return "", errors.New("mailconfig: no key-encryption key is configured")
	}
	plain, err := s.cipher.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Current implements mail.Resolver: the transport, sender and default reply-to
// for THIS send. ok=false means the instance has no mail path, which the
// composer answers exactly as the historical no-op mailer did.
func (s *Service) Current() (mail.Transport, netmail.Address, string, bool) {
	snap := s.cur.Load()
	if snap == nil || snap.transport == nil {
		return nil, netmail.Address{}, "", false
	}
	return snap.transport, snap.from, snap.replyTo, true
}

// Available is THE live "can this instance send email" read.
//
// It replaces three independent BOOT facts that used to answer the same
// question — the wired contact mailer, cmd/api's mailWired closure behind the
// registration email-verification gate, and cfg.MailEnabled on the status and
// infrastructure pages. They were frozen at process start, so an admin who
// configured mail from the panel got a working mailer and a product that still
// believed it had none: the verification gate stayed ineffective and the pages
// still said "not configured".
//
// The dev capture seam counts. It is a real outbound path — it is how the local
// and e2e stacks "send" — and answering false there would 503 the mail test
// button on every developer machine.
func (s *Service) Available() bool {
	if s.devCapture {
		return true
	}
	snap := s.cur.Load()
	return snap != nil && snap.transport != nil
}

// Source names where the active mail path comes from (see the Source
// constants). Dev capture wins, because on that deployment nothing is delivered.
func (s *Service) Source() string {
	if s.devCapture {
		return SourceDevCapture
	}
	snap := s.cur.Load()
	if snap == nil {
		return SourceNone
	}
	return snap.source
}

// Status is the whole GET body. It reads one atomic pointer and touches nothing
// else, so the admin page costs no database round trip.
func (s *Service) Status() State {
	snap := s.cur.Load()
	if snap == nil {
		snap = &snapshot{source: SourceNone, secretStatus: SecretStatusNone}
	}
	return State{
		Source:           s.Source(),
		SecretsAvailable: s.cipher != nil,
		SecretStatus:     snap.secretStatus,
		Environment: Environment{
			Configured: s.env.configured(),
			Host:       s.envField(s.env.Host),
			Port:       s.envPort(),
			From:       s.envField(s.env.From),
		},
		Config:    snap.doc,
		UpdatedAt: snap.updatedAt,
	}
}

// envField and envPort report the environment relay's coordinates only when one
// is actually configured. The SMTP USERNAME and PASSWORD are never reported
// anywhere — they are on this repository's absolute never-list.
func (s *Service) envField(v string) string {
	if !s.env.configured() {
		return ""
	}
	return strings.TrimSpace(v)
}

func (s *Service) envPort() int {
	if !s.env.configured() {
		return 0
	}
	return s.env.Port
}

// SaveResult is what an audit record needs and nothing more: the transport, the
// dotted NAMES of the fields that changed, and whether the credential was
// replaced. No value ever leaves this struct.
type SaveResult struct {
	Transport     string
	ChangedFields []string
	SecretChanged bool
}

// Save validates the WHOLE document, seals the credential, writes the row,
// announces the change to the other replicas and reloads this one.
//
// Validation is mail.NewTransport's, not a second copy: building the transport
// IS the check, so a configuration that saves is a configuration that resolves,
// and the 422 field paths a caller gets are the same ones the transport would
// have complained about. Returns *mail.ValidationError for a bad document and
// ErrSecretsKeyMissing when a credential arrives with no KEK to seal it.
func (s *Service) Save(ctx context.Context, in Input, actor uuid.UUID) (SaveResult, error) {
	if s.repo == nil {
		return SaveResult{}, errors.New("mailconfig: no repository is wired")
	}
	kind := strings.TrimSpace(in.Transport)
	bad := validateIdentity(in)

	// The row as it stands in the DATABASE, not as this process cached it: a
	// save must not decide "the transport is unchanged, keep the secret" from a
	// snapshot another replica has already superseded.
	prior, priorErr := s.repo.GetMailConfig(ctx)
	hasPrior := priorErr == nil
	if priorErr != nil && !errors.Is(priorErr, pgx.ErrNoRows) {
		return SaveResult{}, fmt.Errorf("mailconfig: read current: %w", priorErr)
	}

	secretField := mail.SecretField(kind)
	secret, keep := "", false
	if secretField == "" {
		bad = append(bad, mail.FieldError{
			Field: "transport",
			Msg:   "unknown transport (want one of " + strings.Join(mail.Kinds(), ", ") + ")",
		})
	} else {
		value, present := in.secret(kind)
		switch {
		case present && value != "":
			secret = value
		case present && value == "":
			// An explicit clear. Only SMTP has a shape where no credential is
			// correct; for a provider, "no API key" is not a configuration.
			if kind != mail.KindSMTP {
				bad = append(bad, mail.FieldError{Field: secretField, Msg: "required"})
			}
		case hasPrior && prior.Transport == kind && prior.Secret != "":
			// Omitted, same transport: keep what is stored — but ONLY if the
			// credential would still go to the same place.
			//
			// The sealed column and the write-only API exist so that an admin
			// SESSION cannot read the credential back. Keeping the secret across
			// a changed smtp.host hands it back anyway, one AUTH PLAIN at a time:
			// point the host at a relay you control, omit the password, and the
			// next send (or the admin test button) delivers the stored password —
			// frequently a third-party account, an SES/SendGrid/Mailgun SMTP
			// credential or a Workspace app password, whose blast radius is not
			// this instance. A changed server address therefore costs the
			// password again, exactly like a changed transport does.
			//
			// Only the ADDRESS counts. Username, port and encryption travel to
			// the same relay, and the vendor transports pin their own hosts, so
			// Mailgun's domain/region change nothing about where the key goes.
			if kind == mail.KindSMTP && !sameHost(decodeSettings(prior.Settings).Host, in.settings(kind).Host) {
				bad = append(bad, mail.FieldError{
					Field: secretField,
					Msg:   "required: changing the server address requires the password again",
				})
				break
			}
			// It is unsealed here and re-sealed below rather than copied as
			// ciphertext, so a save that succeeds has PROVEN the stored
			// credential is readable — otherwise an admin could keep saving a
			// configuration that can never send.
			plain, err := s.open(prior.Secret)
			if err != nil {
				bad = append(bad, mail.FieldError{
					Field: secretField,
					Msg:   "the stored credential cannot be read (the key-encryption key changed); enter it again",
				})
				break
			}
			secret, keep = plain, true
		case kind == mail.KindSMTP:
			// Omitted with nothing stored for this transport: an anonymous
			// relay, which is a real shape (a local postfix, a LAN smarthost).
			// The username check below is what stops it being a silent
			// downgrade when credentials WERE intended.
		default:
			// A new provider, or a first configuration. The stored credential
			// (if any) belongs to the transport it was typed for, so there is
			// nothing to keep.
			bad = append(bad, mail.FieldError{Field: secretField, Msg: "required"})
		}
	}

	if kind == mail.KindSMTP && secret == "" && strings.TrimSpace(smtpUsername(in)) != "" && !reported(bad, "smtp.password") {
		// AUTH PLAIN with an empty password is not "anonymous", it is an
		// authenticated session with a blank credential — which relays reject
		// and which reads to the operator as a wrong password. mail.NewTransport
		// catches the mirror image (a password with no username); this is the
		// half only this layer can see, because it is the one the three-way
		// secret semantics can produce by OMISSION.
		//
		// Skipped when the branch above already refused the password: a form
		// that binds errors by field name would render two messages on one
		// input, and the second would be the less specific of the two.
		bad = append(bad, mail.FieldError{Field: "smtp.password", Msg: "required when a username is set"})
	}

	if len(bad) > 0 {
		return SaveResult{}, &mail.ValidationError{Fields: bad}
	}

	settings := in.settings(kind)
	if _, err := mail.NewTransport(kind, settings.transportSettings(), secret); err != nil {
		return SaveResult{}, err
	}

	sealed := ""
	if secret != "" {
		if s.cipher == nil {
			return SaveResult{}, ErrSecretsKeyMissing
		}
		var err error
		if sealed, err = s.cipher.Seal([]byte(secret)); err != nil {
			return SaveResult{}, fmt.Errorf("mailconfig: seal credential: %w", err)
		}
	}

	encoded, err := settings.encode()
	if err != nil {
		return SaveResult{}, fmt.Errorf("mailconfig: encode settings: %w", err)
	}

	from := strings.TrimSpace(in.FromAddress)
	replyTo := strings.TrimSpace(in.ReplyTo)
	before := map[string]string{}
	if hasPrior {
		before = auditableFields(prior.Transport, prior.FromAddress, prior.FromName, prior.ReplyTo, decodeSettings(prior.Settings))
	}
	priorSealed := ""
	if hasPrior {
		priorSealed = prior.Secret
	}
	result := SaveResult{
		Transport:     kind,
		ChangedFields: changedFields(before, auditableFields(kind, from, in.FromName, replyTo, settings)),
		// `keep` means the stored credential was re-sealed unchanged, which is
		// not a change an audit row should claim. Everything else compares the
		// stored envelope: a fresh seal of the SAME password reads as changed
		// (each Seal uses a new nonce), which is the honest reading — the
		// credential was re-entered and rewritten — while no-secret to
		// no-secret correctly reads as unchanged.
		SecretChanged: !keep && priorSealed != sealed,
	}

	if _, err := s.repo.UpsertMailConfig(ctx, sqlcgen.UpsertMailConfigParams{
		Transport:   kind,
		FromAddress: from,
		FromName:    in.FromName,
		ReplyTo:     replyTo,
		Settings:    encoded,
		Secret:      sealed,
		UpdatedBy:   pgconv.UUID(actor),
	}); err != nil {
		return SaveResult{}, fmt.Errorf("mailconfig: save: %w", err)
	}
	if err := s.announce(ctx); err != nil {
		return result, err
	}
	return result, s.Load(ctx)
}

// Reset removes the document so the instance reverts to its environment
// configuration. It reports whether there was one to remove, so an audit record
// can tell a real reset from a no-op.
func (s *Service) Reset(ctx context.Context, _ uuid.UUID) (bool, error) {
	if s.repo == nil {
		return false, errors.New("mailconfig: no repository is wired")
	}
	n, err := s.repo.DeleteMailConfig(ctx)
	if err != nil {
		return false, fmt.Errorf("mailconfig: reset: %w", err)
	}
	if err := s.announce(ctx); err != nil {
		return n > 0, err
	}
	return n > 0, s.Load(ctx)
}

// announce advances the settings-version counter so every other replica
// reloads. A single-process install (and every unit test) has no bumper wired,
// which is not an error: there is nobody else to tell.
func (s *Service) announce(ctx context.Context) error {
	if s.bump == nil {
		return nil
	}
	if err := s.bump(ctx); err != nil {
		return fmt.Errorf("mailconfig: announce change: %w", err)
	}
	return nil
}

// ProbeReport is what /admin/system needs: which route was probed, what the
// probe found, and whether a verified session was nonetheless CLEARTEXT.
type ProbeReport struct {
	Source string
	// Kind is the transport probed ("smtp", "resend", …), "" when there is
	// nothing to probe.
	Kind   string
	Result mail.ProbeResult
	// Cleartext is a handshake that SUCCEEDED over an unencrypted connection to
	// a relay that is not on this machine. It is its own field because it is not
	// a failure — the relay works — and it is not a success either: every
	// password-reset token this instance sends crosses the network in the clear.
	Cleartext bool
}

// Probe asks the ACTIVE transport whether a send would have a chance, WITHOUT
// sending anything. The result is cached for probeTTL: see that constant for
// why the admin status page must not be an outbound-call generator.
func (s *Service) Probe(ctx context.Context) ProbeReport {
	s.probeMu.Lock()
	if s.probeSeen != nil && time.Since(s.probeAt) < probeTTL {
		cached := *s.probeSeen
		s.probeMu.Unlock()
		return cached
	}
	s.probeMu.Unlock()

	report := s.probeNow(ctx)

	s.probeMu.Lock()
	stored := report
	s.probeSeen = &stored
	s.probeAt = time.Now()
	s.probeMu.Unlock()
	return report
}

func (s *Service) probeNow(ctx context.Context) ProbeReport {
	report := ProbeReport{Source: s.Source()}
	snap := s.cur.Load()
	if snap == nil || snap.transport == nil {
		return report
	}
	report.Kind = snap.transport.Kind()
	report.Result = snap.transport.Probe(ctx)
	report.Cleartext = report.Result.Err == nil && report.Result.Verified &&
		!report.Result.Encrypted && !mail.IsLoopbackHost(snap.smtpHost)
	return report
}

// brokenTransport stands in for a configuration that exists and cannot be used:
// an unopenable credential, or a stored document that no longer validates. It
// keeps the instance's capability TRUE — mail is configured — while failing
// every send with a reason an admin can act on.
type brokenTransport struct {
	kind   string
	reason mail.Reason
}

func (t brokenTransport) Kind() string { return t.kind }

func (t brokenTransport) why() error {
	reason := t.reason
	if reason == "" {
		reason = mail.ReasonSecretUndecryptable
	}
	return &mail.SendError{Reason: reason, Err: errors.New("mailconfig: the stored mail configuration cannot be used")}
}

func (t brokenTransport) Send(context.Context, mail.Message) error { return t.why() }

func (t brokenTransport) Probe(context.Context) mail.ProbeResult {
	return mail.ProbeResult{Err: t.why()}
}

var (
	_ mail.Resolver  = (*Service)(nil)
	_ mail.Transport = brokenTransport{}
)
