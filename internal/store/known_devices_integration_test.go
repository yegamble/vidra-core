//go:build integration

// Integration test: the known-device and new-device-mail-slot SQL (migration
// 0175). The `xmax = 0` insert detector and the single-statement rate-limit
// claim are Postgres behaviours a fake would only restate.
package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestKnownDeviceQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	n := time.Now().UnixNano()
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: fmt.Sprintf("dev-%d", n), Email: fmt.Sprintf("dev-%d@example.test", n), PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })

	touch := func(h string) bool {
		isNew, err := q.TouchKnownDevice(ctx, sqlcgen.TouchKnownDeviceParams{UserID: user.ID, DeviceHash: h, Label: "Chrome on macOS"})
		if err != nil {
			t.Fatal(err)
		}
		return isNew
	}
	if !touch("h1") || touch("h1") || !touch("h2") {
		t.Fatal("TouchKnownDevice must report insert vs existing")
	}
	rows, err := q.ListKnownDevices(ctx, user.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list = %d, %v", len(rows), err)
	}
	if c, _ := q.CountUserKnownDevices(ctx, user.ID); c != 2 {
		t.Fatalf("count = %d", c)
	}
	if d, err := q.DeleteKnownDevice(ctx, sqlcgen.DeleteKnownDeviceParams{UserID: user.ID, ID: rows[0].ID}); err != nil || d != 1 {
		t.Fatalf("delete = %d, %v", d, err)
	}

	claim := func(at time.Time) bool {
		ok, err := q.ClaimNewDeviceEmailSlot(ctx, sqlcgen.ClaimNewDeviceEmailSlotParams{UserID: user.ID, Now: at, CooldownSeconds: 3600})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	now := time.Now()
	if !claim(now) || claim(now.Add(10*time.Minute)) || !claim(now.Add(61*time.Minute)) {
		t.Fatal("mail slot: first wins, inside cooldown loses, after cooldown wins")
	}
	if _, err := q.GetSecurityPrefs(ctx, user.ID); err != nil {
		t.Fatalf("claiming created the prefs row, default on: %v", err)
	}
	if err := q.SetNewDeviceEmailPref(ctx, sqlcgen.SetNewDeviceEmailPrefParams{UserID: user.ID, NewDeviceEmail: false}); err != nil {
		t.Fatal(err)
	}
	if on, _ := q.GetSecurityPrefs(ctx, user.ID); on {
		t.Fatal("opt-out not stored")
	}
}
