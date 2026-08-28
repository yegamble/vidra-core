package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// audit preserves the legacy call shape while writing the typed v2 envelope.
func (s *Server) audit(c echo.Context, action, result, actorID, reason string) {
	s.auditEvent(c, audit.Event{
		Action: action, Result: result, ActorID: actorID, Reason: reason,
	})
}

// auditEvent emits and best-effort persists a typed security-audit event for the
// current request. Request/correlation/trace fields are attached centrally so
// individual handlers cannot accidentally omit them. Resource ids and metadata
// still come from explicit, allowlisted event construction at the call site.
func (s *Server) auditEvent(c echo.Context, ev audit.Event) {
	ctx := c.Request().Context()
	if ev.RequestID == "" {
		ev.RequestID = c.Response().Header().Get(echo.HeaderXRequestID)
	}
	if ev.CorrelationID == "" {
		ev.CorrelationID = correlationIDFromContext(ctx)
	}
	if ev.TraceID == "" {
		if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
			ev.TraceID = sc.TraceID().String()
		}
	}
	if ev.Actor.ID == "" {
		ev.Actor.ID = ev.ActorID
	}
	if ev.Actor.Kind == "" {
		if ev.Actor.ID == "" {
			ev.Actor.Kind = "anonymous"
		} else {
			ev.Actor.Kind = "user"
		}
	}
	if userID, role, ok := principalFromContext(c); ok && ev.Actor.ID == userID.String() {
		ev.Actor.Role = role
	}
	var pipelineRunID, jobID string
	if ev.PipelineRunID != uuid.Nil {
		pipelineRunID = ev.PipelineRunID.String()
	}
	if ev.JobID != uuid.Nil {
		jobID = ev.JobID.String()
	}
	observability.Audit(ctx, s.logger, observability.AuditEvent{
		SchemaVersion: ev.SchemaVersion,
		Domain:        ev.Domain, Action: ev.Action, Result: ev.Result,
		ActorID: ev.Actor.ID, ActorKind: ev.Actor.Kind, ActorRole: ev.Actor.Role,
		RequestID: ev.RequestID, CorrelationID: ev.CorrelationID, TraceID: ev.TraceID,
		PipelineRunID: pipelineRunID, JobID: jobID,
		ResourceType: ev.ResourceType, ResourceID: ev.ResourceID, Reason: ev.Reason,
	})
	// Persist the durable audit trail best-effort when wired; a failure never
	// blocks the request (the slog line above is still emitted).
	if s.auditLog != nil {
		if err := s.auditLog.Record(ctx, ev); err != nil {
			s.logger.WarnContext(ctx, "audit log persist failed", "error", err, "action", ev.Action)
		}
	}
}

// registerRequest is the POST /api/v1/auth/register body.
type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Note is an optional message to moderators, used only when the instance
	// requires registration approval; ignored otherwise.
	Note string `json:"note,omitempty"`
	// AgeAttestation is the PeerTube-style minimum-age checkbox (config-parity
	// W7): "I am at least N years old". Required (true) while
	// registration_minimum_age is active; no birthdate is collected.
	AgeAttestation bool `json:"age_attestation,omitempty"`
	// CookieMode opts the new session into cookie mode: the refresh token is
	// set as an httpOnly vidra_refresh cookie and omitted from the body.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

// maxRegistrationNoteLen bounds the optional applicant note.
const maxRegistrationNoteLen = 2000

// bcrypt silently truncates input beyond 72 bytes, so we cap password length to
// avoid a surprising security cliff.
const maxPasswordLen = 72

// usernameReservedCharsMessage is the shared 422 text for the '@'/whitespace
// ban applied to NEW usernames (register + owner claim).
const usernameReservedCharsMessage = "must not contain '@' or spaces"

// usernameHasReservedChars reports whether a NEW username collides with the
// sign-in identifier namespace. Sign-in accepts an email OR a username and
// resolves the email column first, so a username containing '@' can only ever
// be an unreachable sign-in identifier — or a deliberate lookalike of somebody
// else's address. Whitespace is banned alongside it because a username with
// leading/inner spaces is indistinguishable from a typo at the prompt.
//
// Existing usernames are deliberately NOT migrated: they keep working, they
// simply can never shadow an email (precedence already guarantees that). Only
// newly chosen names are constrained.
func usernameHasReservedChars(name string) bool {
	return strings.ContainsRune(name, '@') || strings.ContainsFunc(name, unicode.IsSpace)
}

