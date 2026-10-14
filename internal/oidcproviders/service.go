// Package oidcproviders stores admin-managed OpenID Connect login providers
// (migration 0182) and publishes them to auth.OAuthService, where they override
// the environment providers of the same name.
//
// The client secret follows the mail_config doctrine: sealed by
// internal/secretbox under the MFA KEK chain, never stored without a KEK, never
// returned (the admin view says secret_configured), and a row whose secret
// cannot be opened is published DISABLED rather than silently falling back to
// an environment provider the admin chose to replace.
package oidcproviders

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var (
	ErrNotFound = errors.New("oidcproviders: no such provider")
	ErrExists   = errors.New("oidcproviders: a provider with this name already exists")
	// ErrSecretsKeyMissing: no KEK is configured, so the secret cannot be sealed.
	ErrSecretsKeyMissing = errors.New("oidcproviders: no key-encryption key is configured, so a client secret cannot be stored")
)

// Secret states reported by the admin view.
const (
	SecretStatusOK            = "ok"
	SecretStatusUndecryptable = "undecryptable"
)

// Repository is the data access the service needs; *sqlcgen.Queries satisfies it.
type Repository interface {
	ListOIDCProviders(ctx context.Context) ([]sqlcgen.OidcProvider, error)
	GetOIDCProvider(ctx context.Context, name string) (sqlcgen.OidcProvider, error)
	CreateOIDCProvider(ctx context.Context, arg sqlcgen.CreateOIDCProviderParams) (sqlcgen.OidcProvider, error)
	UpdateOIDCProvider(ctx context.Context, arg sqlcgen.UpdateOIDCProviderParams) (sqlcgen.OidcProvider, error)
	DeleteOIDCProvider(ctx context.Context, name string) (int64, error)
	OAuthIdentitiesExistForProvider(ctx context.Context, provider string) (bool, error)
}

// Sink receives the database provider set (auth.OAuthService).
type Sink interface {
	SetDatabaseProviders([]auth.OAuthProvider)
}

