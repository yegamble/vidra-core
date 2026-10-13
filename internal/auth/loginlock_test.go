package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeLockStore mirrors the SQL semantics of login_throttles.
type fakeLockStore struct {
	rows map[string]*fakeLockRow
}
type fakeLockRow struct {
	count  int32
	start  time.Time
	locked time.Time
}

func newFakeLockStore() *fakeLockStore         { return &fakeLockStore{rows: map[string]*fakeLockRow{}} }
func lockKey(u uuid.UUID, scope string) string { return u.String() + "|" + scope }

func (f *fakeLockStore) RecordLoginFailure(_ context.Context, a sqlcgen.RecordLoginFailureParams) (int32, error) {
	r := f.rows[lockKey(a.UserID, a.Scope)]
	win := time.Duration(a.WindowSeconds * float64(time.Second))
	switch {
	case r == nil:
		r = &fakeLockRow{count: 1, start: a.Now}
		f.rows[lockKey(a.UserID, a.Scope)] = r
	case !r.start.After(a.Now.Add(-win)):
		r.count, r.start = 1, a.Now
	default:
		r.count++
	}
	return r.count, nil
}
func (f *fakeLockStore) SetLoginLock(_ context.Context, a sqlcgen.SetLoginLockParams) error {
	if r := f.rows[lockKey(a.UserID, a.Scope)]; r != nil {
		r.locked = a.LockedUntil.Time
		if a.ResetCount {
			r.count = 0
		}
	}
	return nil
}
func (f *fakeLockStore) CountActiveLoginLocks(_ context.Context, a sqlcgen.CountActiveLoginLocksParams) (int64, error) {
	var n int64
	for _, sc := range a.Scopes {
		if r := f.rows[lockKey(a.UserID, sc)]; r != nil && r.locked.After(a.Now) {
			n++
		}
	}
	return n, nil
}
func (f *fakeLockStore) ClearLoginThrottleScope(_ context.Context, a sqlcgen.ClearLoginThrottleScopeParams) error {
	delete(f.rows, lockKey(a.UserID, a.Scope))
	return nil
}
func (f *fakeLockStore) ClearLoginThrottles(_ context.Context, id uuid.UUID) (int64, error) {
	var n int64
	for k := range f.rows {
		if len(k) > 36 && k[:36] == id.String() {
			delete(f.rows, k)
			n++
		}
	}
	return n, nil
}

type lockEnv struct {
	svc   *Service
	store *fakeLockStore
	now   time.Time
	user  sqlcgen.User
}

func newLockEnv(t *testing.T) *lockEnv {
	t.Helper()
	env := &lockEnv{store: newFakeLockStore(), now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	env.svc = NewService(newFakeRepo(), newTestIssuer(), time.Hour, WithLoginLockStore(env.store))
	env.svc.now = func() time.Time { return env.now }
	env.user, _ = register(t, env.svc, "alice", "alice@example.com")
	return env
}

func (e *lockEnv) login(ip, pw string) error {
	_, err := e.svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: pw, ClientIP: ip}, "ua")
	return err
}