func (r registerRequest) Validate() []FieldError {
	var fes []FieldError
	name := strings.TrimSpace(r.Username)
	switch {
	case name == "":
		fes = append(fes, FieldError{Field: "username", Message: "is required"})
	case len(name) < 3 || len(name) > 30:
		fes = append(fes, FieldError{Field: "username", Message: "must be 3–30 characters"})
	case usernameHasReservedChars(name):
		fes = append(fes, FieldError{Field: "username", Message: usernameReservedCharsMessage})
	}
	if !looksLikeEmail(r.Email) {
		fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
	}
	switch {
	case len(r.Password) < 8:
		fes = append(fes, FieldError{Field: "password", Message: "must be at least 8 characters"})
	case len(r.Password) > maxPasswordLen:
		fes = append(fes, FieldError{Field: "password", Message: "must be at most 72 characters"})
	}
	if len(r.Note) > maxRegistrationNoteLen {
		fes = append(fes, FieldError{Field: "note", Message: "must be at most 2000 characters"})
	}
	return fes
}

// loginRequest is the POST /api/v1/auth/login body. Exactly one identifier
// field is sent: `identifier` (email OR username — the current shape) or the
// legacy `email` (kept so older clients keep signing in unchanged).
type loginRequest struct {
	Email string `json:"email"`
	// Identifier is an email address or a username. It carries no shape rule
	// beyond length on purpose: usernames predating the '@'/whitespace ban can
	// contain anything, and refusing them here would lock those accounts out.
	// Ambiguity is resolved in the query, not the validator — the email column
	// is matched first, so an identifier that is someone's address always
	// reaches that someone.
	Identifier string `json:"identifier,omitempty"`
	Password   string `json:"password"`
	// CookieMode opts the new session into cookie mode: the refresh token is
	// set as an httpOnly vidra_refresh cookie and omitted from the body.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

// Sign-in identifiers are bounded well above the practical maximum (RFC 5321
// caps a mail path at 254 octets; usernames at 30) purely to stop an unbounded
// string reaching the database.
const maxLoginIdentifierLen = 254

func (r loginRequest) Validate() []FieldError {
	var fes []FieldError
	email := strings.TrimSpace(r.Email)
	identifier := strings.TrimSpace(r.Identifier)
	switch {
	case email != "" && identifier != "":
		// Two identifiers in one body is ambiguous, and silently preferring one
		// would make the caller's intent unknowable — including to an audit
		// reader. Refuse rather than guess.
		fes = append(fes, FieldError{Field: "identifier", Message: "must not be combined with email — send exactly one"})
	case identifier != "":
		if n := len(identifier); n < 3 || n > maxLoginIdentifierLen {
			fes = append(fes, FieldError{Field: "identifier", Message: "must be 3–254 characters"})
		}
	default:
		// Legacy email-only shape. This is also the empty-body case, which keeps
		// reporting the `email` field exactly as it did before `identifier`
		// existed.
		if !looksLikeEmail(r.Email) {
			fes = append(fes, FieldError{Field: "email", Message: "must be a valid email"})
		}
	}
	if r.Password == "" {
		fes = append(fes, FieldError{Field: "password", Message: "is required"})
	}
	return fes
}

// looksLikeEmail is a deliberately lax structural check: exactly one "@" with
// non-empty local and domain parts and a dot in the domain. Real deliverability
// is proven by the verification flow, not by a regex.
func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}