// Provider is the admin view of one row. It has no secret field at all.
type Provider struct {
	Name                string    `json:"name"`
	DisplayName         string    `json:"display_name"`
	IssuerURL           string    `json:"issuer_url"`
	ClientID            string    `json:"client_id"`
	Scopes              []string  `json:"scopes"`
	Enabled             bool      `json:"enabled"`
	AutoProvision       bool      `json:"auto_provision"`
	AllowedEmailDomains []string  `json:"allowed_email_domains"`
	SecretConfigured    bool      `json:"secret_configured"`
	SecretStatus        string    `json:"secret_status"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// Change describes an update for the audit trail: field NAMES only.
type Change struct {
	ChangedFields []string
	SecretChanged bool
}

// Service manages the rows and keeps the sink in step with them.
type Service struct {
	repo         Repository
	cipher       *secretbox.Cipher
	sink         Sink
	bump         func(context.Context) error
	allowPrivate bool
	envIssuers   map[string]string
}

type Option func(*Service)

// WithVersionBump announces a change to the other replicas (settingsversion).
func WithVersionBump(f func(context.Context) error) Option { return func(s *Service) { s.bump = f } }

// WithAllowPrivate accepts http and private-address issuers (labs, internal IdPs).
func WithAllowPrivate(b bool) Option { return func(s *Service) { s.allowPrivate = b } }

// WithEnvironmentIssuers names the environment providers' issuers, so a row may
// override one only with the issuer its linked accounts came from.
func WithEnvironmentIssuers(m map[string]string) Option { return func(s *Service) { s.envIssuers = m } }

// NewService builds the service. cipher may be nil (no KEK): reads work, and
// any write that would store a secret is refused with ErrSecretsKeyMissing.
func NewService(repo Repository, cipher *secretbox.Cipher, sink Sink, opts ...Option) *Service {
	s := &Service{repo: repo, cipher: cipher, sink: sink}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Load reads every row and publishes the set. Boot loader and settingsversion
// reload hook alike.
func (s *Service) Load(ctx context.Context) error {
	rows, err := s.repo.ListOIDCProviders(ctx)
	if err != nil {
		return fmt.Errorf("oidcproviders: load: %w", err)
	}
	slices.SortFunc(rows, func(a, b sqlcgen.OidcProvider) int { return strings.Compare(a.Name, b.Name) })
	out := make([]auth.OAuthProvider, 0, len(rows))
	for _, r := range rows {
		secret, err := s.open(r.ClientSecret)
		out = append(out, auth.OAuthProvider{
			Name: r.Name, DisplayName: r.DisplayName, IssuerURL: r.IssuerUrl, ClientID: r.ClientID,
			ClientSecret: secret, Scopes: r.Scopes, AllowedEmailDomains: r.AllowedEmailDomains,
			Disabled: !r.Enabled || err != nil, DisableSignup: !r.AutoProvision, Guarded: true,
		})
	}
	s.sink.SetDatabaseProviders(out)
	return nil
}

// List returns the admin view of every row.
func (s *Service) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.repo.ListOIDCProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Provider, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.view(r))
	}
	slices.SortFunc(out, func(a, b Provider) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Create adds a provider.
func (s *Service) Create(ctx context.Context, name string, in Input, actor uuid.UUID) (Provider, error) {
	ve := &ValidationError{}
	validateName(ve, name)
	validateIssuer(ve, in.IssuerURL, s.allowPrivate)
	if in.ClientSecret == nil || *in.ClientSecret == "" {
		ve.add("client_secret", "is required")
	}
	norm := validateCommon(ve, in)
	if len(ve.Fields) > 0 {
		return Provider{}, ve
	}
	if _, err := s.repo.GetOIDCProvider(ctx, name); err == nil {
		return Provider{}, ErrExists
	}
	// A row may override an environment provider's settings but never its
	// issuer: provider names stay bound to the issuer their subjects come from.
	if envIssuer, ok := s.envIssuers[name]; ok && envIssuer != in.IssuerURL {
		ve.add("issuer_url", "must equal the issuer of the environment provider with this name")
		return Provider{}, ve
	}
	linked, err := s.repo.OAuthIdentitiesExistForProvider(ctx, name)
	if err != nil {
		return Provider{}, err
	}
	if _, env := s.envIssuers[name]; linked && !env {
		ve.add("name", "accounts are already linked through this name from another issuer; choose a different name")
		return Provider{}, ve
	}
	sealed, err := s.seal(*in.ClientSecret)
	if err != nil {
		return Provider{}, err
	}
	row, err := s.repo.CreateOIDCProvider(ctx, sqlcgen.CreateOIDCProviderParams{
		Name: name, DisplayName: norm.DisplayName, IssuerUrl: in.IssuerURL, ClientID: norm.ClientID,
		ClientSecret: sealed, Scopes: norm.Scopes, Enabled: orTrue(in.Enabled), AutoProvision: orTrue(in.AutoProvision),
		AllowedEmailDomains: norm.AllowedEmailDomains, UpdatedBy: pgconv.UUID(actor),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return Provider{}, ErrExists
		}
		return Provider{}, err
	}
	return s.view(row), s.publish(ctx)
}

// Update replaces a provider's mutable fields.
func (s *Service) Update(ctx context.Context, name string, in Input, actor uuid.UUID) (Provider, Change, error) {
	cur, err := s.repo.GetOIDCProvider(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Provider{}, Change{}, ErrNotFound
	} else if err != nil {
		return Provider{}, Change{}, err
	}
	ve := &ValidationError{}
	if in.IssuerURL != "" && in.IssuerURL != cur.IssuerUrl {
		ve.add("issuer_url", "cannot change: accounts are linked by subject, which is only unique within one issuer; add a new provider instead")
	}
	norm := validateCommon(ve, in)
	if len(ve.Fields) > 0 {
		return Provider{}, Change{}, ve
	}
	p := sqlcgen.UpdateOIDCProviderParams{
		Name: name, DisplayName: norm.DisplayName, ClientID: norm.ClientID, ClientSecret: cur.ClientSecret,
		Scopes: norm.Scopes, Enabled: orCur(in.Enabled, cur.Enabled), AutoProvision: orCur(in.AutoProvision, cur.AutoProvision),
		AllowedEmailDomains: norm.AllowedEmailDomains, UpdatedBy: pgconv.UUID(actor),
	}
	var ch Change
	if in.ClientSecret != nil {
		if *in.ClientSecret == "" {
			ve.add("client_secret", "cannot be cleared; omit it to keep the stored secret")
			return Provider{}, Change{}, ve
		}
		if p.ClientSecret, err = s.seal(*in.ClientSecret); err != nil {
			return Provider{}, Change{}, err
		}
		ch.SecretChanged = true
	}
	for _, f := range []struct {
		name string
		diff bool
	}{
		{"display_name", p.DisplayName != cur.DisplayName},
		{"client_id", p.ClientID != cur.ClientID},
		{"scopes", !slices.Equal(p.Scopes, cur.Scopes)},
		{"enabled", p.Enabled != cur.Enabled},
		{"auto_provision", p.AutoProvision != cur.AutoProvision},
		{"allowed_email_domains", !slices.Equal(p.AllowedEmailDomains, cur.AllowedEmailDomains)},
	} {
		if f.diff {
			ch.ChangedFields = append(ch.ChangedFields, f.name)
		}
	}
	row, err := s.repo.UpdateOIDCProvider(ctx, p)
	if err != nil {
		return Provider{}, Change{}, err
	}
	return s.view(row), ch, s.publish(ctx)
}

// Delete removes a provider; reports whether one existed.
func (s *Service) Delete(ctx context.Context, name string) (bool, error) {
	n, err := s.repo.DeleteOIDCProvider(ctx, name)
	if err != nil {
		return false, err
	}
	return n > 0, s.publish(ctx)
}

// publish tells the other replicas, then reloads this one. The row is written
// either way, so a failed announce still reloads locally before reporting.
func (s *Service) publish(ctx context.Context) error {
	var berr error
	if s.bump != nil {
		berr = s.bump(ctx)
	}
	return errors.Join(berr, s.Load(ctx))
}

func (s *Service) view(r sqlcgen.OidcProvider) Provider {
	status := SecretStatusOK
	if _, err := s.open(r.ClientSecret); err != nil {
		status = SecretStatusUndecryptable
	}
	return Provider{
		Name: r.Name, DisplayName: r.DisplayName, IssuerURL: r.IssuerUrl, ClientID: r.ClientID,
		Scopes: nonNil(r.Scopes), Enabled: r.Enabled, AutoProvision: r.AutoProvision,
		AllowedEmailDomains: nonNil(r.AllowedEmailDomains), SecretConfigured: r.ClientSecret != "",
		SecretStatus: status, UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func (s *Service) seal(plain string) (string, error) {
	if s.cipher == nil {
		return "", ErrSecretsKeyMissing
	}
	return s.cipher.Seal([]byte(plain))
}

func (s *Service) open(sealed string) (string, error) {
	if !secretbox.IsSealed(sealed) || s.cipher == nil {
		return "", errors.New("oidcproviders: secret cannot be opened")
	}
	b, err := s.cipher.Open(sealed)
	return string(b), err
}

func orTrue(b *bool) bool { return b == nil || *b }
func orCur(b *bool, cur bool) bool {
	if b == nil {
		return cur
	}
	return *b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
