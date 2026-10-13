package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// New-device sign-in notices (PeerTube 7 parity) and the "recognised devices"
// list behind them.
//
// A device is identified by a long-lived random cookie (vidra_device) bound,
// inside a SHA-256, to the browser family and OS parsed from the User-Agent: a
// copied cookie replayed from a different browser or OS reads as a NEW device.
// Only the hash, a coarse "Chrome on macOS" label and timestamps are stored —
// never the cookie, the raw User-Agent or an IP address (core has no GeoIP).
//
// Policy:
//   - an account with NO known device records silently: a brand-new account, or
//     the first sign-in after this shipped, must not mail everyone;
//   - otherwise an unrecognised device is mailed ONCE (the sign-in records it,
//     so the next one is known), unless the account opted out;
//   - mail to one account is rate-limited to one per newDeviceMailCooldown, so
//     a credential-stuffer cycling cookies cannot turn this into an inbox flood;
//   - best-effort: nothing here can fail or slow a sign-in beyond the send.
//
// Known limits: a client that keeps no cookies (a raw API script) is a new
// device each time, bounded by the cooldown and the opt-out; "forget" removes
// the record (the next sign-in from it mails again) but does NOT sign the
// device out — sessions carry no device link, so "sign out everywhere" remains
// the session control.
const (
	newDeviceMailCooldown = time.Hour
	maxKnownDevices       = 50
	knownDeviceMaxAge     = 180 * 24 * time.Hour
)

// DeviceStore is the persistence the device feature needs. *sqlcgen.Queries
// satisfies it; nil (the default) disables recognition and mail.
type DeviceStore interface {
	CountUserKnownDevices(ctx context.Context, userID uuid.UUID) (int64, error)
	TouchKnownDevice(ctx context.Context, arg sqlcgen.TouchKnownDeviceParams) (bool, error)
	HasKnownDevice(ctx context.Context, arg sqlcgen.HasKnownDeviceParams) (bool, error)
	PruneKnownDevices(ctx context.Context, arg sqlcgen.PruneKnownDevicesParams) (int64, error)
	ListKnownDevices(ctx context.Context, userID uuid.UUID) ([]sqlcgen.ListKnownDevicesRow, error)
	DeleteKnownDevice(ctx context.Context, arg sqlcgen.DeleteKnownDeviceParams) (int64, error)
	GetSecurityPrefs(ctx context.Context, userID uuid.UUID) (bool, error)
	SetNewDeviceEmailPref(ctx context.Context, arg sqlcgen.SetNewDeviceEmailPrefParams) error
	ClaimNewDeviceEmailSlot(ctx context.Context, arg sqlcgen.ClaimNewDeviceEmailSlotParams) (bool, error)
}

// NewDeviceMailer is the optional mailer capability; the Composer has it, the
// no-op and test mailers need not.
type NewDeviceMailer interface {
	SendNewDeviceSignIn(ctx context.Context, email string, n mail.NewDeviceSignIn) error
}

// WithDeviceStore enables new-device recognition and notices.
func WithDeviceStore(st DeviceStore) Option {
	return func(s *Service) { s.deviceStore = st }
}

// WithAsyncRunner replaces how background sends run (default: a goroutine).
// Tests pass an inline runner to stay deterministic.
func WithAsyncRunner(run func(func())) Option {
	return func(s *Service) { s.async = run }
}

// ErrDeviceNotFound: no such device for this account.
var ErrDeviceNotFound = errors.New("auth: device not found")

// uaLabel reduces a User-Agent to ("Chrome", "macOS"). Deliberately coarse and
// order-sensitive (Edge and Chrome both say Chrome; Chrome says Safari).
func uaLabel(ua string) (browser, os string) {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "edg/") || strings.Contains(l, "edge/"):
		browser = "Edge"
	case strings.Contains(l, "opr/") || strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "firefox/") || strings.Contains(l, "fxios"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "crios"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	default:
		browser = "Unknown browser"
	}
	switch {
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad"):
		os = "iOS"
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "mac os x") || strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "cros"):
		os = "ChromeOS"
	case strings.Contains(l, "linux") || strings.Contains(l, "x11"):
		os = "Linux"
	default:
		os = "Unknown OS"
	}
	return browser, os
}

func deviceHash(token, browser, os string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + browser + "\x00" + os))
	return hex.EncodeToString(sum[:])
}

func newDeviceToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type deviceTokenKey struct{}

// WithDeviceToken carries the request's device token (the vidra_device cookie,
// or one the HTTP layer just minted for it) to the sign-in paths. Without it a
// sign-in is simply not device-tracked (provider sign-ins, tests).
func WithDeviceToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, deviceTokenKey{}, token)
}

// NewDeviceToken mints the long-lived random device token.
func NewDeviceToken() (string, error) { return newDeviceToken() }

