package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
)

// First-run owner claim (0104): the admin account is created by redeeming the
// one-time setup token boot printed to the operator console — never by winning
// the registration race. While the claim is pending every signup endpoint
// answers 403 owner_claim_required; this endpoint is the only way forward.

// claimOwnerRequest is the POST /api/v1/setup/claim-owner body. Account fields
// follow the register rules; the token is the boot-logged owner-claim token.
type claimOwnerRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// CookieMode opts the new session into cookie mode: the refresh token is
	// set as an httpOnly vidra_refresh cookie and omitted from the body.
	CookieMode bool `json:"cookie_mode,omitempty"`
}

func (r claimOwnerRequest) Validate() []FieldError {
	var fes []FieldError
	if strings.TrimSpace(r.Token) == "" {
		fes = append(fes, FieldError{Field: "token", Message: "is required"})
	}
	name := strings.TrimSpace(r.Username)
	switch {
	case name == "":
		fes = append(fes, FieldError{Field: "username", Message: "is required"})
	case len(name) < 3 || len(name) > 30:
		fes = append(fes, FieldError{Field: "username", Message: "must be 3–30 characters"})
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
	return fes
}

// handleClaimOwner redeems the one-time owner-claim token for THE admin
// account plus a session. Unauthenticated by design — the token IS the
// credential — constant-time compared and single-winner at the database in
// the auth service. Wrong/redeemed/absent tokens all answer the same 403
// owner_claim_invalid, so the endpoint reveals nothing about claim state.
func (s *Server) handleClaimOwner(c echo.Context) error {
	var in claimOwnerRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	user, tokens, err := s.authsvc.ClaimOwner(c.Request().Context(), auth.ClaimOwnerInput{
		Token:    in.Token,
		Username: in.Username,
		Email:    in.Email,
		Password: in.Password,
	}, c.Request().UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrOwnerClaimInvalid) {
			s.audit(c, observability.ActionOwnerClaim, observability.ResultFailure, "", "invalid_token")
			return &OwnerClaimInvalidError{}
		}
		if errors.Is(err, auth.ErrConflict) {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return err
	}
	s.invalidateUserCount()
	s.audit(c, observability.ActionOwnerClaim, observability.ResultSuccess, user.ID.String(), "")
	// Cookie mode is opted into via the explicit flag only: the vidra_refresh
	// cookie is scoped Path=/api/v1/auth, so it is never presented to this
	// endpoint and the register/login cookie-detection fallback can't fire here.
	return s.authResponse(http.StatusCreated, c, user, tokens, in.CookieMode)
}
