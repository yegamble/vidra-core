package video

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Keep the password-hashing suites fast: bcrypt at production cost would dominate
// these tests (the httpapi package does the same from its own init).
func init() { auth.UseFastPasswordHashingForTests() }

// --- fakeRepo video-password + embed methods (mirror video_passwords.sql) ---

type fakePasswordRow struct {
	ID        uuid.UUID
	Hash      string
	CreatedAt time.Time
}

func (f *fakeRepo) ListVideoPasswords(_ context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoPasswordsRow, error) {
	out := make([]sqlcgen.ListVideoPasswordsRow, 0, len(f.passwords[videoID]))
	for _, p := range f.passwords[videoID] {
		out = append(out, sqlcgen.ListVideoPasswordsRow{ID: p.ID, CreatedAt: p.CreatedAt})
	}
	return out, nil
}

func (f *fakeRepo) ListVideoPasswordHashes(_ context.Context, videoID uuid.UUID) ([]string, error) {
	out := make([]string, 0, len(f.passwords[videoID]))
	for _, p := range f.passwords[videoID] {
		out = append(out, p.Hash)
	}
	return out, nil
}

func (f *fakeRepo) CountVideoPasswords(_ context.Context, videoID uuid.UUID) (int64, error) {
	return int64(len(f.passwords[videoID])), nil
}

func (f *fakeRepo) InsertVideoPassword(_ context.Context, a sqlcgen.InsertVideoPasswordParams) (sqlcgen.InsertVideoPasswordRow, error) {
	if f.passwords == nil {
		f.passwords = map[uuid.UUID][]fakePasswordRow{}
	}
	p := fakePasswordRow{ID: uuid.New(), Hash: a.PasswordHash, CreatedAt: time.Now()}
	f.passwords[a.VideoID] = append(f.passwords[a.VideoID], p)
	return sqlcgen.InsertVideoPasswordRow{ID: p.ID, CreatedAt: p.CreatedAt}, nil
}

