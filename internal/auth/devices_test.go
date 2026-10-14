package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/mail"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeDeviceStore struct {
	devs     map[string]sqlcgen.ListKnownDevicesRow // key user|hash
	owner    map[uuid.UUID]uuid.UUID
	optOut   map[uuid.UUID]bool
	lastMail map[uuid.UUID]time.Time
}

func newFakeDeviceStore() *fakeDeviceStore {
	return &fakeDeviceStore{devs: map[string]sqlcgen.ListKnownDevicesRow{}, owner: map[uuid.UUID]uuid.UUID{},
		optOut: map[uuid.UUID]bool{}, lastMail: map[uuid.UUID]time.Time{}}
}

func (f *fakeDeviceStore) CountUserKnownDevices(_ context.Context, u uuid.UUID) (int64, error) {
	var n int64
	for _, o := range f.owner {
		if o == u {
			n++
		}
	}
	return n, nil
}
func (f *fakeDeviceStore) TouchKnownDevice(_ context.Context, a sqlcgen.TouchKnownDeviceParams) (bool, error) {
	k := a.UserID.String() + "|" + a.DeviceHash
	if _, ok := f.devs[k]; ok {
		return false, nil
	}
	id := uuid.New()
	f.devs[k] = sqlcgen.ListKnownDevicesRow{ID: id, DeviceHash: a.DeviceHash, Label: a.Label}
	f.owner[id] = a.UserID
	return true, nil
}
func (f *fakeDeviceStore) HasKnownDevice(_ context.Context, a sqlcgen.HasKnownDeviceParams) (bool, error) {
	_, ok := f.devs[a.UserID.String()+"|"+a.DeviceHash]
	return ok, nil
}
func (f *fakeDeviceStore) PruneKnownDevices(_ context.Context, a sqlcgen.PruneKnownDevicesParams) (int64, error) {
	var mine []string
	for k, d := range f.devs {
		if f.owner[d.ID] == a.UserID {
			mine = append(mine, k)
		}
	}
	var n int64
	for len(mine) > int(a.Keep) { // oldest-by-insertion is not tracked; evict arbitrary extras
		k := mine[0]
		mine = mine[1:]
		delete(f.owner, f.devs[k].ID)
		delete(f.devs, k)
		n++
	}
	return n, nil
}
func (f *fakeDeviceStore) ListKnownDevices(_ context.Context, u uuid.UUID) ([]sqlcgen.ListKnownDevicesRow, error) {
	var out []sqlcgen.ListKnownDevicesRow
	for _, d := range f.devs {
		if f.owner[d.ID] == u {
			out = append(out, d)
		}
	}
	return out, nil
}
func (f *fakeDeviceStore) DeleteKnownDevice(_ context.Context, a sqlcgen.DeleteKnownDeviceParams) (int64, error) {
	for k, d := range f.devs {
		if d.ID == a.ID && f.owner[d.ID] == a.UserID {
			delete(f.devs, k)
			delete(f.owner, d.ID)
			return 1, nil
		}
	}
	return 0, nil
}
func (f *fakeDeviceStore) GetSecurityPrefs(_ context.Context, u uuid.UUID) (bool, error) {
	if v, ok := f.optOut[u]; ok {
		return !v, nil
	}
	return false, pgx.ErrNoRows
}
func (f *fakeDeviceStore) SetNewDeviceEmailPref(_ context.Context, a sqlcgen.SetNewDeviceEmailPrefParams) error {
	f.optOut[a.UserID] = !a.NewDeviceEmail
	return nil
}
func (f *fakeDeviceStore) ClaimNewDeviceEmailSlot(_ context.Context, a sqlcgen.ClaimNewDeviceEmailSlotParams) (bool, error) {
	if t, ok := f.lastMail[a.UserID]; ok && t.After(a.Now.Add(-time.Duration(a.CooldownSeconds*float64(time.Second)))) {
		return false, nil
	}
	f.lastMail[a.UserID] = a.Now
	return true, nil
}

type deviceMailer struct {
	noopMailer
	sent []mail.NewDeviceSignIn
}

func (m *deviceMailer) SendNewDeviceSignIn(_ context.Context, _ string, n mail.NewDeviceSignIn) error {
	m.sent = append(m.sent, n)
	return nil
}

