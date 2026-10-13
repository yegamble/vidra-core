package auth

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/google/uuid"

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
//     to stop a distributed guesser. It NEVER applies to a prefix the account
//     recently signed in from successfully, so rotating prefixes cannot keep
//     the owner out of their own network.
//
// Attempts are counted BEFORE the password is compared (one atomic statement),
// so a parallel burst cannot all pass the threshold; locks only ever lengthen.
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
	EnsureLoginThrottle(ctx context.Context, arg sqlcgen.EnsureLoginThrottleParams) error
	RecordLoginAttempt(ctx context.Context, arg sqlcgen.RecordLoginAttemptParams) (sqlcgen.RecordLoginAttemptRow, error)
	SetLoginLock(ctx context.Context, arg sqlcgen.SetLoginLockParams) error
	CountActiveLoginLocks(ctx context.Context, arg sqlcgen.CountActiveLoginLocksParams) (int64, error)
	RecordLoginSuccess(ctx context.Context, arg sqlcgen.RecordLoginSuccessParams) error
	HasRecentLoginSuccess(ctx context.Context, arg sqlcgen.HasRecentLoginSuccessParams) (bool, error)
	ReleaseLoginAttempt(ctx context.Context, arg sqlcgen.ReleaseLoginAttemptParams) error
	ClearLoginThrottleScope(ctx context.Context, arg sqlcgen.ClearLoginThrottleScopeParams) error
	ClearLoginThrottles(ctx context.Context, userID uuid.UUID) (int64, error)
}

// WithLoginLockStore enables per-account failed-login backoff.
func WithLoginLockStore(st LoginLockStore) Option {
	return func(s *Service) { s.lockStore = st }
}

// knownPrefixFor is how long a successful sign-in keeps its prefix exempt from
// the account-wide cap.
const knownPrefixFor = 30 * 24 * time.Hour

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

// loginGate is the per-attempt verdict of the throttle.
type loginGate struct {
	refuse bool // do not compare the password; answer exactly like a wrong one
	scope  string
	ipN    int32 // this attempt's pre-counted number within the window
	allN   int32
	exempt bool
}

// gateLogin runs BEFORE the password compare. It refuses while a lock is
// active, otherwise counts this attempt atomically and refuses (setting the
// lock) when the count is already past a threshold. A store error fails OPEN.
func (s *Service) gateLogin(ctx context.Context, user sqlcgen.User, clientIP, userAgent string) loginGate {
	g := loginGate{scope: ipScope(clientIP)}
	if s.lockStore == nil {
		return g
	}
	now := s.now()
	exempt := s.globalCapExempt(ctx, user, g.scope, userAgent)
	g.exempt = exempt
	scopes := []string{g.scope}
	if !exempt {
		scopes = append(scopes, scopeAll)
	}
	if n, err := s.lockStore.CountActiveLoginLocks(ctx, sqlcgen.CountActiveLoginLocksParams{UserID: user.ID, Scopes: scopes, Now: now}); err == nil && n > 0 {
		g.refuse = true
		return g
	}
	_ = s.lockStore.EnsureLoginThrottle(ctx, sqlcgen.EnsureLoginThrottleParams{UserID: user.ID, Scope: g.scope, Now: now})
	_ = s.lockStore.EnsureLoginThrottle(ctx, sqlcgen.EnsureLoginThrottleParams{UserID: user.ID, Scope: scopeAll, Now: now})
	if r, err := s.lockStore.RecordLoginAttempt(ctx, sqlcgen.RecordLoginAttemptParams{
		UserID: user.ID, Scope: g.scope, Now: now, WindowSeconds: lockWindow.Seconds(),
	}); err == nil {
		g.ipN = r.FailCount
		if r.FailCount > prefixThreshold && !r.Probe {
			s.lockPrefix(ctx, user.ID, g.scope, r.FailCount)
			g.refuse = true
		}
	}
	if r, err := s.lockStore.RecordLoginAttempt(ctx, sqlcgen.RecordLoginAttemptParams{
		UserID: user.ID, Scope: scopeAll, Now: now, WindowSeconds: lockWindow.Seconds(),
	}); err == nil {
		g.allN = r.FailCount
		if r.FailCount > globalThreshold && !r.Probe && !exempt {
			s.lockAll(ctx, user.ID)
			g.refuse = true
		}
	}
	return g
}

func (s *Service) lockPrefix(ctx context.Context, userID uuid.UUID, scope string, n int32) {
	d := prefixLockBase
	for i := int32(prefixThreshold); i < n && d < prefixLockMax; i++ { // n=5 -> base
		d *= 2
	}
	if d > prefixLockMax {
		d = prefixLockMax
	}
	_ = s.lockStore.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{
		UserID: userID, Scope: scope, LockedUntil: s.now().Add(d),
	})
}

func (s *Service) lockAll(ctx context.Context, userID uuid.UUID) {
	_ = s.lockStore.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{
		UserID: userID, Scope: scopeAll, ResetCount: true, LockedUntil: s.now().Add(globalLockFor),
	})
}

// globalCapExempt: the account-wide cap never locks out a prefix this account
// recently signed in from.
func (s *Service) globalCapExempt(ctx context.Context, user sqlcgen.User, scope, userAgent string) bool {
	ok, err := s.lockStore.HasRecentLoginSuccess(ctx, sqlcgen.HasRecentLoginSuccessParams{
		UserID: user.ID, Scope: okScope(scope), Since: s.now().Add(-knownPrefixFor),
	})
	return err == nil && ok
}

func okScope(ipScope string) string { return "ok:" + ipScope }

// loginFailed sets any lock a wrong password earns (the attempt was counted
// by gateLogin already).
func (s *Service) loginFailed(ctx context.Context, user sqlcgen.User, g loginGate) {
	if s.lockStore == nil {
		return
	}
	if g.ipN >= prefixThreshold {
		s.lockPrefix(ctx, user.ID, g.scope, g.ipN)
	}
	if g.allN >= globalThreshold && !g.exempt {
		s.lockAll(ctx, user.ID)
	}
}

// loginSucceeded hands the attempt back and remembers the prefix.
func (s *Service) loginSucceeded(ctx context.Context, user sqlcgen.User, g loginGate) {
	if s.lockStore == nil {
		return
	}
	_ = s.lockStore.ClearLoginThrottleScope(ctx, sqlcgen.ClearLoginThrottleScopeParams{UserID: user.ID, Scope: g.scope})
	_ = s.lockStore.ReleaseLoginAttempt(ctx, sqlcgen.ReleaseLoginAttemptParams{UserID: user.ID, Scope: scopeAll})
	_ = s.lockStore.RecordLoginSuccess(ctx, sqlcgen.RecordLoginSuccessParams{UserID: user.ID, Scope: okScope(g.scope), Now: s.now()})
}

// ClearLoginLockout is the administrator action: drop every failure counter and
// lock for the account. It reports whether there was anything to clear.
func (s *Service) ClearLoginLockout(ctx context.Context, userID uuid.UUID) (bool, error) {
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		return false, ErrAccountNotFound
	}
	if s.lockStore == nil {
		return false, nil
	}
	n, err := s.lockStore.ClearLoginThrottles(ctx, userID)
	return n > 0, err
}
