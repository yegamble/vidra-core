package playersettings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository: one row per user. A miss returns
// pgx.ErrNoRows, mirroring the real :one query.
type fakeRepo struct {
	rows       map[uuid.UUID]sqlcgen.GetUserPlayerSettingsRow
	upsertErr  error
	getErr     error
	upsertSeen int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]sqlcgen.GetUserPlayerSettingsRow{}}
}

func (f *fakeRepo) GetUserPlayerSettings(_ context.Context, userID uuid.UUID) (sqlcgen.GetUserPlayerSettingsRow, error) {
	if f.getErr != nil {
		return sqlcgen.GetUserPlayerSettingsRow{}, f.getErr
	}
	row, ok := f.rows[userID]
	if !ok {
		return sqlcgen.GetUserPlayerSettingsRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeRepo) UpsertUserPlayerSettings(_ context.Context, a sqlcgen.UpsertUserPlayerSettingsParams) error {
	f.upsertSeen++
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.rows[a.UserID] = sqlcgen.GetUserPlayerSettingsRow{
		UserID:          a.UserID,
		AutoplayNext:    a.AutoplayNext,
		DefaultSpeed:    a.DefaultSpeed,
		DefaultQuality:  a.DefaultQuality,
		CaptionsDefault: a.CaptionsDefault,
		TheaterDefault:  a.TheaterDefault,
	}
	return nil
}

func ptrBool(b bool) *bool        { return &b }
func ptrFloat(f float64) *float64 { return &f }
func ptrStr(s string) *string     { return &s }

// TestGetFreshUserReturnsDefaults: a user with no stored row gets Defaults, not
// an error.
func TestGetFreshUserReturnsDefaults(t *testing.T) {
	svc := NewService(newFakeRepo())
	got, err := svc.Get(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != Defaults() {
		t.Fatalf("Get = %+v, want defaults %+v", got, Defaults())
	}
}

// TestUpdateMergeKeepsOmittedFields: a partial patch changes only the supplied
// fields; omitted fields keep the stored value across successive updates.
func TestUpdateMergeKeepsOmittedFields(t *testing.T) {
	svc := NewService(newFakeRepo())
	uid := uuid.New()
	ctx := context.Background()

	// From defaults, set speed + captions.
	got, err := svc.Update(ctx, uid, Patch{DefaultSpeed: ptrFloat(1.5), CaptionsDefault: ptrBool(true)})
	if err != nil {
		t.Fatalf("update 1: %v", err)
	}
	want := Settings{AutoplayNext: true, DefaultSpeed: 1.5, DefaultQuality: "auto", CaptionsDefault: true, TheaterDefault: false}
	if got != want {
		t.Fatalf("after update 1 = %+v, want %+v", got, want)
	}

	// Change quality only; speed + captions must survive.
	got, err = svc.Update(ctx, uid, Patch{DefaultQuality: ptrStr("1080p")})
	if err != nil {
		t.Fatalf("update 2: %v", err)
	}
	want.DefaultQuality = "1080p"
	if got != want {
		t.Fatalf("after update 2 = %+v, want %+v (speed/captions must persist)", got, want)
	}

	// Flip a boolean to false explicitly (a non-nil false must apply, not be
	// mistaken for "omitted").
	got, err = svc.Update(ctx, uid, Patch{AutoplayNext: ptrBool(false)})
	if err != nil {
		t.Fatalf("update 3: %v", err)
	}
	if got.AutoplayNext {
		t.Fatalf("autoplay_next still true after explicit false: %+v", got)
	}
}

// TestUpdateValidation: the speed ladder and quality shape are enforced, and a
// rejected update writes nothing.
func TestUpdateValidation(t *testing.T) {
	valid := []float64{0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3, 3.5, 4}
	for _, v := range valid {
		if !validSpeed(v) {
			t.Errorf("validSpeed(%v) = false, want true", v)
		}
	}
	for _, v := range []float64{0, 0.1, 1.1, 2.25, 4.5, 5, -1} {
		if validSpeed(v) {
			t.Errorf("validSpeed(%v) = true, want false", v)
		}
	}
	for _, q := range []string{"auto", "144p", "720p", "1080p", "2160p"} {
		if !validQuality(q) {
			t.Errorf("validQuality(%q) = false, want true", q)
		}
	}
	for _, q := range []string{"", "720", "p", "7p", "12345p", "ultra", "AUTO", "720P", "720px"} {
		if validQuality(q) {
			t.Errorf("validQuality(%q) = true, want false", q)
		}
	}

	// A rejected Update returns *ValidationError and never calls upsert.
	repo := newFakeRepo()
	svc := NewService(repo)
	_, err := svc.Update(context.Background(), uuid.New(), Patch{DefaultSpeed: ptrFloat(1.1)})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Update bad speed err = %v, want *ValidationError", err)
	}
	if repo.upsertSeen != 0 {
		t.Fatalf("upsert ran %d times on a rejected update, want 0", repo.upsertSeen)
	}

	_, err = svc.Update(context.Background(), uuid.New(), Patch{DefaultQuality: ptrStr("720")})
	if !errors.As(err, &ve) {
		t.Fatalf("Update bad quality err = %v, want *ValidationError", err)
	}
}

// TestPerUserIsolation: one user's update does not affect another's effective
// settings.
func TestPerUserIsolation(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()

	if _, err := svc.Update(ctx, ada, Patch{DefaultSpeed: ptrFloat(2), TheaterDefault: ptrBool(true)}); err != nil {
		t.Fatalf("ada update: %v", err)
	}
	bobGot, err := svc.Get(ctx, bob)
	if err != nil {
		t.Fatalf("bob get: %v", err)
	}
	if bobGot != Defaults() {
		t.Fatalf("bob's settings = %+v, want defaults (ada's update leaked)", bobGot)
	}
}

// TestGetPropagatesRealErrors: a non-ErrNoRows read error is surfaced (defaults
// are only for the "no row" case).
func TestGetPropagatesRealErrors(t *testing.T) {
	repo := newFakeRepo()
	repo.getErr = errors.New("boom")
	svc := NewService(repo)
	if _, err := svc.Get(context.Background(), uuid.New()); err == nil {
		t.Fatal("Get swallowed a real read error")
	}
}