// userView is the public projection of a user — never includes the password hash.
type userView struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"email_verified"`
	DisplayName   string `json:"display_name"`
	Bio           string `json:"bio"`
	// Unlisted is the account-level discovery opt-out (§16): when true the
	// account's channels/videos are hidden from public feed/search surfaces
	// while direct URLs keep working.
	Unlisted bool `json:"unlisted"`
	// HistoryEnabled is the per-user watch-history preference (config-parity
	// W7): while false, watch-progress/history writes are skipped.
	HistoryEnabled bool `json:"history_enabled"`
	// Search & recommendation preferences (search-service W4): the user half of
	// the two-factor personalization gate (instance setting AND user pref AND
	// signed-in). All default true.
	SearchHistoryEnabled               bool `json:"search_history_enabled"`
	PersonalizedSearchEnabled          bool `json:"personalized_search_enabled"`
	PersonalizedRecommendationsEnabled bool `json:"personalized_recommendations_enabled"`
	// ProfilePublic is an explicit opt-in. A false profile returns 404 publicly.
	ProfilePublic bool `json:"profile_public"`
	// ShowBluesky is the per-user opt-in to display the linked Bluesky/ATProto
	// handle on the public profile (0102). Default false.
	ShowBluesky bool `json:"show_bluesky"`
	// SensitiveContentPolicy is the per-user sensitive-content policy override
	// (0100). Omitted when the user inherits the instance policy (column NULL);
	// otherwise one of hide|warn|blur|display.
	SensitiveContentPolicy *string   `json:"sensitive_content_policy,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	// HasAvatar/HasBanner are set on GET/PATCH /auth/me (omitted elsewhere);
	// when true the image is served at GET /users/{id}/avatar | /banner.
	HasAvatar *bool `json:"has_avatar,omitempty"`
	HasBanner *bool `json:"has_banner,omitempty"`
}

func newUserView(u sqlcgen.User) userView {
	return userView{
		ID:                                 u.ID.String(),
		Username:                           u.Username,
		Email:                              u.Email,
		Role:                               u.Role,
		EmailVerified:                      u.EmailVerified,
		DisplayName:                        u.DisplayName,
		Bio:                                u.Bio,
		Unlisted:                           u.Unlisted,
		HistoryEnabled:                     u.HistoryEnabled,
		SearchHistoryEnabled:               u.SearchHistoryEnabled,
		PersonalizedSearchEnabled:          u.PersonalizedSearchEnabled,
		PersonalizedRecommendationsEnabled: u.PersonalizedRecommendationsEnabled,
		ProfilePublic:                      u.ProfilePublic,
		ShowBluesky:                        u.ShowBluesky,
		SensitiveContentPolicy:             u.SensitiveContentPolicy,
		CreatedAt:                          u.CreatedAt,
	}
}

// authResponse is returned by register and login. RefreshToken is omitted in
// cookie mode, where the httpOnly vidra_refresh cookie is the sole carrier.
type authResponse struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	User         userView `json:"user"`
}

func (s *Server) authResponse(status int, c echo.Context, user sqlcgen.User, tokens auth.Tokens, cookieMode bool) error {
	resp := authResponse{
		Token:        tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.authTTL.Seconds()),
		User:         newUserView(user),
	}
	if cookieMode {
		// The cookie is the sole refresh-token carrier in cookie mode: set the
		// rotated token httpOnly and keep the raw value out of the JSON body so
		// it never has to touch JavaScript-accessible storage.
		s.setRefreshCookie(c, tokens.RefreshToken)
		resp.RefreshToken = ""
	}
	return c.JSON(status, resp)
}

