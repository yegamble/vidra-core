package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// donationAddressView is the public projection of a donation address. It NEVER
// exposes the verification nonce/expiry (internal challenge bookkeeping) — only
// the address itself and whether ownership was cryptographically proven. There
// is deliberately no balance/amount field: Vidra is non-custodial and holds no
// funds.
type donationAddressView struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	ChannelID *string   `json:"channel_id,omitempty"`
	Network   string    `json:"network"`
	Address   string    `json:"address"`
	Label     string    `json:"label"`
	Verified  bool      `json:"verified"`
	CreatedAt time.Time `json:"created_at"`
}

func newDonationAddressView(a sqlcgen.DonationAddress) donationAddressView {
	v := donationAddressView{
		ID:        a.ID.String(),
		OwnerID:   a.OwnerID.String(),
		Network:   a.Network,
		Address:   a.Address,
		Label:     a.Label,
		Verified:  a.Verified,
		CreatedAt: a.CreatedAt,
	}
	if a.ChannelID.Valid {
		id := uuid.UUID(a.ChannelID.Bytes).String()
		v.ChannelID = &id
	}
	return v
}

// donationAddressListResponse wraps a list of donation addresses.
type donationAddressListResponse struct {
	Addresses []donationAddressView `json:"addresses"`
}

func donationListResponse(rows []sqlcgen.DonationAddress) donationAddressListResponse {
	views := make([]donationAddressView, 0, len(rows))
	for _, r := range rows {
		views = append(views, newDonationAddressView(r))
	}
	return donationAddressListResponse{Addresses: views}
}

// addDonationAddressRequest is the POST /api/v1/me/donation-addresses body.
type addDonationAddressRequest struct {
	// ChannelID is optional: empty makes this an account-level (profile)
	// address; a UUID scopes it to a channel the caller owns.
	ChannelID string `json:"channel_id"`
	Network   string `json:"network"`
	Address   string `json:"address"`
	Label     string `json:"label"`
}

func (r addDonationAddressRequest) Validate() []FieldError {
	var fes []FieldError
	network := strings.ToLower(strings.TrimSpace(r.Network))
	address := strings.TrimSpace(r.Address)
	switch {
	case !donation.IsKnownNetwork(network):
		fes = append(fes, FieldError{Field: "network", Message: "must be one of: " + strings.Join(donation.SupportedNetworks(), ", ")})
	case !donation.ValidAddress(network, address):
		fes = append(fes, FieldError{Field: "address", Message: "is not a valid " + network + " address"})
	}
	if strings.TrimSpace(r.ChannelID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(r.ChannelID)); err != nil {
			fes = append(fes, FieldError{Field: "channel_id", Message: "must be a valid channel id"})
		}
	}
	if len(r.Label) > 60 {
		fes = append(fes, FieldError{Field: "label", Message: "must be at most 60 characters"})
	}
	return fes
}

// handleAddDonationAddress adds a donation address for the authenticated user.
func (s *Server) handleAddDonationAddress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in addDonationAddressRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	var channelID *uuid.UUID
	if raw := strings.TrimSpace(in.ChannelID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid channel id")
		}
		channelID = &id
	}
	row, err := s.donationsvc.Add(c.Request().Context(), userID, donation.AddInput{
		ChannelID: channelID,
		Network:   in.Network,
		Address:   in.Address,
		Label:     in.Label,
	})
	if err != nil {
		return donationError(err)
	}
	return c.JSON(http.StatusCreated, newDonationAddressView(row))
}

// handleListMyDonationAddresses lists all of the caller's donation addresses
// (account-level and channel-scoped).
func (s *Server) handleListMyDonationAddresses(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	rows, err := s.donationsvc.ListOwn(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, donationListResponse(rows))
}