func (f *fakeRepo) VideoPasswordExists(_ context.Context, a sqlcgen.VideoPasswordExistsParams) (bool, error) {
	for _, p := range f.passwords[a.VideoID] {
		if p.ID == a.ID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) DeleteVideoPassword(_ context.Context, a sqlcgen.DeleteVideoPasswordParams) (int64, error) {
	rows := f.passwords[a.VideoID]
	for i, p := range rows {
		if p.ID == a.ID {
			f.passwords[a.VideoID] = append(rows[:i], rows[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) DeleteVideoPasswords(_ context.Context, videoID uuid.UUID) error {
	delete(f.passwords, videoID)
	return nil
}

func (f *fakeRepo) InsertVideoPasswords(_ context.Context, a sqlcgen.InsertVideoPasswordsParams) error {
	if f.passwords == nil {
		f.passwords = map[uuid.UUID][]fakePasswordRow{}
	}
	for _, h := range a.PasswordHash {
		f.passwords[a.VideoID] = append(f.passwords[a.VideoID], fakePasswordRow{ID: uuid.New(), Hash: h, CreatedAt: time.Now()})
	}
	return nil
}

func (f *fakeRepo) GetVideoEmbedPrivacy(_ context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEmbedPrivacyRow, error) {
	if row, ok := f.embed[videoID]; ok {
		return row, nil
	}
	return sqlcgen.GetVideoEmbedPrivacyRow{EmbedPrivacy: "enabled", EmbedAllowedDomains: []string{}}, nil
}

func (f *fakeRepo) SetVideoEmbedPrivacy(_ context.Context, a sqlcgen.SetVideoEmbedPrivacyParams) error {
	if f.embed == nil {
		f.embed = map[uuid.UUID]sqlcgen.GetVideoEmbedPrivacyRow{}
	}
	f.embed[a.ID] = sqlcgen.GetVideoEmbedPrivacyRow{EmbedPrivacy: a.EmbedPrivacy, EmbedAllowedDomains: a.EmbedAllowedDomains}
	return nil
}

// --- helpers ---

// seedVideo creates a private draft owned by owner and returns its id.
func seedVideo(t *testing.T, svc *Service, owner uuid.UUID) uuid.UUID {
	t.Helper()
	v, err := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "v", Privacy: "private"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	// The fake tags every created video with f.owner as the owner.
	_ = owner
	return v.ID
}

// --- tests ---

// TestAddPasswordValidation proves the 6–100 character rule and owner gating.
func TestAddPasswordValidation(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := seedVideo(t, svc, owner)
	ctx := context.Background()

	// Too short and too long are 400-class *PasswordValidationError.
	for _, bad := range []string{"", "12345", strings.Repeat("x", MaxVideoPasswordLen+1)} {
		if _, err := svc.AddPassword(ctx, owner, id, bad); err == nil {
			t.Fatalf("AddPassword(%q) = nil, want validation error", bad)
		} else if _, ok := err.(*PasswordValidationError); !ok {
			t.Fatalf("AddPassword(%q) err = %T, want *PasswordValidationError", bad, err)
		}
	}
	// A valid password stores and returns id + created_at (never the plaintext).
	p, err := svc.AddPassword(ctx, owner, id, "hunter2")
	if err != nil {
		t.Fatalf("AddPassword valid: %v", err)
	}
	if p.ID == uuid.Nil || p.CreatedAt.IsZero() {
		t.Fatalf("AddPassword returned empty projection: %+v", p)
	}
	// A non-owner is ErrForbidden (rendered 404 by the HTTP layer).
	if _, err := svc.AddPassword(ctx, uuid.New(), id, "hunter2"); err != ErrForbidden {
		t.Fatalf("non-owner AddPassword err = %v, want ErrForbidden", err)
	}
}

// TestVerifyPassword proves the unlock verification: a stored password matches,
// a wrong one does not, and a video with no passwords verifies nothing.
func TestVerifyPassword(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := seedVideo(t, svc, owner)
	ctx := context.Background()
	if _, err := svc.AddPassword(ctx, owner, id, "correct horse"); err != nil {
		t.Fatalf("seed password: %v", err)
	}

	if ok, err := svc.VerifyPassword(ctx, id, "correct horse"); err != nil || !ok {
		t.Fatalf("VerifyPassword(correct) = %v,%v want true,nil", ok, err)
	}
	if ok, err := svc.VerifyPassword(ctx, id, "wrong"); err != nil || ok {
		t.Fatalf("VerifyPassword(wrong) = %v,%v want false,nil", ok, err)
	}
	if ok, err := svc.VerifyPassword(ctx, uuid.New(), "anything"); err != nil || ok {
		t.Fatalf("VerifyPassword(no passwords) = %v,%v want false,nil", ok, err)
	}
}

// TestReplacePasswords proves the 1–20 count rule and whole-set replace.
func TestReplacePasswords(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := seedVideo(t, svc, owner)
	ctx := context.Background()

	if _, err := svc.ReplacePasswords(ctx, owner, id, nil); err == nil {
		t.Fatalf("ReplacePasswords(empty) = nil, want validation error")
	}
	tooMany := make([]string, MaxVideoPasswords+1)
	for i := range tooMany {
		tooMany[i] = "password123"
	}
	if _, err := svc.ReplacePasswords(ctx, owner, id, tooMany); err == nil {
		t.Fatalf("ReplacePasswords(21) = nil, want validation error")
	}
	stored, err := svc.ReplacePasswords(ctx, owner, id, []string{"first-one", "second-one"})
	if err != nil {
		t.Fatalf("ReplacePasswords valid: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored = %d passwords, want 2", len(stored))
	}
	// Replace again fully replaces (old set gone): only the new one verifies.
	if _, err := svc.ReplacePasswords(ctx, owner, id, []string{"brand-new-pw"}); err != nil {
		t.Fatalf("ReplacePasswords second: %v", err)
	}
	if ok, _ := svc.VerifyPassword(ctx, id, "first-one"); ok {
		t.Fatalf("old password should no longer verify after replace")
	}
	if ok, _ := svc.VerifyPassword(ctx, id, "brand-new-pw"); !ok {
		t.Fatalf("new password should verify after replace")
	}
}

// TestDeletePasswordLastOnPasswordVideo proves an unknown id is ErrPasswordNotFound
// and that removing the last password of a privacy=password video is ErrLastPassword.
func TestDeletePasswordLastOnPasswordVideo(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	id := seedVideo(t, svc, owner)
	ctx := context.Background()

	p, err := svc.AddPassword(ctx, owner, id, "only-password")
	if err != nil {
		t.Fatalf("seed password: %v", err)
	}
	// Unknown password id → ErrPasswordNotFound.
	if err := svc.DeletePassword(ctx, owner, id, uuid.New()); err != ErrPasswordNotFound {
		t.Fatalf("delete unknown = %v, want ErrPasswordNotFound", err)
	}
	// While the video is NOT password-privacy, deleting the last one is allowed.
	if err := svc.DeletePassword(ctx, owner, id, p.ID); err != nil {
		t.Fatalf("delete last on non-password video = %v, want nil", err)
	}

	// Now make the video privacy=password with exactly one password and prove the
	// last-delete 409 guard.
	p2, _ := svc.AddPassword(ctx, owner, id, "another-password")
	row := repo.videos[id]
	row.Privacy = PrivacyPassword
	repo.videos[id] = row
	if err := svc.DeletePassword(ctx, owner, id, p2.ID); err != ErrLastPassword {
		t.Fatalf("delete last on password video = %v, want ErrLastPassword", err)
	}
}

// TestPrivacyPasswordRequiresPassword proves you cannot create a video directly as
// privacy=password, nor switch to it, without at least one password.
func TestPrivacyPasswordRequiresPassword(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	ctx := context.Background()

	if _, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "v", Privacy: PrivacyPassword}); err != ErrPasswordRequired {
		t.Fatalf("CreateDraft(password) = %v, want ErrPasswordRequired", err)
	}

	id := seedVideo(t, svc, owner)
	pw := PrivacyPassword
	if _, err := svc.Update(ctx, owner, id, UpdateInput{Privacy: &pw}); err != ErrPasswordRequired {
		t.Fatalf("Update->password with no passwords = %v, want ErrPasswordRequired", err)
	}
	// After adding a password the switch succeeds.
	if _, err := svc.AddPassword(ctx, owner, id, "unlock-me"); err != nil {
		t.Fatalf("AddPassword: %v", err)
	}
	if _, err := svc.Update(ctx, owner, id, UpdateInput{Privacy: &pw}); err != nil {
		t.Fatalf("Update->password with a password = %v, want nil", err)
	}
}