// handleRegister creates an account and returns it with an access + refresh
// token. Config-parity W7 gates run in order: registration open → user limit
// → age attestation → approval queue → email-verification hold → plain signup.
func (s *Server) handleRegister(c echo.Context) error {
	if !s.registrationEnabled() {
		return echo.NewHTTPError(http.StatusForbidden, "registration is disabled on this instance")
	}
	// registration_user_limit: count-and-refuse (approximate under concurrent
	// signups by design). /instance reports effective registration_enabled
	// false + registration_disabled_reason while at the limit.
	if s.registrationAtUserLimit(c.Request().Context()) {
		return echo.NewHTTPError(http.StatusForbidden, "registration is closed: this instance has reached its user limit")
	}
	var in registerRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// registration_minimum_age: the signup must carry the attestation flag
	// while the setting is active (PeerTube parity — an attestation checkbox,
	// never a birthdate). Contextual, so validated here rather than in
	// registerRequest.Validate.
	if minAge := s.registrationMinimumAge(); minAge > 0 && !in.AgeAttestation {
		return &ValidationError{Fields: []FieldError{{
			Field:   "age_attestation",
			Message: fmt.Sprintf("you must confirm you are at least %d years old", minAge),
		}}}
	}
	regInput := auth.RegisterInput{Username: in.Username, Email: in.Email, Password: in.Password}

	// When the instance requires approval, signup files a pending request instead
	// of creating an account + session. The response reveals no token. The
	// email-verification gate composes AFTER approval (approve → hold until
	// verified), applied in ApproveRegistration.
	if s.registrationRequiresApproval() {
		if _, err := s.authsvc.RequestRegistration(c.Request().Context(), regInput, in.Note); err != nil {
			if errors.Is(err, auth.ErrOwnerClaimRequired) {
				return &OwnerClaimRequiredError{}
			}
			if errors.Is(err, auth.ErrConflict) {
				return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
			}
			return err
		}
		s.audit(c, observability.ActionRegistrationRequest, observability.ResultSuccess, "", "pending_approval")
		return c.JSON(http.StatusAccepted, map[string]string{"status": "pending"})
	}

	// Email-verification hold (registration_require_email_verification AND a
	// mail path): the account is created pending, no session is issued, and the
	// verification message is sent. 202 mirrors the approval flow's shape.
	if s.registrationRequiresEmailVerification() {
		user, err := s.authsvc.RegisterPendingVerification(c.Request().Context(), regInput)
		if err != nil {
			if errors.Is(err, auth.ErrOwnerClaimRequired) {
				return &OwnerClaimRequiredError{}
			}
			if errors.Is(err, auth.ErrConflict) {
				return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
			}
			if user.ID == uuid.Nil {
				return err
			}
			// The account exists held but the verification send failed: still
			// 202 (a retry would 409 on the created account); the operator
			// recovery path is the admin email_verified override.
			s.logger.Warn("registration verification mail failed", "user_id", user.ID, "error", err)
		}
		s.invalidateUserCount()
		s.audit(c, observability.ActionRegister, observability.ResultSuccess, user.ID.String(), "pending_email_verification")
		return c.JSON(http.StatusAccepted, map[string]string{"status": "verification_pending"})
	}

	user, tokens, err := s.authsvc.Register(c.Request().Context(), regInput, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrOwnerClaimRequired) {
			return &OwnerClaimRequiredError{}
		}
		if errors.Is(err, auth.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	s.invalidateUserCount()
	s.audit(c, observability.ActionRegister, observability.ResultSuccess, user.ID.String(), "")
	cookieMode := in.CookieMode || refreshCookieToken(c) != ""
	return s.authResponse(http.StatusCreated, c, user, tokens, cookieMode)
}

// handleMe returns the authenticated account. It runs behind requireAuth, so the
// principal is always present; it reloads the user so the response reflects the
// current database state (role, email_verified, …) rather than stale token claims.
func (s *Server) handleMe(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	user, err := s.authsvc.UserByID(c.Request().Context(), userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	view := newUserView(user)
	s.attachUserImageFlags(c.Request().Context(), &view, userID)
	return c.JSON(http.StatusOK, view)
}

// updateProfileRequest is the PATCH /api/v1/auth/me body. Fields are optional;
// only those present are changed. Identity fields (username, email) are not
// editable here.
type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Bio         *string `json:"bio"`
	// Unlisted toggles the §16 discovery opt-out.
	Unlisted *bool `json:"unlisted"`
	// HistoryEnabled toggles the per-user watch-history preference (W7).
	HistoryEnabled *bool `json:"history_enabled"`
	ProfilePublic  *bool `json:"profile_public"`
	// ShowBluesky toggles displaying the linked Bluesky/ATProto handle on the
	// public profile (0102). Default false.
	ShowBluesky *bool `json:"show_bluesky"`
	// Search & recommendation preferences (search-service W4).
	SearchHistoryEnabled               *bool `json:"search_history_enabled"`
	PersonalizedSearchEnabled          *bool `json:"personalized_search_enabled"`
	PersonalizedRecommendationsEnabled *bool `json:"personalized_recommendations_enabled"`
	// SensitiveContentPolicy sets the per-user sensitive-content override (0100):
	// one of hide|warn|blur|display, or "" to clear it (inherit the instance
	// policy). Any other value is rejected.
	SensitiveContentPolicy *string `json:"sensitive_content_policy"`
}

func (r updateProfileRequest) Validate() []FieldError {
	var fes []FieldError
	if r.DisplayName == nil && r.Bio == nil && r.Unlisted == nil && r.HistoryEnabled == nil && r.ProfilePublic == nil &&
		r.ShowBluesky == nil &&
		r.SearchHistoryEnabled == nil && r.PersonalizedSearchEnabled == nil && r.PersonalizedRecommendationsEnabled == nil &&
		r.SensitiveContentPolicy == nil {
		return []FieldError{{Field: "display_name", Message: "at least one profile field is required"}}
	}
	if r.DisplayName != nil && len(strings.TrimSpace(*r.DisplayName)) > 50 {
		fes = append(fes, FieldError{Field: "display_name", Message: "must be at most 50 characters"})
	}
	if r.Bio != nil && len(*r.Bio) > 1000 {
		fes = append(fes, FieldError{Field: "bio", Message: "must be at most 1000 characters"})
	}
	// A provided policy must be empty (clear-to-inherit) or one of the four enum
	// strings; anything else is a validation error.
	if r.SensitiveContentPolicy != nil {
		if v := strings.TrimSpace(*r.SensitiveContentPolicy); v != "" && !instancesettings.IsSensitiveContentPolicy(v) {
			fes = append(fes, FieldError{Field: "sensitive_content_policy", Message: "must be one of hide, warn, blur, display, or empty to inherit"})
		}
	}
	return fes
}

// handleUpdateMe updates the authenticated account's profile (display name, bio).
func (s *Server) handleUpdateMe(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in updateProfileRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, err := s.authsvc.UpdateProfile(c.Request().Context(), userID, auth.ProfileInput{
		DisplayName:                        in.DisplayName,
		Bio:                                in.Bio,
		Unlisted:                           in.Unlisted,
		HistoryEnabled:                     in.HistoryEnabled,
		ProfilePublic:                      in.ProfilePublic,
		ShowBluesky:                        in.ShowBluesky,
		SearchHistoryEnabled:               in.SearchHistoryEnabled,
		PersonalizedSearchEnabled:          in.PersonalizedSearchEnabled,
		PersonalizedRecommendationsEnabled: in.PersonalizedRecommendationsEnabled,
		SensitiveContentPolicy:             in.SensitiveContentPolicy,
	})
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	// IPFS mirror re-evaluation (fix_plan P19): toggling the discovery opt-out
	// changes the eligibility of the owner's avatar/banner AND every one of their
	// videos' derivatives (unlisted is private for mirroring, spec §7). Round-2 audit
	// (MAJOR): this ENQUEUES a durable per-user re-eval job — a single cheap DB write —
	// instead of running the SyncVideo fan-out inline on the request goroutine, which
	// could strand a now-unlisted owner's videos pinned if the loop was slow or the
	// client disconnected mid-loop. The mirror worker expands the job off the request
	// path with per-video error isolation; the periodic eligibility sweep is the
	// backstop. Best-effort — a mirror hiccup never fails the profile update.
	if in.Unlisted != nil && s.ipfsmirrorsvc != nil {
		if err := s.ipfsmirrorsvc.EnqueueUserReeval(c.Request().Context(), userID); err != nil {
			s.logger.Warn("ipfs mirror reeval enqueue failed", "user_id", userID, "error", err)
		}
	}
	// Search: an unlisted toggle suppresses/restores the owner's docs in the
	// index (search-service W4). Best-effort.
	if in.Unlisted != nil {
		s.searchEvents.EnqueueUserSuppress(c.Request().Context(), userID, *in.Unlisted)
	}
	view := newUserView(user)
	s.attachUserImageFlags(c.Request().Context(), &view, userID)
	return c.JSON(http.StatusOK, view)
}

// mfaRequiredResponse is the login response for an MFA-enabled account: no
// session tokens, just the short-lived single-purpose mfa_token to present at
// POST /api/v1/auth/mfa/challenge together with a TOTP or recovery code.
type mfaRequiredResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
}

