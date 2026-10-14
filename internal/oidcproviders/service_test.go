package oidcproviders

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeRepo struct {
	rows       map[string]sqlcgen.OidcProvider
	identities map[string]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]sqlcgen.OidcProvider{}, identities: map[string]bool{}}
}

func (f *fakeRepo) ListOIDCProviders(context.Context) ([]sqlcgen.OidcProvider, error) {
	var out []sqlcgen.OidcProvider
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) GetOIDCProvider(_ context.Context, name string) (sqlcgen.OidcProvider, error) {
	r, ok := f.rows[name]
	if !ok {
		return r, pgx.ErrNoRows
	}
	return r, nil
}

func (f *fakeRepo) CreateOIDCProvider(_ context.Context, a sqlcgen.CreateOIDCProviderParams) (sqlcgen.OidcProvider, error) {
	r := sqlcgen.OidcProvider{Name: a.Name, DisplayName: a.DisplayName, IssuerUrl: a.IssuerUrl, ClientID: a.ClientID,
		ClientSecret: a.ClientSecret, Scopes: a.Scopes, Enabled: a.Enabled, AutoProvision: a.AutoProvision,
		AllowedEmailDomains: a.AllowedEmailDomains, UpdatedBy: a.UpdatedBy, UpdatedAt: time.Now()}
	f.rows[a.Name] = r
	return r, nil
}

func (f *fakeRepo) UpdateOIDCProvider(_ context.Context, a sqlcgen.UpdateOIDCProviderParams) (sqlcgen.OidcProvider, error) {
	r, ok := f.rows[a.Name]
	if !ok {
		return r, pgx.ErrNoRows
	}
	r.DisplayName, r.ClientID, r.ClientSecret, r.Scopes = a.DisplayName, a.ClientID, a.ClientSecret, a.Scopes
	r.Enabled, r.AutoProvision, r.AllowedEmailDomains, r.UpdatedBy = a.Enabled, a.AutoProvision, a.AllowedEmailDomains, a.UpdatedBy
	f.rows[a.Name] = r
	return r, nil
}

func (f *fakeRepo) DeleteOIDCProvider(_ context.Context, name string) (int64, error) {
	if _, ok := f.rows[name]; !ok {
		return 0, nil
	}
	delete(f.rows, name)
	return 1, nil
}

func (f *fakeRepo) OAuthIdentitiesExistForProvider(_ context.Context, name string) (bool, error) {
	return f.identities[name], nil
}

type fakeSink struct{ got []auth.OAuthProvider }

func (s *fakeSink) SetDatabaseProviders(p []auth.OAuthProvider) { s.got = p }

