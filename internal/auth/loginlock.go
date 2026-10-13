package auth

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Failed-login backoff (PeerTube parity: per-account lockout).
//
// The strict auth limiter is per IP, so a botnet — or one patient attacker —
// gets a fresh budget per address against one account. This adds a brake keyed
// on the ACCOUNT, built so the brake itself is not a weapon:
//
//   - Primary: per (account, coarse IP prefix). N failures from one /24 (IPv6
//     /48) inside the window lock sign-ins for THAT account from THAT prefix,
//     with exponential backoff. Someone elsewhere cannot lock the owner out
//     of their own network by guessing, and the owner on a different network is
//     unaffected.
//   - Secondary, softer: an account-wide cap across every prefix, far higher
//     and with a short FIXED lock whose counter resets as it trips. It exists
//     to stop a distributed guesser, and costs a distributed attacker at least
//     globalThreshold failures per lock to keep the account unavailable for
//     globalLockFor — a nuisance bound, not an outage lever.
//
// Indistinguishability: a locked account answers exactly like a wrong password
// or an unknown account (ErrInvalidCredentials, same status, same body, no
// Retry-After), and the locked path spends a bcrypt comparison like the other
// two. The password is NOT checked while locked, so a locked account with the
// right password is also refused — that is the lock. Counters exist only for
// real accounts, so no row/err can differentiate an unknown identifier.
//
// Residual trade-offs: an attacker sharing the victim's /24 (campus, CGNAT)
// can lock the victim out from that prefix for up to lockMax; the account
// cannot be locked from other prefixes except by the soft cap. Admins can
// clear everything (DELETE /admin/users/{id}/login-lockout).
const (
	lockWindow      = 15 * time.Minute
	prefixThreshold = 5
	prefixLockBase  = 30 * time.Second
	prefixLockMax   = 15 * time.Minute
	globalThreshold = 30
	globalLockFor   = 2 * time.Minute

	scopeAll = "all"
)

// LoginLockStore is the persistence the backoff needs. *sqlcgen.Queries
// satisfies it; nil (the default) leaves login unthrottled per account.
type LoginLockStore interface {
	RecordLoginFailure(ctx context.Context, arg sqlcgen.RecordLoginFailureParams) (int32, error)
	SetLoginLock(ctx context.Context, arg sqlcgen.SetLoginLockParams) error
	CountActiveLoginLocks(ctx context.Context, arg sqlcgen.CountActiveLoginLocksParams) (int64, error)
	ClearLoginThrottleScope(ctx context.Context, arg sqlcgen.ClearLoginThrottleScopeParams) error
	ClearLoginThrottles(ctx context.Context, userID uuid.UUID) (int64, error)
}

// WithLoginLockStore enables per-account failed-login backoff.
func WithLoginLockStore(st LoginLockStore) Option {
	return func(s *Service) { s.lockStore = st }
}

// ipScope reduces a client address to the coarse scope key: IPv4 /24, IPv6
// /48. The raw address is never stored.
func ipScope(raw string) string {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		if host, _, serr := net.SplitHostPort(raw); serr == nil {
			addr, err = netip.ParseAddr(host)
		}
	}
	if err != nil {
		return "ip:unknown"
	}
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	p, _ := addr.Prefix(bits)
	return "ip:" + p.String()
}

// loginLocked reports whether this (account, client) is inside a lock. A store
// error fails OPEN: a database blip must not become a login outage.
func (s *Service) loginLocked(ctx context.Context, userID uuid.UUID, scope string) bool {
	if s.lockStore == nil {
		return false
	}
	n, err := s.lockStore.CountActiveLoginLocks(ctx, sqlcgen.CountActiveLoginLocksParams{
		UserID: userID, Scopes: []string{scope, scopeAll}, Now: s.now(),
	})
	return err == nil && n > 0
}

// recordLoginFailure counts a wrong password and sets any lock it earns.
func (s *Service) recordLoginFailure(ctx context.Context, userID uuid.UUID, scope string) {
	if s.lockStore == nil {
		return
	}
	now := s.now()
	if n, err := s.lockStore.RecordLoginFailure(ctx, sqlcgen.RecordLoginFailureParams{
		UserID: userID, Scope: scope, Now: now, WindowSeconds: lockWindow.Seconds(),
	}); err == nil && n >= prefixThreshold {
		d := prefixLockBase
		for i := int32(prefixThreshold); i < n && d < prefixLockMax; i++ {
			d *= 2
		}
		if d > prefixLockMax {
			d = prefixLockMax
		}
		_ = s.lockStore.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{
			UserID: userID, Scope: scope, LockedUntil: pgtype.Timestamptz{Time: now.Add(d), Valid: true},
		})
	}
	if n, err := s.lockStore.RecordLoginFailure(ctx, sqlcgen.RecordLoginFailureParams{
		UserID: userID, Scope: scopeAll, Now: now, WindowSeconds: lockWindow.Seconds(),
	}); err == nil && n >= globalThreshold {
		_ = s.lockStore.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{
			UserID: userID, Scope: scopeAll, ResetCount: true,
			LockedUntil: pgtype.Timestamptz{Time: now.Add(globalLockFor), Valid: true},
		})
	}
}

// clearLoginFailures forgets this prefix's failures after a correct password.
func (s *Service) clearLoginFailures(ctx context.Context, userID uuid.UUID, scope string) {
	if s.lockStore != nil {
		_ = s.lockStore.ClearLoginThrottleScope(ctx, sqlcgen.ClearLoginThrottleScopeParams{UserID: userID, Scope: scope})
	}
}

// ClearLoginLockout is the administrator action: drop every failure counter and
// lock for the account. It reports whether there was anything to clear.
func (s *Service) ClearLoginLockout(ctx context.Context, userID uuid.UUID) (bool, error) {
	if s.lockStore == nil {
		return false, nil
	}
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		return false, ErrAccountNotFound
	}
	n, err := s.lockStore.ClearLoginThrottles(ctx, userID)
	return n > 0, err
}