// handleLogin verifies credentials and returns an access + refresh token — or,
// when the account has TOTP MFA enabled, an MFA challenge (mfa_required +
// mfa_token) WITHOUT any session tokens.
func (s *Server) handleLogin(c echo.Context) error {
	var in loginRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	res, err := s.authsvc.Login(c.Request().Context(), auth.LoginInput{
		Email:      in.Email,
		Identifier: in.Identifier,
		Password:   in.Password,
	}, c.Request().UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			// No actor_id: the account is unknown or the password was wrong, and
			// we must not leak which (or the attempted identifier — it is PII
			// when it is an email). Identifier-neutral wording, because the
			// caller may have signed in with a username.
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "invalid_credentials")
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
		case errors.Is(err, auth.ErrAccountDisabled):
			// Login returns an empty user on this path, so no actor_id is available.
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "account_disabled")
			return echo.NewHTTPError(http.StatusForbidden, "account is disabled")
		case errors.Is(err, auth.ErrEmailVerificationRequired):
			// W7 verification hold: valid credentials, but the account stays
			// sessionless until its email is verified. A typed code so the
			// login form can show "check your email" instead of a dead end.
			s.audit(c, observability.ActionLogin, observability.ResultFailure, "", "email_verification_required")
			return &EmailVerificationRequiredError{}
		}
		return err
	}
	if res.MFARequired {
		// Credentials verified, but the session is withheld until the MFA
		// challenge completes (audited there as auth.mfa.challenge).
		s.audit(c, observability.ActionLogin, observability.ResultSuccess, res.User.ID.String(), "mfa_required")
		return c.JSON(http.StatusOK, mfaRequiredResponse{MFARequired: true, MFAToken: res.MFAToken})
	}
	s.audit(c, observability.ActionLogin, observability.ResultSuccess, res.User.ID.String(), "")
	cookieMode := in.CookieMode || refreshCookieToken(c) != ""
	return s.authResponse(http.StatusOK, c, res.User, res.Tokens, cookieMode)
}

