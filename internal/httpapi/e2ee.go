package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/observability"
)

// e2eeDeviceView is the projection of a registered E2EE device. Only PUBLIC
// key material is ever stored or returned; private keys never leave clients.
type e2eeDeviceView struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	DeviceName  string    `json:"device_name"`
	IdentityKey string    `json:"identity_key"`
	SigningKey  string    `json:"signing_key"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

func newE2EEDeviceView(d e2ee.Device) e2eeDeviceView {
	return e2eeDeviceView{
		ID:          d.ID.String(),
		UserID:      d.UserID.String(),
		DeviceName:  d.DeviceName,
		IdentityKey: d.IdentityKey,
		SigningKey:  d.SigningKey,
		CreatedAt:   d.CreatedAt,
		LastSeenAt:  d.LastSeenAt,
	}
}

type e2eeDeviceListResponse struct {
	Devices []e2eeDeviceView `json:"devices"`
}

// registerE2EEDeviceRequest is the POST /e2ee/devices body.
type registerE2EEDeviceRequest struct {
	DeviceName  string `json:"device_name"`
	IdentityKey string `json:"identity_key"`
	SigningKey  string `json:"signing_key"`
}

func (r registerE2EEDeviceRequest) Validate() []FieldError {
	var errs []FieldError
	if strings.TrimSpace(r.DeviceName) == "" {
		errs = append(errs, FieldError{Field: "device_name", Message: "is required"})
	}
	if strings.TrimSpace(r.IdentityKey) == "" {
		errs = append(errs, FieldError{Field: "identity_key", Message: "is required"})
	}
	if strings.TrimSpace(r.SigningKey) == "" {
		errs = append(errs, FieldError{Field: "signing_key", Message: "is required"})
	}
	return errs
}

// oneTimeKeyIn is one public prekey in an upload batch.
type oneTimeKeyIn struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"`
}

// uploadOneTimeKeysRequest is the POST /e2ee/devices/{id}/one-time-keys body.
type uploadOneTimeKeysRequest struct {
	OneTimeKeys []oneTimeKeyIn `json:"one_time_keys"`
}

func (r uploadOneTimeKeysRequest) Validate() []FieldError {
	if len(r.OneTimeKeys) == 0 {
		return []FieldError{{Field: "one_time_keys", Message: "is required"}}
	}
	return nil
}

type oneTimeKeyCountResponse struct {
	Count int64 `json:"count"`
}

type uploadOneTimeKeysResponse struct {
	Unclaimed int64 `json:"unclaimed"`
}

// e2eeClaimView is one device's claim result: its public identity plus one
// atomically claimed one-time key (null when the device has none left).
type e2eeClaimView struct {
	DeviceID    string        `json:"device_id"`
	IdentityKey string        `json:"identity_key"`
	SigningKey  string        `json:"signing_key"`
	OneTimeKey  *oneTimeKeyIn `json:"one_time_key"`
}

type e2eeClaimResponse struct {
	UserID string          `json:"user_id"`
	Claims []e2eeClaimView `json:"claims"`
}

// encryptedMessageView is a stored envelope as returned to a recipient. The
// ciphertext is byte-identical to what the sender posted (§3 invariant).
type encryptedMessageView struct {
	ID                string     `json:"id"`
	ConversationID    string     `json:"conversation_id"`
	SenderUserID      string     `json:"sender_user_id"`
	SenderDeviceID    string     `json:"sender_device_id"`
	RecipientDeviceID string     `json:"recipient_device_id"`
	MessageType       int32      `json:"message_type"`
	Ciphertext        string     `json:"ciphertext"`
	CreatedAt         time.Time  `json:"created_at"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

type encryptedMessageListResponse struct {
	Envelopes []encryptedMessageView `json:"envelopes"`
	Limit     int                    `json:"limit"`
	Offset    int                    `json:"offset"`
}

// sendEncryptedResponse summarises an accepted encrypted send.
type sendEncryptedResponse struct {
	ConversationID string     `json:"conversation_id"`
	EnvelopeCount  int        `json:"envelope_count"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// e2eeError maps the e2ee service's sentinel errors to HTTP responses shared
// by all E2EE handlers. Returns nil when err is not an e2ee sentinel.
func e2eeError(err error) error {
	var invalid *e2ee.InvalidError
	switch {
	case errors.As(err, &invalid):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, invalid.Reason)
	case errors.Is(err, e2ee.ErrCannotMessageSelf):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "cannot message yourself")
	case errors.Is(err, e2ee.ErrRecipientNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "recipient not found")
	case errors.Is(err, e2ee.ErrNotParticipant):
		return echo.NewHTTPError(http.StatusNotFound, "conversation not found")
	case errors.Is(err, e2ee.ErrBlocked):
		return echo.NewHTTPError(http.StatusForbidden, "cannot message this user")
	case errors.Is(err, e2ee.ErrDeviceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	case errors.Is(err, e2ee.ErrNotAuthorized):
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	case errors.Is(err, e2ee.ErrTooManyDevices):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "device limit reached")
	case errors.Is(err, e2ee.ErrTooManyKeys):
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "one-time key limit reached")
	}
	return nil
}

