package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/oidcproviders"
)

// oidcProviderManager is internal/oidcproviders as this package needs it.
type oidcProviderManager interface {
	List(ctx context.Context) ([]oidcproviders.Provider, error)
	Create(ctx context.Context, name string, in oidcproviders.Input, actor uuid.UUID) (oidcproviders.Provider, error)
	Update(ctx context.Context, name string, in oidcproviders.Input, actor uuid.UUID) (oidcproviders.Provider, oidcproviders.Change, error)
	Delete(ctx context.Context, name string) (bool, error)
	SecretsAvailable() bool
}

// WithOIDCProviderService mounts the admin OIDC provider endpoints (0182).
func WithOIDCProviderService(svc oidcProviderManager) Option {
	return func(s *Server) { s.oidcprovsvc = svc }
}

// oidcProviderRequest is a create/update body: the provider fields plus the
// caller's fresh credential, which is kept out of oidcproviders.Input so it can
// never be stored. Name is read on create only (it is the URL on update).
type oidcProviderRequest struct {
	Name string `json:"name"`
	oidcproviders.Input
	CurrentPassword string `json:"current_password"`
	StepUpToken     string `json:"step_up_token"`
}

type oidcEnvironmentProvider struct {
	Name       string `json:"name"`
	IssuerURL  string `json:"issuer_url"`
	Overridden bool   `json:"overridden"`
}

// --- GET /api/v1/admin/oidc-providers ---
//
// The admin-managed rows (never a secret: secret_configured/secret_status
// only) and the environment providers they may override (name + issuer only).
func (s *Server) handleListOIDCProviders(c echo.Context) error {
	rows, err := s.oidcprovsvc.List(c.Request().Context())
	if err != nil {
		return err
	}
	inDB := make(map[string]bool, len(rows))
	for _, r := range rows {
		inDB[r.Name] = true
	}
	env := make([]oidcEnvironmentProvider, 0, len(s.cfg.OAuthProviders))
	for _, p := range s.cfg.OAuthProviders {
		env = append(env, oidcEnvironmentProvider{Name: p.Name, IssuerURL: p.IssuerURL, Overridden: inDB[p.Name]})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"providers":         rows,
		"environment":       env,
		"secrets_available": s.oidcprovsvc.SecretsAvailable(),
	})
}

// --- POST /api/v1/admin/oidc-providers ---
func (s *Server) handleCreateOIDCProvider(c echo.Context) error {
	return s.writeOIDCProvider(c, observability.ActionAdminOIDCProviderCreate, func(ctx context.Context, in oidcProviderRequest, actor uuid.UUID) (oidcproviders.Provider, oidcproviders.Change, int, error) {
		p, err := s.oidcprovsvc.Create(ctx, in.Name, in.Input, actor)
		return p, oidcproviders.Change{SecretChanged: err == nil}, http.StatusCreated, err
	})
}

// --- PUT /api/v1/admin/oidc-providers/{name} ---
func (s *Server) handleUpdateOIDCProvider(c echo.Context) error {
	return s.writeOIDCProvider(c, observability.ActionAdminOIDCProviderUpdate, func(ctx context.Context, in oidcProviderRequest, actor uuid.UUID) (oidcproviders.Provider, oidcproviders.Change, int, error) {
		p, ch, err := s.oidcprovsvc.Update(ctx, c.Param("name"), in.Input, actor)
		return p, ch, http.StatusOK, err
	})
}

type oidcWriteFunc func(context.Context, oidcProviderRequest, uuid.UUID) (oidcproviders.Provider, oidcproviders.Change, int, error)