// refreshRequest is the POST /api/v1/auth/refresh and /logout body. The token
// may be omitted when the request carries the vidra_refresh cookie (cookie
// mode); presence of one or the other is enforced in the handler after cookie
// resolution.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	// CookieMode opts the rotated session into cookie mode even when the token
	// arrived in the body. A cookie-supplied token implies cookie mode.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

// errRefreshTokenRequired is the 422 returned when neither the body nor the
// vidra_refresh cookie supplies a refresh token.
func errRefreshTokenRequired() error {
	return &ValidationError{Fields: []FieldError{
		{Field: "refresh_token", Message: "is required (in the body or the vidra_refresh cookie)"},
	}}
}

// handleRefresh rotates a refresh token, returning a new access + refresh pair.
// The token comes from the body or, when absent, from the vidra_refresh cookie;
// in cookie mode rotation re-sets the cookie and the body omits the raw token.
func (s *Server) handleRefresh(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	raw, fromCookie := resolveRefreshToken(c, in.RefreshToken)
	if raw == "" {
		return errRefreshTokenRequired()
	}
	cookieMode := in.CookieMode || fromCookie
	user, tokens, err := s.authsvc.Refresh(c.Request().Context(), raw, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidRefresh) {
			// A dead cookie would be re-presented on every future attempt (each
			// one tripping reuse detection), so clear it alongside the 401.
			if fromCookie {
				s.clearRefreshCookie(c)
			}
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired refresh token")
		}
		return err
	}
	return s.authResponse(http.StatusOK, c, user, tokens, cookieMode)
}

