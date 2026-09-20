package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/mailconfig"
	"github.com/vidra/vidra-core/internal/observability"
)

// mailConfigProvider is the outbound-mail configuration service
// (internal/mailconfig) as this package needs it. It is an interface for the
// usual reason: the contract test mounts the routes with a service over no
// database at all.
type mailConfigProvider interface {
	// Status is the whole GET body, read from memory.
	Status() mailconfig.State
	// Available is the LIVE "can this instance send email" read — the one
	// predicate that replaced three boot-frozen copies of the question.
	Available() bool
	// Source names where the active mail path comes from.
	Source() string
	Save(ctx context.Context, in mailconfig.Input, actor uuid.UUID) (mailconfig.SaveResult, error)
	Reset(ctx context.Context, actor uuid.UUID) (bool, error)
	// Probe asks the ACTIVE transport whether a send would have a chance,
	// without sending. Cached inside the service; see mailconfig.probeTTL.
	Probe(ctx context.Context) mailconfig.ProbeReport
}

// WithMailConfigService wires the admin-configurable outbound-mail service. It
// is what turns features.mail, the registration email-verification gate and the
// admin status/infrastructure pages into LIVE reads instead of boot facts.
func WithMailConfigService(svc mailConfigProvider) Option {
	return func(s *Server) { s.mailconfigsvc = svc }
}

// maxMailConfigBody bounds the PUT. The document is a handful of short strings;
// anything larger is a mistake or an attempt to make the server allocate.
const maxMailConfigBody = 16 << 10

// --- GET /api/v1/admin/mail-config ---
//
// The configuration an admin edits, and the state it is in. It NEVER returns a
// credential: each transport block reports only whether one is stored, as a
// *_set boolean, and `secret_status` says whether the stored one can still be
// opened. The environment block reports host/port/from so an admin can see what
// a DELETE would revert to — and never the SMTP username or password, which are
// on this repository's absolute never-list (see admin_infra.go).
func (s *Server) handleGetMailConfig(c echo.Context) error {
	if s.mailconfigsvc == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "outbound mail configuration is not available on this deployment")
	}
	return c.JSON(http.StatusOK, s.mailconfigsvc.Status())
}

// --- PUT /api/v1/admin/mail-config ---
//
// Saves the WHOLE document. Validation is internal/mail's transport builder,
// not a second copy of its rules, so a configuration that saves is one that
// resolves and the 422 field paths name exactly what the transport refused.
//
//	409 — a credential was supplied on a deployment with no KEK to seal it with
//	      (mail_secrets_key_missing). Secrets are stored sealed or not at all.
//	422 — the document is invalid; `fields` carries dotted paths (smtp.host,
//	      mailgun.api_key).
func (s *Server) handleUpdateMailConfig(c echo.Context) error {
	if s.mailconfigsvc == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "outbound mail configuration is not available on this deployment")
	}
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in mailconfig.Input
	if err := decodeMailConfigBody(c, &in); err != nil {
		return err
	}

	res, err := s.mailconfigsvc.Save(c.Request().Context(), in, callerID)
	if err != nil {
		return s.mailConfigSaveError(c, callerID, err)
	}

	// The audit record carries the transport, the dotted NAMES of the fields
	// that changed and whether the credential was replaced. Never a value: a
	// relay password and a provider API key are exactly what an audit trail
	// must be able to describe without holding.
	s.auditEvent(c, audit.Event{
		Action:  observability.ActionAdminMailConfigUpdate,
		Result:  observability.ResultSuccess,
		ActorID: callerID.String(),
		Reason:  "saved",
		Metadata: []audit.MetadataField{
			{Key: "provider", Value: res.Transport},
			{Key: "changed_keys", Value: strings.Join(res.ChangedFields, ",")},
			{Key: "secret_changed", Value: strconv.FormatBool(res.SecretChanged)},
		},
	})
	return c.JSON(http.StatusOK, s.mailconfigsvc.Status())
}