// handleDeleteDonationAddress removes one of the caller's donation addresses.
func (s *Server) handleDeleteDonationAddress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "donation address not found")
	if err != nil {
		return err
	}
	if err := s.donationsvc.Delete(c.Request().Context(), userID, id); err != nil {
		return donationError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// donationChallengeResponse is the POST .../challenge body: the message the
// owner must sign with the address's private key, and when it expires.
type donationChallengeResponse struct {
	Message   string    `json:"message"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleChallengeDonationAddress issues a proof-of-control challenge for one of
// the caller's addresses. Networks without a signing path answer 501.
func (s *Server) handleChallengeDonationAddress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "donation address not found")
	if err != nil {
		return err
	}
	message, expiresAt, err := s.donationsvc.Challenge(c.Request().Context(), userID, id)
	if err != nil {
		return donationError(err)
	}
	return c.JSON(http.StatusCreated, donationChallengeResponse{Message: message, ExpiresAt: expiresAt})
}

// verifyDonationAddressRequest is the POST .../verify body.
type verifyDonationAddressRequest struct {
	Signature string `json:"signature"`
}

func (r verifyDonationAddressRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Signature) == "" {
		return []FieldError{{Field: "signature", Message: "is required"}}
	}
	return nil
}

// handleVerifyDonationAddress verifies a signed challenge and, on success, marks
// the address verified. Networks without a signing path answer 501.
func (s *Server) handleVerifyDonationAddress(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "donation address not found")
	if err != nil {
		return err
	}
	var in verifyDonationAddressRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	row, err := s.donationsvc.Verify(c.Request().Context(), userID, id, in.Signature)
	if err != nil {
		return donationError(err)
	}
	// A creator proving control of a wallet is a security-relevant account
	// action: audit it with the safe address id + network only — never the
	// signature, nonce, or any key material.
	s.audit(c, observability.ActionDonationVerify, observability.ResultSuccess, userID.String(), id.String()+" "+row.Network)
	return c.JSON(http.StatusOK, newDonationAddressView(row))
}

// handleListUserDonationAddresses lists a user's PUBLIC (account-level)
// donation addresses. No auth required; only what the owner added is exposed.
func (s *Server) handleListUserDonationAddresses(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		// An unknown/invalid user simply has no public addresses.
		return c.JSON(http.StatusOK, donationAddressListResponse{Addresses: []donationAddressView{}})
	}
	rows, err := s.donationsvc.ListForUser(c.Request().Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, donationListResponse(rows))
}

// handleListChannelDonationAddresses lists a channel's PUBLIC donation
// addresses, resolved by handle. No auth required.
func (s *Server) handleListChannelDonationAddresses(c echo.Context) error {
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	}
	rows, err := s.donationsvc.ListForChannel(ctx, ch.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, donationListResponse(rows))
}

// donationError maps donation service sentinels to HTTP error envelopes.
func donationError(err error) error {
	switch {
	case errors.Is(err, donation.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "donation address not found")
	case errors.Is(err, donation.ErrChannelNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	case errors.Is(err, donation.ErrForbidden):
		return echo.NewHTTPError(http.StatusForbidden, "you do not own this resource")
	case errors.Is(err, donation.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, "this donation address already exists")
	case errors.Is(err, donation.ErrInvalidNetwork):
		return &ValidationError{Fields: []FieldError{{Field: "network", Message: "unsupported network"}}}
	case errors.Is(err, donation.ErrInvalidAddress):
		return &ValidationError{Fields: []FieldError{{Field: "address", Message: "invalid address for this network"}}}
	case errors.Is(err, donation.ErrVerificationUnsupported):
		return echo.NewHTTPError(http.StatusNotImplemented, "address ownership verification is not supported for this network")
	case errors.Is(err, donation.ErrNoChallenge):
		return echo.NewHTTPError(http.StatusConflict, "no active verification challenge; request one first")
	case errors.Is(err, donation.ErrChallengeExpired):
		return echo.NewHTTPError(http.StatusConflict, "the verification challenge expired; request a new one")
	case errors.Is(err, donation.ErrBadSignature):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "signature is malformed")
	case errors.Is(err, donation.ErrSignatureMismatch):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "signature did not verify for this address")
	default:
		return err
	}
}