const (
	uaChromeMac  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	uaFirefoxWin = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

type devEnv struct {
	svc    *Service
	store  *fakeDeviceStore
	mailer *deviceMailer
	now    time.Time
	user   sqlcgen.User
}

func newDevEnv(t *testing.T) *devEnv {
	t.Helper()
	e := &devEnv{store: newFakeDeviceStore(), mailer: &deviceMailer{}, now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	e.svc = NewService(newFakeRepo(), newTestIssuer(), time.Hour, WithDeviceStore(e.store), WithMailer(e.mailer), WithAsyncRunner(func(f func()) { f() }))
	e.svc.now = func() time.Time { return e.now }
	e.user, _ = register(t, e.svc, "alice", "alice@example.com")
	return e
}

func (e *devEnv) login(token, ua string) {
	ctx := WithDeviceToken(context.Background(), token)
	if _, err := e.svc.Login(ctx, LoginInput{Identifier: "alice", Password: "supersecret"}, ua); err != nil {
		panic(err)
	}
}

func TestFirstDeviceRecordsSilently(t *testing.T) {
	e := newDevEnv(t)
	e.login("tok-A", uaChromeMac)
	if len(e.mailer.sent) != 0 {
		t.Fatalf("first-ever device must not mail: %+v", e.mailer.sent)
	}
	if n, _ := e.store.CountUserKnownDevices(context.Background(), e.user.ID); n != 1 {
		t.Fatalf("devices = %d, want 1", n)
	}
}

func TestNewDeviceMailsOnceKnownDoesNot(t *testing.T) {
	e := newDevEnv(t)
	e.login("tok-A", uaChromeMac)
	e.login("tok-B", uaFirefoxWin) // new device
	if len(e.mailer.sent) != 1 {
		t.Fatalf("mails = %d, want 1", len(e.mailer.sent))
	}
	got := e.mailer.sent[0]
	if got.Browser != "Firefox" || got.OS != "Windows" {
		t.Fatalf("mail content = %+v", got)
	}
	e.login("tok-B", uaFirefoxWin) // same device again
	e.login("tok-A", uaChromeMac)
	if len(e.mailer.sent) != 1 {
		t.Fatalf("a known device re-mailed: %d", len(e.mailer.sent))
	}
}

func TestStolenCookieFromAnotherBrowserIsNew(t *testing.T) {
	e := newDevEnv(t)
	e.login("tok-A", uaChromeMac)
	e.login("tok-A", uaFirefoxWin) // same cookie, different browser/OS
	if len(e.mailer.sent) != 1 {
		t.Fatalf("cookie replayed from another UA must read as a new device; mails=%d", len(e.mailer.sent))
	}
}

func TestNewDeviceMailRateLimitedAndOptOut(t *testing.T) {
	e := newDevEnv(t)
	e.login("tok-A", uaChromeMac)
	e.login("tok-B", uaFirefoxWin) // mail 1
	e.login("tok-C", uaFirefoxWin) // new again, inside the cooldown: recorded, not mailed
	if len(e.mailer.sent) != 1 {
		t.Fatalf("mails = %d, want 1 inside the cooldown", len(e.mailer.sent))
	}
	e.now = e.now.Add(newDeviceMailCooldown + time.Minute)
	e.login("tok-D", uaFirefoxWin)
	if len(e.mailer.sent) != 2 {
		t.Fatalf("mails after cooldown = %d, want 2", len(e.mailer.sent))
	}
	if err := e.svc.SetNewDeviceEmailEnabled(context.Background(), e.user.ID, false); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Hour)
	e.login("tok-E", uaFirefoxWin)
	if len(e.mailer.sent) != 2 || e.svc.NewDeviceEmailEnabled(context.Background(), e.user.ID) {
		t.Fatalf("opted-out account was mailed (mails=%d)", len(e.mailer.sent))
	}
}

func TestListAndForgetDevice(t *testing.T) {
	e := newDevEnv(t)
	e.login("tok-A", uaChromeMac)
	e.login("tok-B", uaFirefoxWin)
	list, err := e.svc.KnownDevices(context.Background(), e.user.ID, "tok-B", uaFirefoxWin)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	var cur, other DeviceView
	for _, d := range list {
		if d.Current {
			cur = d
		} else {
			other = d
		}
	}
	if cur.Label != "Firefox on Windows" || other.Label != "Chrome on macOS" {
		t.Fatalf("labels/current wrong: %+v", list)
	}
	if err := e.svc.ForgetDevice(context.Background(), uuid.New(), other.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("another account's forget must be not-found: %v", err)
	}
	if err := e.svc.ForgetDevice(context.Background(), e.user.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Hour)
	e.login("tok-A", uaChromeMac) // forgotten -> new again -> mails
	if len(e.mailer.sent) != 2 {
		t.Fatalf("mails = %d, want 2 (first new device + forgotten one)", len(e.mailer.sent))
	}
}

func TestNoTokenInContextMeansNoTracking(t *testing.T) {
	e := newDevEnv(t)
	if _, err := e.svc.Login(context.Background(), LoginInput{Identifier: "alice", Password: "supersecret"}, uaChromeMac); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.CountUserKnownDevices(context.Background(), e.user.ID); n != 0 {
		t.Fatal("sign-in without a device token must not record")
	}
}

func TestUALabel(t *testing.T) {
	cases := map[string][2]string{
		uaChromeMac:  {"Chrome", "macOS"},
		uaFirefoxWin: {"Firefox", "Windows"},
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1": {"Safari", "iOS"},
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/130.0 Safari/537.36 Edg/130.0":                                                   {"Edge", "Windows"},
		"curl/8.0": {"Unknown browser", "Unknown OS"},
	}
	for ua, want := range cases {
		if b, o := uaLabel(ua); b != want[0] || o != want[1] {
			t.Errorf("uaLabel(%q) = %s/%s, want %v", ua, b, o, want)
		}
	}
}

func TestDeviceListIsCapped(t *testing.T) {
	e := newDevEnv(t)
	for i := 0; i < maxKnownDevices+20; i++ {
		e.login("tok-"+uuid.NewString(), uaChromeMac)
	}
	if n, _ := e.store.CountUserKnownDevices(context.Background(), e.user.ID); n > maxKnownDevices {
		t.Fatalf("devices = %d, want <= %d", n, maxKnownDevices)
	}
}