// writeOIDCProvider is the shared create/update path: decode strictly, demand
// a fresh credential (a login provider decides who can sign in), write, and
// audit by provider name and field NAMES only.
func (s *Server) writeOIDCProvider(c echo.Context, action string, write oidcWriteFunc) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in oidcProviderRequest
	if err := decodeAdminDocumentBody(c, &in); err != nil {
		return err
	}
	if err := s.confirmFreshAdminWriter(c, callerID, action, in.CurrentPassword, in.StepUpToken); err != nil {
		return err
	}
	p, ch, status, err := write(c.Request().Context(), in, callerID)
	if err != nil {
		return s.oidcProviderError(c, action, callerID, err)
	}
	s.auditEvent(c, audit.Event{
		Action: action, Result: observability.ResultSuccess, ActorID: callerID.String(), Reason: "saved",
		Metadata: []audit.MetadataField{
			{Key: "provider", Value: p.Name},
			{Key: "changed_keys", Value: strings.Join(ch.ChangedFields, ",")},
			{Key: "secret_changed", Value: strconv.FormatBool(ch.SecretChanged)},
		},
	})
	return c.JSON(status, p)
}

// oidcProviderError maps a refused write to its response and audits the
// refusal by reason code.
func (s *Server) oidcProviderError(c echo.Context, action string, callerID uuid.UUID, err error) error {
	refuse := func(reason string, out error) error {
		s.audit(c, action, observability.ResultFailure, callerID.String(), reason)
		return out
	}
	var ve *oidcproviders.ValidationError
	switch {
	case errors.As(err, &ve):
		fields := make([]FieldError, 0, len(ve.Fields))
		names := make([]string, 0, len(ve.Fields))
		for _, f := range ve.Fields {
			fields = append(fields, FieldError{Field: f.Field, Message: f.Msg})
			names = append(names, f.Field)
		}
		return refuse("invalid:"+strings.Join(names, ","), &ValidationError{Fields: fields})
	case errors.Is(err, oidcproviders.ErrSecretsKeyMissing):
		return refuse("secrets_key_missing", &OIDCSecretsKeyMissingError{})
	case errors.Is(err, oidcproviders.ErrExists):
		return refuse("exists", echo.NewHTTPError(http.StatusConflict, "an OIDC provider with this name already exists"))
	case errors.Is(err, oidcproviders.ErrNotFound):
		return refuse("not_found", echo.NewHTTPError(http.StatusNotFound, "no such OIDC provider"))
	}
	return refuse("save_failed", err)
}

// oidcDeleteRequest carries the fresh credential a delete needs.
type oidcDeleteRequest struct {
	CurrentPassword string `json:"current_password"`
	StepUpToken     string `json:"step_up_token"`
}

// --- DELETE /api/v1/admin/oidc-providers/{name} ---
//
// Removes a database-only provider. Takes a fresh credential like the other
// destructive admin writes.
//
// A row that overrides an ENVIRONMENT provider of the same name is refused
// (disable or edit it instead): deleting it would silently put the
// environment provider back without the row's restrictions (enabled,
// auto_provision, allowed_email_domains). The same thing happens on an
// app-only rollback to a core without migration 0182, which ignores the
// table — a rollback concern tracked outside this handler.
func (s *Server) handleDeleteOIDCProvider(c echo.Context) error {
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	action := observability.ActionAdminOIDCProviderDelete
	var in oidcDeleteRequest
	// No body is "no proof" (403 step_up_required below), not a malformed one.
	if c.Request().ContentLength != 0 {
		if err := decodeAdminDocumentBody(c, &in); err != nil {
			return err
		}
	}
	if err := s.confirmFreshAdminWriter(c, callerID, action, in.CurrentPassword, in.StepUpToken); err != nil {
		return err
	}
	name := c.Param("name")
	for _, p := range s.cfg.OAuthProviders {
		if p.Name == name {
			return s.oidcProviderError(c, action, callerID, &oidcproviders.ValidationError{Fields: []oidcproviders.FieldError{{
				Field: "name", Msg: "overrides an environment provider; disable or edit it instead of deleting it",
			}}})
		}
	}
	existed, err := s.oidcprovsvc.Delete(c.Request().Context(), name)
	if err != nil {
		return s.oidcProviderError(c, action, callerID, err)
	}
	if !existed {
		return echo.NewHTTPError(http.StatusNotFound, "no such OIDC provider")
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionAdminOIDCProviderDelete, Result: observability.ResultSuccess,
		ActorID: callerID.String(), Reason: "deleted",
		Metadata: []audit.MetadataField{{Key: "provider", Value: name}},
	})
	return c.NoContent(http.StatusNoContent)
}