func testCipher(t *testing.T) *secretbox.Cipher {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c, err := secretbox.NewCipherFromBase64(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func validInput() Input {
	return Input{IssuerURL: "https://sso.corp.example", ClientID: "vidra", ClientSecret: strp("s3cret"),
		DisplayName: "Corp SSO", AllowedEmailDomains: []string{"Corp.Example"}, AutoProvision: boolp(false)}
}

func TestCreateSealsSecretAndPublishes(t *testing.T) {
	repo, sink := newFakeRepo(), &fakeSink{}
	bumps := 0
	s := NewService(repo, testCipher(t), sink, WithVersionBump(func(context.Context) error { bumps++; return nil }))
	ctx := context.Background()

	p, err := s.Create(ctx, "corp", validInput(), uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !p.SecretConfigured || !p.Enabled || p.AutoProvision || p.AllowedEmailDomains[0] != "corp.example" {
		t.Errorf("created view = %+v", p)
	}
	stored := repo.rows["corp"].ClientSecret
	if !secretbox.IsSealed(stored) || strings.Contains(stored, "s3cret") {
		t.Errorf("stored secret %q is not sealed", stored)
	}
	if bumps != 1 {
		t.Errorf("version bumps = %d, want 1", bumps)
	}
	if len(sink.got) != 1 {
		t.Fatalf("sink got %d providers", len(sink.got))
	}
	g := sink.got[0]
	if g.ClientSecret != "s3cret" || !g.Guarded || !g.DisableSignup || g.Disabled || g.DisplayName != "Corp SSO" {
		t.Errorf("published provider = %+v", g)
	}

	if _, err := s.Create(ctx, "corp", validInput(), uuid.New()); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate create: err = %v, want ErrExists", err)
	}
}

func TestCreateWithoutKEKRefused(t *testing.T) {
	s := NewService(newFakeRepo(), nil, &fakeSink{})
	if _, err := s.Create(context.Background(), "corp", validInput(), uuid.New()); !errors.Is(err, ErrSecretsKeyMissing) {
		t.Errorf("err = %v, want ErrSecretsKeyMissing", err)
	}
}

func TestCreateValidation(t *testing.T) {
	s := NewService(newFakeRepo(), testCipher(t), &fakeSink{})
	for _, tc := range []struct {
		name, field string
		mut         func(*Input)
	}{
		{"Bad Name", "name", func(*Input) {}},
		{"atproto", "name", func(*Input) {}},
		{"ok", "issuer_url", func(in *Input) { in.IssuerURL = "http://sso.corp.example" }},
		{"ok", "issuer_url", func(in *Input) { in.IssuerURL = "https://127.0.0.1" }},
		{"ok", "issuer_url", func(in *Input) { in.IssuerURL = "https://sso.example/?q=1" }},
		{"ok", "client_id", func(in *Input) { in.ClientID = " " }},
		{"ok", "client_secret", func(in *Input) { in.ClientSecret = nil }},
		{"ok", "allowed_email_domains", func(in *Input) { in.AllowedEmailDomains = []string{"not a domain"} }},
		{"ok", "scopes", func(in *Input) { in.Scopes = []string{"has space"} }},
	} {
		in := validInput()
		tc.mut(&in)
		_, err := s.Create(context.Background(), tc.name, in, uuid.New())
		var ve *ValidationError
		if !errors.As(err, &ve) || !ve.Has(tc.field) {
			t.Errorf("%s/%s: err = %v, want a validation error on %s", tc.name, tc.field, err, tc.field)
		}
	}

	lab := NewService(newFakeRepo(), testCipher(t), &fakeSink{}, WithAllowPrivate(true))
	in := validInput()
	in.IssuerURL = "http://dex:5556/dex"
	if _, err := lab.Create(context.Background(), "dex", in, uuid.New()); err != nil {
		t.Errorf("allow-private lab issuer: %v", err)
	}
}

// TestNameRebindingGuard: accounts are linked by (provider name, subject), and a
// subject is only unique within one issuer. A name that already has linked
// identities may only be bound to the issuer they came from.
func TestNameRebindingGuard(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	repo.identities["corp"] = true
	s := NewService(repo, testCipher(t), &fakeSink{})
	var ve *ValidationError
	if _, err := s.Create(ctx, "corp", validInput(), uuid.New()); !errors.As(err, &ve) || !ve.Has("name") {
		t.Errorf("rebinding a linked name with no matching environment issuer: err = %v", err)
	}

	same := NewService(repo, testCipher(t), &fakeSink{}, WithEnvironmentIssuers(map[string]string{"corp": "https://sso.corp.example"}))
	if _, err := same.Create(ctx, "corp", validInput(), uuid.New()); err != nil {
		t.Errorf("overriding the environment provider with the same issuer: %v", err)
	}

	repo2 := newFakeRepo()
	repo2.identities["corp"] = true
	other := NewService(repo2, testCipher(t), &fakeSink{}, WithEnvironmentIssuers(map[string]string{"corp": "https://other.example"}))
	if _, err := other.Create(ctx, "corp", validInput(), uuid.New()); !errors.As(err, &ve) || !ve.Has("name") {
		t.Errorf("overriding the environment provider with a different issuer: err = %v", err)
	}
}

func TestUpdateKeepsSecretAndRefusesIssuerChange(t *testing.T) {
	ctx := context.Background()
	repo, sink := newFakeRepo(), &fakeSink{}
	s := NewService(repo, testCipher(t), sink)
	if _, err := s.Create(ctx, "corp", validInput(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	sealed := repo.rows["corp"].ClientSecret

	in := validInput()
	in.ClientSecret = nil
	in.ClientID = "vidra-2"
	in.Enabled = boolp(false)
	p, ch, err := s.Update(ctx, "corp", in, uuid.New())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if repo.rows["corp"].ClientSecret != sealed || ch.SecretChanged || p.Enabled {
		t.Errorf("absent secret must keep the stored one; view=%+v change=%+v", p, ch)
	}
	if strings.Join(ch.ChangedFields, ",") != "client_id,enabled" {
		t.Errorf("changed fields = %v", ch.ChangedFields)
	}
	if len(sink.got) != 1 || !sink.got[0].Disabled {
		t.Errorf("a disabled row must still be published (it shadows the environment): %+v", sink.got)
	}

	in.ClientSecret = strp("rotated")
	if _, ch, err := s.Update(ctx, "corp", in, uuid.New()); err != nil || !ch.SecretChanged {
		t.Errorf("rotate: change=%+v err=%v", ch, err)
	}

	in.IssuerURL = "https://elsewhere.example"
	var ve *ValidationError
	if _, _, err := s.Update(ctx, "corp", in, uuid.New()); !errors.As(err, &ve) || !ve.Has("issuer_url") {
		t.Errorf("issuer change: err = %v, want a validation error", err)
	}
	if _, _, err := s.Update(ctx, "nope", validInput(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: err = %v, want ErrNotFound", err)
	}
}

func TestUndecryptableSecretFailsClosed(t *testing.T) {
	ctx := context.Background()
	repo, sink := newFakeRepo(), &fakeSink{}
	if _, err := NewService(repo, testCipher(t), &fakeSink{}).Create(ctx, "corp", validInput(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	// Same rows, a different KEK: the secret cannot be opened.
	s := NewService(repo, testCipher(t), sink)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 1 || !sink.got[0].Disabled || sink.got[0].ClientSecret != "" {
		t.Errorf("an undecryptable row must publish as disabled, never fall back: %+v", sink.got)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].SecretStatus != SecretStatusUndecryptable {
		t.Errorf("List = %+v, %v", list, err)
	}

	existed, err := s.Delete(ctx, "corp")
	if err != nil || !existed || len(sink.got) != 0 {
		t.Errorf("Delete: existed=%v err=%v sink=%v", existed, err, sink.got)
	}
}
