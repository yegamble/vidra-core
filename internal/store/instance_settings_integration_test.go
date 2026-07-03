//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestInstanceSettingsPersistAndReload exercises the instance_settings overlay
// against real PostgreSQL: the sqlc upsert/list/delete round trip, the settings
// service's boot Load + write-then-reload cache behaviour, and the
// updated_by ON DELETE SET NULL semantics (the row survives its admin's
// deletion).
func TestInstanceSettingsPersistAndReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	// Seed an admin user (the setting's updated_by).
	var adminID uuid.UUID
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		"iscfg-"+suffix, "iscfg-"+suffix+"@example.test",
	).Scan(&adminID); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, adminID) }()
	// Clean up any settings rows this test writes (they have no user FK cascade
	// once updated_by is nulled).
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM instance_settings WHERE key IN ($1, $2)`,
			instancesettings.KeyInstanceName, instancesettings.KeyUploadsEnabled)
	}()

	defaults := instancesettings.Defaults{
		InstanceName:        "Config Default",
		RegistrationEnabled: true,
		UploadsEnabled:      true,
		CommentsEnabled:     true,
	}
	svc := instancesettings.NewService(q, defaults)
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := svc.String(instancesettings.KeyInstanceName); got != "Config Default" {
		t.Errorf("boot instance_name = %q, want Config Default", got)
	}
	if !svc.Bool(instancesettings.KeyUploadsEnabled) {
		t.Error("boot uploads_enabled = false, want true (config default)")
	}

	// Write two overrides through the service (validate → persist → reload).
	if err := svc.Apply(ctx, map[string]instancesettings.Update{
		instancesettings.KeyInstanceName:   {Value: "Persisted Name"},
		instancesettings.KeyUploadsEnabled: {Value: "false"},
	}, adminID); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Raw sqlc list confirms the rows landed with updated_by set.
	rows, err := q.ListInstanceSettings(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := map[string]sqlcgen.InstanceSetting{}
	for _, r := range rows {
		found[r.Key] = r
	}
	if r, ok := found[instancesettings.KeyInstanceName]; !ok || r.Value != "Persisted Name" || !r.UpdatedBy.Valid {
		t.Errorf("instance_name row = %+v, want value=Persisted Name updated_by set", r)
	}

	// A fresh service instance (simulating a restart) loads the persisted override.
	fresh := instancesettings.NewService(q, defaults)
	if err := fresh.Load(ctx); err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	if got := fresh.String(instancesettings.KeyInstanceName); got != "Persisted Name" {
		t.Errorf("reloaded instance_name = %q, want Persisted Name", got)
	}
	if fresh.Bool(instancesettings.KeyUploadsEnabled) {
		t.Error("reloaded uploads_enabled = true, want false")
	}

	// updated_by is ON DELETE SET NULL: deleting the admin keeps the row but
	// nulls the stamp.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, adminID); err != nil {
		t.Fatalf("delete admin: %v", err)
	}
	rows, _ = q.ListInstanceSettings(ctx)
	var stillThere bool
	for _, r := range rows {
		if r.Key == instancesettings.KeyInstanceName {
			stillThere = true
			if r.UpdatedBy.Valid {
				t.Error("updated_by still set after admin deletion, want NULL")
			}
			if r.Value != "Persisted Name" {
				t.Errorf("value changed after admin deletion: %q", r.Value)
			}
		}
	}
	if !stillThere {
		t.Error("instance_name override vanished after admin deletion (want ON DELETE SET NULL, not cascade)")
	}

	// Delete an override → the key falls back to the config default on reload.
	if err := svc.Apply(ctx, map[string]instancesettings.Update{
		instancesettings.KeyInstanceName: {Delete: true},
	}, uuid.Nil); err != nil {
		t.Fatalf("delete override: %v", err)
	}
	if got := svc.String(instancesettings.KeyInstanceName); got != "Config Default" {
		t.Errorf("after delete instance_name = %q, want Config Default", got)
	}
}