// handleLogoutAll revokes every active session for the authenticated user
// ("sign out everywhere"). It runs behind requireAuth, so the principal is
// always present, and returns 204.
func (s *Server) handleLogoutAll(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.authsvc.LogoutAll(c.Request().Context(), userID); err != nil {
		return err
	}
	// Clear the cookie-mode session cookie too (harmless when none was set).
	s.clearRefreshCookie(c)
	s.audit(c, observability.ActionLogoutAll, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}

// handleLogout revokes the session for the presented refresh token — from the
// body or, when absent, from the vidra_refresh cookie. It is idempotent and
// always returns 204, never revealing whether the token existed. The response
// clears the cookie so a browser is always left signed out.
func (s *Server) handleLogout(c echo.Context) error {
	var in refreshRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	raw, _ := resolveRefreshToken(c, in.RefreshToken)
	if raw == "" {
		return errRefreshTokenRequired()
	}
	if err := s.authsvc.Logout(c.Request().Context(), raw); err != nil {
		return err
	}
	// Clear the cookie-mode session cookie too (harmless when none was set).
	s.clearRefreshCookie(c)
	// Idempotent: no actor_id, since the token may be unknown/already-revoked.
	s.audit(c, observability.ActionLogout, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// passwordResetRequest is the POST /api/v1/auth/password-reset body.
type passwordResetRequest struct {
	Email string `json:"email"`
}

func (r passwordResetRequest) Validate() []FieldError {
	if !looksLikeEmail(r.Email) {
		return []FieldError{{Field: "email", Message: "must be a valid email"}}
	}
	return nil
}

// handleRequestPasswordReset starts the password-reset flow. It always returns
// 202 Accepted — it never reveals whether the email belongs to an account, so it
// cannot be used to enumerate registered users. A matching, active account is
// issued a single-use reset token delivered by the mailer adapter.
func (s *Server) handleRequestPasswordReset(c echo.Context) error {
	var in passwordResetRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.RequestPasswordReset(c.Request().Context(), in.Email); err != nil {
		return err
	}
	// No actor_id/email: enumeration-safe, so the event records only that a reset
	// was requested, not for whom.
	s.audit(c, observability.ActionPasswordResetRequest, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusAccepted)
}

// passwordResetConfirmRequest is the POST /api/v1/auth/password-reset/confirm body.
type passwordResetConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (r passwordResetConfirmRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Token) == "" {
		fes = append(fes, FieldError{Field: "token", Message: "is required"})
	}
	switch {
	case len(r.Password) < 8:
		fes = append(fes, FieldError{Field: "password", Message: "must be at least 8 characters"})
	case len(r.Password) > maxPasswordLen:
		fes = append(fes, FieldError{Field: "password", Message: "must be at most 72 characters"})
	}
	return fes
}

// handleConfirmPasswordReset completes the reset: a valid token sets the new
// password and signs the account out everywhere (all sessions revoked). An
// unknown, used, or expired token is a 400; the token is never echoed back.
func (s *Server) handleConfirmPasswordReset(c echo.Context) error {
	var in passwordResetConfirmRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.ResetPassword(c.Request().Context(), in.Token, in.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidResetToken) {
			s.audit(c, observability.ActionPasswordResetComplete, observability.ResultFailure, "", "invalid_token")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired reset token")
		}
		return err
	}
	s.audit(c, observability.ActionPasswordResetComplete, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// handleRequestEmailVerification issues a verification message for the
// authenticated account. It runs behind requireAuth, so the principal is always
// present. It always returns 202, and is a no-op for an already-verified account.
func (s *Server) handleRequestEmailVerification(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.authsvc.RequestEmailVerification(c.Request().Context(), userID); err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionEmailVerifyRequest, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusAccepted)
}

// emailVerificationConfirmRequest is the POST /api/v1/auth/verify-email/confirm body.
type emailVerificationConfirmRequest struct {
	Token string `json:"token"`
}

func (r emailVerificationConfirmRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Token) == "" {
		return []FieldError{{Field: "token", Message: "is required"}}
	}
	return nil
}

// handleConfirmEmailVerification marks the account's email verified given a valid
// token. It is public — the user may follow the link while logged out. 204 on
// success; an unknown, used, or expired token is a 400.
func (s *Server) handleConfirmEmailVerification(c echo.Context) error {
	var in emailVerificationConfirmRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.VerifyEmail(c.Request().Context(), in.Token); err != nil {
		if errors.Is(err, auth.ErrInvalidVerificationToken) {
			s.audit(c, observability.ActionEmailVerifyConfirm, observability.ResultFailure, "", "invalid_token")
			return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired verification token")
		}
		return err
	}
	s.audit(c, observability.ActionEmailVerifyConfirm, observability.ResultSuccess, "", "")
	return c.NoContent(http.StatusNoContent)
}

// deactivateAccountRequest is the POST /api/v1/auth/me/deactivate body. The
// current password is required to confirm a sensitive self-service action (so a
// stolen access token alone cannot disable the account).
type deactivateAccountRequest struct {
	Password string `json:"password"`
}

func (r deactivateAccountRequest) Validate() []FieldError {
	if r.Password == "" {
		return []FieldError{{Field: "password", Message: "is required"}}
	}
	return nil
}

// handleDeactivateAccount disables the authenticated account after confirming
// its password, signing it out everywhere. Behind requireAuth. 204 on success;
// a wrong password is 403. Deactivation is reversible by an administrator; hard
// deletion is a separate, policy-gated flow.
func (s *Server) handleDeactivateAccount(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in deactivateAccountRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.authsvc.DeactivateAccount(c.Request().Context(), userID, in.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidPassword):
			s.audit(c, observability.ActionAccountDeactivate, observability.ResultFailure, userID.String(), "invalid_password")
			return echo.NewHTTPError(http.StatusForbidden, "incorrect password")
		case errors.Is(err, auth.ErrAccountNotFound):
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	s.audit(c, observability.ActionAccountDeactivate, observability.ResultSuccess, userID.String(), "")
	return c.NoContent(http.StatusNoContent)
}