// handleRegisterE2EEDevice registers an E2EE device (public keys + label) for
// the caller. Behind requireAuth.
func (s *Server) handleRegisterE2EEDevice(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in registerE2EEDeviceRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	d, err := s.e2eesvc.RegisterDevice(c.Request().Context(), userID,
		strings.TrimSpace(in.DeviceName), strings.TrimSpace(in.IdentityKey), strings.TrimSpace(in.SigningKey))
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	return c.JSON(http.StatusCreated, newE2EEDeviceView(d))
}

// handleListMyE2EEDevices returns the caller's registered devices, oldest
// first. Behind requireAuth. Also the frontend's E2EE availability probe.
func (s *Server) handleListMyE2EEDevices(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	devices, err := s.e2eesvc.ListOwnDevices(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	views := make([]e2eeDeviceView, 0, len(devices))
	for _, d := range devices {
		views = append(views, newE2EEDeviceView(d))
	}
	return c.JSON(http.StatusOK, e2eeDeviceListResponse{Devices: views})
}

// handleDeleteE2EEDevice removes one of the caller's devices (cascading its
// one-time keys and envelopes). Behind requireAuth. Unknown-or-not-owned → 404.
func (s *Server) handleDeleteE2EEDevice(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	}
	if err := s.e2eesvc.DeleteDevice(c.Request().Context(), userID, deviceID); err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListUserE2EEDevices returns a peer's devices (public keys only).
// Behind requireAuth and participant-gated: unless the target is the caller,
// they must share a conversation — otherwise 404, so accounts and device
// counts cannot be enumerated. A block either direction → 403.
func (s *Server) handleListUserE2EEDevices(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	devices, err := s.e2eesvc.ListUserDevices(c.Request().Context(), callerID, targetID)
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	views := make([]e2eeDeviceView, 0, len(devices))
	for _, d := range devices {
		views = append(views, newE2EEDeviceView(d))
	}
	return c.JSON(http.StatusOK, e2eeDeviceListResponse{Devices: views})
}

// handleUploadOneTimeKeys stores a batch of public one-time prekeys for one of
// the caller's devices (idempotent per key_id). Behind requireAuth. Not-owned
// device → 404.
func (s *Server) handleUploadOneTimeKeys(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	}
	var in uploadOneTimeKeysRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	keys := make([]e2ee.OneTimeKey, 0, len(in.OneTimeKeys))
	for _, k := range in.OneTimeKeys {
		keys = append(keys, e2ee.OneTimeKey{KeyID: strings.TrimSpace(k.KeyID), Key: strings.TrimSpace(k.Key)})
	}
	unclaimed, err := s.e2eesvc.UploadOneTimeKeys(c.Request().Context(), userID, deviceID, keys)
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	return c.JSON(http.StatusCreated, uploadOneTimeKeysResponse{Unclaimed: unclaimed})
}

// handleCountOneTimeKeys returns the unclaimed one-time-key count of one of
// the caller's devices. Behind requireAuth. Not-owned device → 404.
func (s *Server) handleCountOneTimeKeys(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "device not found")
	}
	count, err := s.e2eesvc.CountOneTimeKeys(c.Request().Context(), userID, deviceID)
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	return c.JSON(http.StatusOK, oneTimeKeyCountResponse{Count: count})
}