func TestLoginLockThresholdAndExpiry(t *testing.T) {
	e := newLockEnv(t)
	for i := 0; i < prefixThreshold; i++ {
		if err := e.login("203.0.113.7", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Locked: even the RIGHT password is refused with the same error.
	if err := e.login("203.0.113.7", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked prefix accepted the right password: %v", err)
	}
	// Same /24, different host: still locked. Different network: unaffected.
	if err := e.login("203.0.113.200", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("same /24 not locked: %v", err)
	}
	if err := e.login("198.51.100.9", "supersecret"); err != nil {
		t.Fatalf("other network must not be locked out: %v", err)
	}
	// Expiry: the first lock is prefixLockBase.
	e.now = e.now.Add(prefixLockBase + time.Second)
	if err := e.login("203.0.113.7", "supersecret"); err != nil {
		t.Fatalf("lock must expire: %v", err)
	}
}

func TestLoginLockBacksOffExponentially(t *testing.T) {
	e := newLockEnv(t)
	for i := 0; i < prefixThreshold; i++ {
		_ = e.login("203.0.113.7", "wrong")
	}
	e.now = e.now.Add(prefixLockBase + time.Second)
	_ = e.login("203.0.113.7", "wrong") // 6th failure: lock doubles to 60s
	e.now = e.now.Add(prefixLockBase + time.Second)
	if err := e.login("203.0.113.7", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second lock should outlast 31s: %v", err)
	}
	e.now = e.now.Add(prefixLockBase)
	if err := e.login("203.0.113.7", "supersecret"); err != nil {
		t.Fatalf("second lock should end by 62s: %v", err)
	}
}

func TestLoginLockWindowResets(t *testing.T) {
	e := newLockEnv(t)
	for i := 0; i < prefixThreshold-1; i++ {
		_ = e.login("203.0.113.7", "wrong")
	}
	e.now = e.now.Add(lockWindow + time.Second)
	_ = e.login("203.0.113.7", "wrong") // count restarts at 1
	if err := e.login("203.0.113.7", "supersecret"); err != nil {
		t.Fatalf("stale failures must not carry over: %v", err)
	}
}

func TestLoginGlobalCapIsSoftAndSelfResetting(t *testing.T) {
	e := newLockEnv(t)
	// Many distinct networks, each under the per-prefix threshold.
	for i := 0; i < globalThreshold; i++ {
		_ = e.login("10."+itoa(i/200)+"."+itoa(i%200)+".1", "wrong")
	}
	if err := e.login("192.0.2.1", "supersecret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("global cap should refuse: %v", err)
	}
	e.now = e.now.Add(globalLockFor + time.Second)
	if err := e.login("192.0.2.1", "supersecret"); err != nil {
		t.Fatalf("global lock is short and must expire: %v", err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestLockedAndUnknownAreIndistinguishable(t *testing.T) {
	e := newLockEnv(t)
	for i := 0; i < prefixThreshold; i++ {
		_ = e.login("203.0.113.7", "wrong")
	}
	locked := e.login("203.0.113.7", "supersecret")
	_, unknown := e.svc.Login(context.Background(), LoginInput{Identifier: "nobody", Password: "supersecret", ClientIP: "203.0.113.7"}, "ua")
	wrong := e.login("198.51.100.9", "wrong")
	if locked != ErrInvalidCredentials || unknown != ErrInvalidCredentials || wrong != ErrInvalidCredentials {
		t.Fatalf("locked=%v unknown=%v wrong=%v: all must be the same error value", locked, unknown, wrong)
	}
	// Unknown identifiers leave no counters behind.
	for k := range e.store.rows {
		if k[:36] != e.user.ID.String() {
			t.Fatalf("a counter exists for a non-account: %s", k)
		}
	}
}

func TestLockedPathSpendsBcrypt(t *testing.T) {
	// The dummy compare must be a real, non-instant bcrypt comparison.
	dummyPasswordCompare("warm")
	start := time.Now()
	dummyPasswordCompare("x")
	if time.Since(start) < 500*time.Microsecond {
		t.Fatal("dummy compare returned instantly: unknown/locked accounts would be a timing oracle")
	}
}

func TestClearLoginLockout(t *testing.T) {
	e := newLockEnv(t)
	for i := 0; i < prefixThreshold; i++ {
		_ = e.login("203.0.113.7", "wrong")
	}
	cleared, err := e.svc.ClearLoginLockout(context.Background(), e.user.ID)
	if err != nil || !cleared {
		t.Fatalf("clear = %v, %v", cleared, err)
	}
	if err := e.login("203.0.113.7", "supersecret"); err != nil {
		t.Fatalf("login after admin clear: %v", err)
	}
	if again, _ := e.svc.ClearLoginLockout(context.Background(), e.user.ID); again {
		t.Fatal("second clear must report nothing to clear")
	}
	if _, err := e.svc.ClearLoginLockout(context.Background(), uuid.New()); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestIPScopeIsCoarse(t *testing.T) {
	cases := map[string]string{
		"203.0.113.77":         "ip:203.0.113.0/24",
		"203.0.113.77:5555":    "ip:203.0.113.0/24",
		"::ffff:203.0.113.77":  "ip:203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6": "ip:2001:db8:1::/48",
		"":                     "ip:unknown",
		"not an ip":            "ip:unknown",
	}
	for in, want := range cases {
		if got := ipScope(in); got != want {
			t.Errorf("ipScope(%q) = %q, want %q", in, got, want)
		}
	}
}