// mailConfigSaveError renders a failed save and audits the refusal. The failure
// is audited with a REASON CODE, never the document: an invalid save is still an
// authenticated admin action worth being able to look back at.
func (s *Server) mailConfigSaveError(c echo.Context, callerID uuid.UUID, err error) error {
	switch {
	case errors.Is(err, mailconfig.ErrSecretsKeyMissing):
		s.audit(c, observability.ActionAdminMailConfigUpdate, observability.ResultFailure, callerID.String(), "secrets_key_missing")
		return &MailSecretsKeyMissingError{}
	default:
		var ve *mail.ValidationError
		if errors.As(err, &ve) {
			s.audit(c, observability.ActionAdminMailConfigUpdate, observability.ResultFailure, callerID.String(),
				"invalid:"+strings.Join(mailFieldNames(ve), ","))
			return &ValidationError{Fields: mailFieldErrors(ve)}
		}
		var na *mailconfig.NotAnnouncedError
		if errors.As(err, &na) {
			// The row IS written. `save_failed` here would be a ledger that
			// contradicts the table and would send whoever reads it looking for
			// a write that already happened. The response stays an error so the
			// admin retries — the retry re-runs the same upsert and the bump,
			// and is idempotent.
			s.audit(c, observability.ActionAdminMailConfigUpdate, observability.ResultFailure, callerID.String(), "saved_not_announced")
			return err
		}
	}
	s.audit(c, observability.ActionAdminMailConfigUpdate, observability.ResultFailure, callerID.String(), "save_failed")
	return err
}

// --- DELETE /api/v1/admin/mail-config ---
//
// Removes the document, so the instance reverts to its ENVIRONMENT mail
// configuration (MAIL_ENABLED + SMTP_*) — or to no mail path at all, which the
// GET's `environment.configured` says in advance.
func (s *Server) handleDeleteMailConfig(c echo.Context) error {
	if s.mailconfigsvc == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "outbound mail configuration is not available on this deployment")
	}
	callerID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	existed, err := s.mailconfigsvc.Reset(c.Request().Context(), callerID)
	if err != nil {
		// Same distinction the save path draws: a DELETE that committed and did
		// not propagate is not a reset that failed, and the trail must not say
		// it was.
		reason := "reset_failed"
		var na *mailconfig.NotAnnouncedError
		if errors.As(err, &na) {
			reason = "reset_not_announced"
		}
		s.audit(c, observability.ActionAdminMailConfigReset, observability.ResultFailure, callerID.String(), reason)
		return err
	}
	reason := "reverted_to_environment"
	if !existed {
		// Nothing was stored. Recorded distinctly so the trail does not imply a
		// configuration was removed when none existed.
		reason = "no_document"
	}
	s.auditEvent(c, audit.Event{
		Action:   observability.ActionAdminMailConfigReset,
		Result:   observability.ResultSuccess,
		ActorID:  callerID.String(),
		Reason:   reason,
		Metadata: []audit.MetadataField{{Key: "provider", Value: s.mailconfigsvc.Source()}},
	})
	return c.NoContent(http.StatusNoContent)
}

// decodeMailConfigBody reads the PUT body under a hard size cap and refuses
// unknown fields. DisallowUnknownFields matters more here than on most bodies:
// a panel that mis-spells `password` would otherwise silently save a
// configuration with no credential and report success.
func decodeMailConfigBody(c echo.Context, out any) error {
	d := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, maxMailConfigBody))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid mail configuration document")
	}
	if d.Decode(new(any)) != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid mail configuration document")
	}
	return nil
}

// mailFieldErrors converts the transport builder's field problems into the
// envelope's shape. The dotted paths are carried through unchanged — they are
// the contract the admin form binds its inputs to.
func mailFieldErrors(ve *mail.ValidationError) []FieldError {
	out := make([]FieldError, 0, len(ve.Fields))
	for _, f := range ve.Fields {
		out = append(out, FieldError{Field: f.Field, Message: f.Msg})
	}
	return out
}

func mailFieldNames(ve *mail.ValidationError) []string {
	out := make([]string, 0, len(ve.Fields))
	for _, f := range ve.Fields {
		out = append(out, f.Field)
	}
	return out
}