// handleClaimOneTimeKeys atomically claims one one-time key per device of the
// target user. Behind requireAuth and participant-gated like the peer device
// listing. Audited with COUNTS ONLY — key material never reaches the audit
// trail or logs.
func (s *Server) handleClaimOneTimeKeys(c echo.Context) error {
	callerID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	claims, err := s.e2eesvc.ClaimKeys(c.Request().Context(), callerID, targetID)
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	claimed := 0
	views := make([]e2eeClaimView, 0, len(claims))
	for _, cl := range claims {
		v := e2eeClaimView{DeviceID: cl.DeviceID.String(), IdentityKey: cl.IdentityKey, SigningKey: cl.SigningKey}
		if cl.OneTimeKey != nil {
			claimed++
			v.OneTimeKey = &oneTimeKeyIn{KeyID: cl.OneTimeKey.KeyID, Key: cl.OneTimeKey.Key}
		}
		views = append(views, v)
	}
	s.audit(c, observability.ActionE2EEClaim, observability.ResultSuccess, callerID.String(),
		fmt.Sprintf("target=%s devices=%d claimed=%d", targetID, len(claims), claimed))
	return c.JSON(http.StatusOK, e2eeClaimResponse{UserID: targetID.String(), Claims: views})
}

// sendEncryptedMessage stores the fan-out envelopes of an encrypted send. It
// is dispatched from handleSendMessage once the conversation is known to be
// encrypted and the caller a participant. Mirrors the plaintext path's
// notification side effect (metadata only — no content).
func (s *Server) sendEncryptedMessage(c echo.Context, userID, convID uuid.UUID, in sendMessageRequest) error {
	senderDeviceID, err := uuid.Parse(strings.TrimSpace(in.SenderDeviceID))
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "sender_device_id", Message: "must be a valid id"}}}
	}
	envelopes := make([]e2ee.Envelope, 0, len(in.Envelopes))
	for _, env := range in.Envelopes {
		rid, err := uuid.Parse(strings.TrimSpace(env.RecipientDeviceID))
		if err != nil {
			return &ValidationError{Fields: []FieldError{{Field: "envelopes", Message: "recipient_device_id must be a valid id"}}}
		}
		envelopes = append(envelopes, e2ee.Envelope{
			RecipientDeviceID: rid,
			MessageType:       env.MessageType,
			Ciphertext:        env.Ciphertext,
		})
	}
	ctx := c.Request().Context()
	res, err := s.e2eesvc.Send(ctx, userID, convID, senderDeviceID, envelopes, in.ExpiresInSeconds)
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	// Best-effort: notify the recipient of the new message (never the content —
	// the notification carries only the conversation id and the sender).
	if s.notifsvc != nil {
		if other, oerr := s.e2eesvc.OtherParticipant(ctx, convID, userID); oerr == nil {
			if nerr := s.notifsvc.NotifyMessage(ctx, other, userID, convID); nerr != nil {
				s.logger.WarnContext(ctx, "notify message failed", "error", nerr, "conversation_id", convID)
			}
		}
	}
	return c.JSON(http.StatusCreated, sendEncryptedResponse{
		ConversationID: res.ConversationID.String(),
		EnvelopeCount:  res.EnvelopeCount,
		CreatedAt:      res.CreatedAt,
		ExpiresAt:      res.ExpiresAt,
	})
}

// listEncryptedMessages returns the caller's devices' envelopes in an
// encrypted conversation, newest first (expired ones filtered). Dispatched
// from handleListMessages.
func (s *Server) listEncryptedMessages(c echo.Context, userID, convID uuid.UUID, limit, offset int) error {
	msgs, err := s.e2eesvc.ListMessages(c.Request().Context(), userID, convID, int32(limit), int32(offset))
	if err != nil {
		if herr := e2eeError(err); herr != nil {
			return herr
		}
		return err
	}
	views := make([]encryptedMessageView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, encryptedMessageView{
			ID:                m.ID.String(),
			ConversationID:    m.ConversationID.String(),
			SenderUserID:      m.SenderUserID.String(),
			SenderDeviceID:    m.SenderDeviceID.String(),
			RecipientDeviceID: m.RecipientDeviceID.String(),
			MessageType:       m.MessageType,
			Ciphertext:        m.Ciphertext,
			CreatedAt:         m.CreatedAt,
			ExpiresAt:         m.ExpiresAt,
		})
	}
	return c.JSON(http.StatusOK, encryptedMessageListResponse{Envelopes: views, Limit: limit, Offset: offset})
}