// noteSignIn records the device a completed sign-in came from and mails the
// account when it is new. Never fails a sign-in.
func (s *Service) noteSignIn(ctx context.Context, user sqlcgen.User, userAgent string) {
	deviceToken, _ := ctx.Value(deviceTokenKey{}).(string)
	if s.deviceStore == nil || deviceToken == "" {
		return
	}
	browser, os := uaLabel(userAgent)
	known, err := s.deviceStore.CountUserKnownDevices(ctx, user.ID)
	if err != nil {
		return
	}
	isNew, err := s.deviceStore.TouchKnownDevice(ctx, sqlcgen.TouchKnownDeviceParams{
		UserID: user.ID, DeviceHash: deviceHash(deviceToken, browser, os), Label: browser + " on " + os,
	})
	if err != nil {
		return
	}
	if isNew {
		// Bounded list: a cookieless client adds a row per sign-in.
		_, _ = s.deviceStore.PruneKnownDevices(ctx, sqlcgen.PruneKnownDevicesParams{
			UserID: user.ID, Cutoff: s.now().Add(-knownDeviceMaxAge), Keep: maxKnownDevices,
		})
	}
	if !isNew || known == 0 {
		return
	}
	if on, err := s.deviceStore.GetSecurityPrefs(ctx, user.ID); err == nil && !on {
		return
	}
	nm, ok := s.mailer.(NewDeviceMailer)
	if !ok || user.Email == "" {
		return
	}
	if won, err := s.deviceStore.ClaimNewDeviceEmailSlot(ctx, sqlcgen.ClaimNewDeviceEmailSlotParams{
		UserID: user.ID, Now: s.now(), CooldownSeconds: newDeviceMailCooldown.Seconds(),
	}); err != nil || !won {
		return
	}
	n := mail.NewDeviceSignIn{When: s.now().UTC(), Browser: browser, OS: os}
	email := user.Email
	// Off the request path: an SMTP relay's latency must not delay a sign-in.
	// Best-effort and not durable (a crash drops the notice); the slot is already
	// claimed, so it is never sent twice.
	s.runAsync(func() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = nm.SendNewDeviceSignIn(sendCtx, email, n)
	})
	return
}

// DeviceView is one recognised device as the account owner sees it. Current is
// true for the device making the request.
type DeviceView struct {
	ID        uuid.UUID
	Label     string
	FirstSeen time.Time
	LastSeen  time.Time
	Current   bool
}

// KnownDevices lists the account's recognised devices, most recent first,
// marking the one the request came from (token + userAgent as for sign-in).
func (s *Service) KnownDevices(ctx context.Context, userID uuid.UUID, token, userAgent string) ([]DeviceView, error) {
	if s.deviceStore == nil {
		return nil, nil
	}
	rows, err := s.deviceStore.ListKnownDevices(ctx, userID)
	if err != nil {
		return nil, err
	}
	browser, os := uaLabel(userAgent)
	cur := ""
	if token != "" {
		cur = deviceHash(token, browser, os)
	}
	out := make([]DeviceView, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeviceView{ID: r.ID, Label: r.Label, FirstSeen: r.FirstSeenAt, LastSeen: r.LastSeenAt,
			Current: cur != "" && r.DeviceHash == cur})
	}
	return out, nil
}

// ForgetDevice removes one recognised device (ErrDeviceNotFound when it is not
// this account's).
func (s *Service) ForgetDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	if s.deviceStore == nil {
		return ErrDeviceNotFound
	}
	n, err := s.deviceStore.DeleteKnownDevice(ctx, sqlcgen.DeleteKnownDeviceParams{UserID: userID, ID: deviceID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// NewDeviceEmailEnabled reports the opt-out state (default on).
func (s *Service) NewDeviceEmailEnabled(ctx context.Context, userID uuid.UUID) bool {
	if s.deviceStore == nil {
		return false
	}
	on, err := s.deviceStore.GetSecurityPrefs(ctx, userID)
	if err != nil { // no row (pgx.ErrNoRows) is the default: on
		return true
	}
	return on
}

// SetNewDeviceEmailEnabled records the opt-in/out.
func (s *Service) SetNewDeviceEmailEnabled(ctx context.Context, userID uuid.UUID, on bool) error {
	if s.deviceStore == nil {
		return nil
	}
	return s.deviceStore.SetNewDeviceEmailPref(ctx, sqlcgen.SetNewDeviceEmailPrefParams{UserID: userID, NewDeviceEmail: on})
}

// runAsync runs fn off the request path; tests set s.async to run inline.
func (s *Service) runAsync(fn func()) {
	if s.async != nil {
		s.async(fn)
		return
	}
	go fn()
}

// knownDeviceFromContext reports whether the request carries a device token
// that is a recognised device of the account (same UA binding as sign-in).
func (s *Service) knownDeviceFromContext(ctx context.Context, userID uuid.UUID, userAgent string) bool {
	tok, _ := ctx.Value(deviceTokenKey{}).(string)
	if s.deviceStore == nil || tok == "" {
		return false
	}
	b, o := uaLabel(userAgent)
	ok, err := s.deviceStore.HasKnownDevice(ctx, sqlcgen.HasKnownDeviceParams{UserID: userID, DeviceHash: deviceHash(tok, b, o)})
	return err == nil && ok
}
