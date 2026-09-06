package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// regReqRow is an in-memory registration request for handler tests.
type regReqRow struct {
	id            uuid.UUID
	username      string
	email         string
	passwordHash  string
	note          string
	status        string
	moderatorNote string
	reviewedAt    pgtype.Timestamptz
	createdAt     time.Time
}

// authFakeRepo is a tiny in-memory auth.Repository for handler tests.
type authFakeRepo struct {
	users    map[string]sqlcgen.User // keyed by lowercased email
	sessions map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow
	resets   map[string]*sqlcgen.PasswordResetToken     // keyed by token hash
	verifs   map[string]*sqlcgen.EmailVerificationToken // keyed by token hash
	// emailChanges mirrors email_change_requests (0129), keyed by token hash.
	// See auth_email_change_fake_test.go for the methods over it.
	emailChanges map[string]*sqlcgen.EmailChangeRequest
	regReqs      []*regReqRow
	// usage mirrors SumUserStorageUsage: a user's total stored video-file bytes.
	// Nil (pure-auth harnesses) means 0; videoServerEnv wires it to sum the
	// video fake repo's files, mirroring the real aggregate query.
	usage func(uuid.UUID) int64
	// uploadUsage mirrors the upload_usage_events daily ledger (W7).
	uploadUsage []uploadUsageEvent
	// Instance-wide aggregate stubs let authFakeRepo satisfy admin.Repository's
	// overview reads (Stats). Nil means 0; videoServerFullWith wires the
	// video/comment ones to the real fakes so the admin-stats handler reflects
	// created state. CountUsers is real (len(users)); peers default to 0.
	statPublicVideos func() int64
	statAllStorage   func() int64
	statComments     func() int64
	statPeers        func() int64
	// ownerClaim mirrors the single-row owner_claim_tokens table (0104). Nil =
	// never minted, so most tests register freely.
	ownerClaim *sqlcgen.OwnerClaimToken
	// mutes/userBlocks mirror the per-viewer predicates the account-search query
	// applies. Nil (pure-auth harnesses) means nobody has muted or blocked
	// anyone; videoServerFullWith wires the shared fakes.
	mutes      *muteFakeRepo
	userBlocks *blockFakeRepo
}

// SearchPublicAccounts mirrors the real query's visibility gate EXACTLY —
// active AND profile_public AND NOT unlisted — because that gate is the
// property the tests exist to prove. Getting it wrong here would make a
// negative test pass for the wrong reason.
func (f *authFakeRepo) SearchPublicAccounts(_ context.Context, a sqlcgen.SearchPublicAccountsParams) ([]sqlcgen.SearchPublicAccountsRow, error) {
	q := strings.ToLower(a.Query)
	var out []sqlcgen.SearchPublicAccountsRow
	for _, u := range f.users {
		if !u.IsActive || !u.ProfilePublic || u.Unlisted {
			continue
		}
		if !strings.Contains(strings.ToLower(u.Username), q) && !strings.Contains(strings.ToLower(u.DisplayName), q) {
			continue
		}
		if a.ViewerID.Valid {
			viewer := uuid.UUID(a.ViewerID.Bytes)
			if f.mutes != nil && f.mutes.isMuted(viewer, u.ID) {
				continue
			}
			if f.userBlocks != nil && f.userBlocks.isBlocked(viewer, u.ID) {
				continue
			}
		}
		out = append(out, sqlcgen.SearchPublicAccountsRow{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
			Bio: u.Bio, CreatedAt: u.CreatedAt,
		})
	}
	// Stable order: the SQL ranks by trigram similarity, which an in-memory fake
	// cannot reproduce; newest-first matches its secondary key and keeps pages
	// deterministic.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	lo := min(int(a.ResultOffset), len(out))
	out = out[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(out) {
		out = out[:a.ResultLimit]
	}
	return out, nil
}

func (f *authFakeRepo) CountSearchPublicAccounts(ctx context.Context, a sqlcgen.CountSearchPublicAccountsParams) (int64, error) {
	rows, err := f.SearchPublicAccounts(ctx, sqlcgen.SearchPublicAccountsParams{
		Query: a.Query, ViewerID: a.ViewerID, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
}

func newAuthFakeRepo() *authFakeRepo {
	return &authFakeRepo{
		users:    map[string]sqlcgen.User{},
		sessions: map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow{},
		resets:   map[string]*sqlcgen.PasswordResetToken{},
		verifs:   map[string]*sqlcgen.EmailVerificationToken{},
	}
}

func (f *authFakeRepo) CreateEmailVerificationToken(_ context.Context, a sqlcgen.CreateEmailVerificationTokenParams) (sqlcgen.EmailVerificationToken, error) {
	t := sqlcgen.EmailVerificationToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.verifs[a.TokenHash] = &t
	return t, nil
}

func (f *authFakeRepo) GetEmailVerificationToken(_ context.Context, hash string) (sqlcgen.EmailVerificationToken, error) {
	if t, ok := f.verifs[hash]; ok {
		return *t, nil
	}
	return sqlcgen.EmailVerificationToken{}, errors.New("not found")
}

func (f *authFakeRepo) MarkEmailVerificationTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.verifs {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *authFakeRepo) DeleteUnusedEmailVerificationTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.verifs {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.verifs, h)
		}
	}
	return nil
}

func (f *authFakeRepo) SetUserEmailVerified(_ context.Context, id uuid.UUID) error {
	for k, u := range f.users {
		if u.ID == id {
			u.EmailVerified = true
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *authFakeRepo) DeactivateUser(_ context.Context, id uuid.UUID) error {
	for k, u := range f.users {
		if u.ID == id {
			u.IsActive = false
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *authFakeRepo) CreatePasswordResetToken(_ context.Context, a sqlcgen.CreatePasswordResetTokenParams) (sqlcgen.PasswordResetToken, error) {
	t := sqlcgen.PasswordResetToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.resets[a.TokenHash] = &t
	return t, nil
}

func (f *authFakeRepo) GetPasswordResetToken(_ context.Context, hash string) (sqlcgen.PasswordResetToken, error) {
	if t, ok := f.resets[hash]; ok {
		return *t, nil
	}
	return sqlcgen.PasswordResetToken{}, errors.New("not found")
}

func (f *authFakeRepo) MarkPasswordResetTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.resets {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *authFakeRepo) DeleteUnusedPasswordResetTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.resets {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.resets, h)
		}
	}
	return nil
}

func (f *authFakeRepo) UpdateUserPassword(_ context.Context, a sqlcgen.UpdateUserPasswordParams) error {
	for k, u := range f.users {
		if u.ID == a.ID {
			u.PasswordHash = a.PasswordHash
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *authFakeRepo) CountUsers(context.Context) (int64, error) { return int64(len(f.users)), nil }

// CountUsersMatching mirrors ListUsers' username/email substring filter.
func (f *authFakeRepo) CountUsersMatching(_ context.Context, query string) (int64, error) {
	if query == "" {
		return int64(len(f.users)), nil
	}
	q := strings.ToLower(query)
	var n int64
	for _, u := range f.users {
		if strings.Contains(strings.ToLower(u.Username), q) ||
			strings.Contains(strings.ToLower(u.Email), q) {
			n++
		}
	}
	return n, nil
}

func (f *authFakeRepo) CreateSession(_ context.Context, a sqlcgen.CreateSessionParams) (sqlcgen.CreateSessionRow, error) {
	id := uuid.New()
	f.sessions[id] = &sqlcgen.GetSessionByRefreshHashRow{
		ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash,
		UserAgent: a.UserAgent, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	return sqlcgen.CreateSessionRow{ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash, ExpiresAt: a.ExpiresAt}, nil
}

func (f *authFakeRepo) GetSessionByRefreshHash(_ context.Context, hash string) (sqlcgen.GetSessionByRefreshHashRow, error) {
	for _, s := range f.sessions {
		if s.RefreshHash == hash {
			return *s, nil
		}
	}
	return sqlcgen.GetSessionByRefreshHashRow{}, errors.New("not found")
}

func (f *authFakeRepo) RevokeSession(_ context.Context, id uuid.UUID) error {
	if s, ok := f.sessions[id]; ok {
		s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		s.RevokedReason = "signed_out"
	}
	return nil
}

// RotateSession mirrors the SQL: same revoke, but stamped with the reason whose
// REUSE is the compromise signal.
func (f *authFakeRepo) RotateSession(_ context.Context, id uuid.UUID) error {
	if s, ok := f.sessions[id]; ok {
		s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		s.RevokedReason = "rotated"
	}
	return nil
}

func (f *authFakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	for _, s := range f.sessions {
		if s.UserID == userID {
			s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			s.RevokedReason = "signed_out"
		}
	}
	return nil
}

// RevokeOtherUserSessions mirrors the SQL: every session for the user EXCEPT
// the named one.
func (f *authFakeRepo) RevokeOtherUserSessions(_ context.Context, a sqlcgen.RevokeOtherUserSessionsParams) error {
	for _, s := range f.sessions {
		if s.UserID == a.UserID && s.ID != a.ID {
			s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			s.RevokedReason = "signed_out"
		}
	}
	return nil
}

// GetActiveSessionForAccessToken mirrors the SQL join: no row for a revoked or
// expired session, or for a disabled/tombstoned account, and the account's
// CURRENT role on the row it does return.
func (f *authFakeRepo) GetActiveSessionForAccessToken(_ context.Context, id uuid.UUID) (sqlcgen.GetActiveSessionForAccessTokenRow, error) {
	s, ok := f.sessions[id]
	if !ok || s.RevokedAt.Valid || !s.ExpiresAt.After(time.Now()) {
		return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
	}
	for _, u := range f.users {
		if u.ID == s.UserID {
			if !u.IsActive || u.DeletedAt.Valid {
				return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
			}
			return sqlcgen.GetActiveSessionForAccessTokenRow{ID: s.ID, UserID: s.UserID, Role: u.Role}, nil
		}
	}
	return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
}

func (f *authFakeRepo) UpsertOwnerClaimToken(_ context.Context, tokenHash string) (sqlcgen.OwnerClaimToken, error) {
	f.ownerClaim = &sqlcgen.OwnerClaimToken{ID: true, TokenHash: tokenHash, CreatedAt: time.Now()}
	return *f.ownerClaim, nil
}

func (f *authFakeRepo) GetUnclaimedOwnerClaimToken(context.Context) (sqlcgen.OwnerClaimToken, error) {
	if f.ownerClaim == nil || f.ownerClaim.ClaimedAt.Valid {
		return sqlcgen.OwnerClaimToken{}, pgx.ErrNoRows
	}
	return *f.ownerClaim, nil
}

// ClaimOwnerAndCreateAdmin mirrors the single-statement CTE: the
// claimed_at-IS-NULL guard is the single-winner gate (loser gets ErrNoRows)
// and a unique violation leaves the token unclaimed.
func (f *authFakeRepo) ClaimOwnerAndCreateAdmin(ctx context.Context, a sqlcgen.ClaimOwnerAndCreateAdminParams) (sqlcgen.ClaimOwnerAndCreateAdminRow, error) {
	if f.ownerClaim == nil || f.ownerClaim.TokenHash != a.TokenHash || f.ownerClaim.ClaimedAt.Valid {
		return sqlcgen.ClaimOwnerAndCreateAdminRow{}, pgx.ErrNoRows
	}
	u, err := f.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: a.Username, Email: a.Email, PasswordHash: a.PasswordHash,
		Role: "admin", HistoryEnabled: a.HistoryEnabled,
	})
	if err != nil {
		return sqlcgen.ClaimOwnerAndCreateAdminRow{}, err
	}
	// The SQL's INSERT writes is_owner = TRUE (0131). Mirrored here because the
	// owner guards read that column and a fake that skipped it would let them
	// pass a test the database would fail.
	u.IsOwner = true
	f.users[strings.ToLower(u.Email)] = u
	f.ownerClaim.ClaimedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return sqlcgen.ClaimOwnerAndCreateAdminRow{
		ID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash,
		Role: u.Role, EmailVerified: u.EmailVerified, IsActive: u.IsActive,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DisplayName: u.DisplayName,
		Bio: u.Bio, PendingEmailVerification: u.PendingEmailVerification,
		HistoryEnabled: u.HistoryEnabled,
		// Mirrors the RETURNING list: the defaulted discovery columns come back
		// with the row, so the service can carry them into the session payload.
		SearchHistoryEnabled:               u.SearchHistoryEnabled,
		PersonalizedSearchEnabled:          u.PersonalizedSearchEnabled,
		PersonalizedRecommendationsEnabled: u.PersonalizedRecommendationsEnabled,
	}, nil
}

func (f *authFakeRepo) CreateUser(_ context.Context, a sqlcgen.CreateUserParams) (sqlcgen.User, error) {
	key := strings.ToLower(a.Email)
	if _, ok := f.users[key]; ok {
		return sqlcgen.User{}, &pgconn.PgError{Code: "23505"}
	}
	u := sqlcgen.User{
		ID: uuid.New(), Username: a.Username, Email: a.Email,
		PasswordHash: a.PasswordHash, Role: a.Role, IsActive: true, CreatedAt: time.Now(),
		PendingEmailVerification: a.PendingEmailVerification,
		HistoryEnabled:           a.HistoryEnabled,
		// Search prefs mirror the migration's NOT NULL DEFAULT TRUE (W4).
		SearchHistoryEnabled:               true,
		PersonalizedSearchEnabled:          true,
		PersonalizedRecommendationsEnabled: true,
	}
	f.users[key] = u
	return u, nil
}

func (f *authFakeRepo) GetUserByEmail(_ context.Context, lowerEmail string) (sqlcgen.User, error) {
	u, ok := f.users[strings.ToLower(lowerEmail)]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

// GetUserByLoginIdentifier mirrors the real sign-in query: email branch first
// (email always wins), then the username branch, neither filtering is_active.
func (f *authFakeRepo) GetUserByLoginIdentifier(_ context.Context, identifier string) (sqlcgen.User, error) {
	if u, ok := f.users[strings.ToLower(identifier)]; ok {
		return u, nil
	}
	for _, u := range f.users {
		if strings.EqualFold(u.Username, identifier) {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func (f *authFakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func (f *authFakeRepo) GetPublicUserProfileByUsername(_ context.Context, username string) (sqlcgen.GetPublicUserProfileByUsernameRow, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Username, username) && u.IsActive && u.ProfilePublic {
			return sqlcgen.GetPublicUserProfileByUsernameRow{
				ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
				Bio: u.Bio, CreatedAt: u.CreatedAt, ProfilePublic: true,
				ShowBluesky: u.ShowBluesky,
			}, nil
		}
	}
	return sqlcgen.GetPublicUserProfileByUsernameRow{}, errors.New("not found")
}

func (f *authFakeRepo) CreateRegistrationRequest(_ context.Context, a sqlcgen.CreateRegistrationRequestParams) (sqlcgen.CreateRegistrationRequestRow, error) {
	for _, r := range f.regReqs {
		if r.status == "pending" && (strings.EqualFold(r.email, a.Email) || strings.EqualFold(r.username, a.Username)) {
			return sqlcgen.CreateRegistrationRequestRow{}, &pgconn.PgError{Code: "23505"}
		}
	}
	rr := &regReqRow{
		id: uuid.New(), username: a.Username, email: a.Email, passwordHash: a.PasswordHash,
		note: a.Note, status: "pending", createdAt: time.Now(),
	}
	f.regReqs = append(f.regReqs, rr)
	return sqlcgen.CreateRegistrationRequestRow{
		ID: rr.id, Username: rr.username, Email: rr.email, Note: rr.note,
		Status: rr.status, ModeratorNote: rr.moderatorNote, CreatedAt: rr.createdAt,
	}, nil
}

func (f *authFakeRepo) ListRegistrationRequests(_ context.Context, a sqlcgen.ListRegistrationRequestsParams) ([]sqlcgen.ListRegistrationRequestsRow, error) {
	var rows []sqlcgen.ListRegistrationRequestsRow
	for i := len(f.regReqs) - 1; i >= 0; i-- { // newest first
		r := f.regReqs[i]
		if a.Status != nil && r.status != *a.Status {
			continue
		}
		rows = append(rows, sqlcgen.ListRegistrationRequestsRow{
			ID: r.id, Username: r.username, Email: r.email, Note: r.note,
			Status: r.status, ModeratorNote: r.moderatorNote, ReviewedAt: r.reviewedAt, CreatedAt: r.createdAt,
		})
	}
	return rows, nil
}

func (f *authFakeRepo) ApproveRegistrationRequest(ctx context.Context, a sqlcgen.ApproveRegistrationRequestParams) (sqlcgen.ApproveRegistrationRequestRow, error) {
	for _, r := range f.regReqs {
		if r.id == a.ID && r.status == "pending" {
			u, err := f.CreateUser(ctx, sqlcgen.CreateUserParams{
				Username: r.username, Email: r.email, PasswordHash: r.passwordHash, Role: "user",
				PendingEmailVerification: a.PendingEmailVerification,
				HistoryEnabled:           a.HistoryEnabled,
			})
			if err != nil {
				return sqlcgen.ApproveRegistrationRequestRow{}, err
			}
			r.status = "approved"
			r.reviewedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return sqlcgen.ApproveRegistrationRequestRow{
				ID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash,
				Role: u.Role, EmailVerified: u.EmailVerified, IsActive: u.IsActive,
				CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DisplayName: u.DisplayName, Bio: u.Bio,
				PendingEmailVerification: u.PendingEmailVerification,
			}, nil
		}
	}
	return sqlcgen.ApproveRegistrationRequestRow{}, pgx.ErrNoRows
}

// RejectRegistrationRequest mirrors the SQL's RETURNING: the applicant on a
// hit, pgx.ErrNoRows on an unknown or already-resolved id.
func (f *authFakeRepo) RejectRegistrationRequest(_ context.Context, a sqlcgen.RejectRegistrationRequestParams) (sqlcgen.RejectRegistrationRequestRow, error) {
	for _, r := range f.regReqs {
		if r.id == a.ID && r.status == "pending" {
			r.status = "rejected"
			r.moderatorNote = a.ModeratorNote
			r.reviewedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return sqlcgen.RejectRegistrationRequestRow{Username: r.username, Email: r.email}, nil
		}
	}
	return sqlcgen.RejectRegistrationRequestRow{}, pgx.ErrNoRows
}

func (f *authFakeRepo) UpdateUserProfile(_ context.Context, a sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error) {
	for k, u := range f.users {
		if u.ID == a.ID {
			if a.DisplayName != nil {
				u.DisplayName = *a.DisplayName
			}
			if a.Bio != nil {
				u.Bio = *a.Bio
			}
			if a.Unlisted != nil {
				u.Unlisted = *a.Unlisted
			}
			if a.HistoryEnabled != nil {
				u.HistoryEnabled = *a.HistoryEnabled
			}
			if a.ProfilePublic != nil {
				u.ProfilePublic = *a.ProfilePublic
			}
			if a.ShowBluesky != nil {
				u.ShowBluesky = *a.ShowBluesky
			}
			if a.SearchHistoryEnabled != nil {
				u.SearchHistoryEnabled = *a.SearchHistoryEnabled
			}
			if a.PersonalizedSearchEnabled != nil {
				u.PersonalizedSearchEnabled = *a.PersonalizedSearchEnabled
			}
			if a.PersonalizedRecommendationsEnabled != nil {
				u.PersonalizedRecommendationsEnabled = *a.PersonalizedRecommendationsEnabled
			}
			if a.SetSensitiveContentPolicy {
				u.SensitiveContentPolicy = a.SensitiveContentPolicy
			}
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

// SumUserStorageUsage lets authFakeRepo satisfy quota.Repository (and the
// admin.Repository usage read). Delegates to the wired usage func (0 when unset).
func (f *authFakeRepo) SumUserStorageUsage(_ context.Context, ownerID uuid.UUID) (int64, error) {
	if f.usage == nil {
		return 0, nil
	}
	return f.usage(ownerID), nil
}

// uploadUsageEvent is one recorded daily-ledger row (config-parity W7).
type uploadUsageEvent struct {
	userID    uuid.UUID
	bytes     int64
	createdAt time.Time
}

// The upload-usage ledger methods let authFakeRepo keep satisfying
// quota.Repository (rolling daily quota, config-parity W7).
func (f *authFakeRepo) RecordUploadUsageEvent(_ context.Context, a sqlcgen.RecordUploadUsageEventParams) error {
	f.uploadUsage = append(f.uploadUsage, uploadUsageEvent{userID: a.UserID, bytes: a.Bytes, createdAt: time.Now()})
	return nil
}

func (f *authFakeRepo) SumUploadUsageSince(_ context.Context, a sqlcgen.SumUploadUsageSinceParams) (int64, error) {
	var sum int64
	for _, e := range f.uploadUsage {
		if e.userID == a.UserID && e.createdAt.After(a.CreatedAt) {
			sum += e.bytes
		}
	}
	return sum, nil
}

func (f *authFakeRepo) PruneUploadUsageEvents(_ context.Context, a sqlcgen.PruneUploadUsageEventsParams) (int64, error) {
	kept := f.uploadUsage[:0]
	var n int64
	for _, e := range f.uploadUsage {
		if e.userID == a.UserID && e.createdAt.Before(a.CreatedAt) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	f.uploadUsage = kept
	return n, nil
}

// The instance-wide overview reads (admin.Repository.Stats). Each delegates to a
// wired stub (0 when unset).
func (f *authFakeRepo) CountPublicVideos(context.Context) (int64, error) {
	if f.statPublicVideos == nil {
		return 0, nil
	}
	return f.statPublicVideos(), nil
}

func (f *authFakeRepo) SumAllStorageUsage(context.Context) (int64, error) {
	if f.statAllStorage == nil {
		return 0, nil
	}
	return f.statAllStorage(), nil
}

func (f *authFakeRepo) CountComments(context.Context) (int64, error) {
	if f.statComments == nil {
		return 0, nil
	}
	return f.statComments(), nil
}

func (f *authFakeRepo) CountFederatedPeers(context.Context) (int64, error) {
	if f.statPeers == nil {
		return 0, nil
	}
	return f.statPeers(), nil
}

// ListUsers + AdminUpdateUser (+ SumUserStorageUsage above) let authFakeRepo
// also satisfy admin.Repository.
func (f *authFakeRepo) ListUsers(_ context.Context, a sqlcgen.ListUsersParams) ([]sqlcgen.ListUsersRow, error) {
	var out []sqlcgen.ListUsersRow
	q := strings.ToLower(a.Query)
	for _, u := range f.users {
		if q == "" || strings.Contains(strings.ToLower(u.Username), q) || strings.Contains(strings.ToLower(u.Email), q) {
			used, _ := f.SumUserStorageUsage(context.Background(), u.ID)
			out = append(out, sqlcgen.ListUsersRow{
				ID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash,
				Role: u.Role, EmailVerified: u.EmailVerified, IsActive: u.IsActive,
				CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
				DisplayName: u.DisplayName, Bio: u.Bio,
				StorageQuotaBytes: u.StorageQuotaBytes, StorageUsedBytes: used,
				BypassQuarantine: u.BypassQuarantine, DeletedAt: u.DeletedAt,
				IsOwner: u.IsOwner,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	lo := int(a.ResultOffset)
	if lo > len(out) {
		lo = len(out)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(out) {
		hi = len(out)
	}
	return out[lo:hi], nil
}

// CountActiveAdmins mirrors the SQL predicate exactly: role='admin' AND
// is_active AND deleted_at IS NULL.
func (f *authFakeRepo) CountActiveAdmins(_ context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if u.Role == "admin" && u.IsActive && !u.DeletedAt.Valid {
			n++
		}
	}
	return n, nil
}

func (f *authFakeRepo) AdminUpdateUser(_ context.Context, a sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error) {
	for k, u := range f.users {
		if u.ID == a.ID {
			if a.Role != nil {
				u.Role = *a.Role
			}
			if a.IsActive != nil {
				u.IsActive = *a.IsActive
			}
			if a.EmailVerified != nil {
				u.EmailVerified = *a.EmailVerified
			}
			if a.BypassQuarantine != nil {
				u.BypassQuarantine = *a.BypassQuarantine
			}
			if a.SetStorageQuota {
				u.StorageQuotaBytes = a.StorageQuotaBytes
			}
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func authServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := authServerWithFakeRepo(t)
	return srv
}

// authServerWithFakeRepo is authServer plus a handle on the backing fake, for
// the tests that must put an account into a state no endpoint can produce (an
// empty password hash, a revoked session). One constructor, so the wiring — and
// the test signing secret — has a single definition.
func authServerWithFakeRepo(t *testing.T) (*Server, *authFakeRepo) {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute)), repo
}

func postTo(srv *Server, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRegisterEndpointCreatesAccount(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Token == "" || body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		t.Errorf("unexpected auth response: %+v", body)
	}
	// Registration never mints admins (0104): even the first registered
	// account is a plain user — the admin exists only via /setup/claim-owner.
	if body.User.Role != "user" {
		t.Errorf("first registered user role = %q, want user", body.User.Role)
	}
	// The password hash must never appear in the response.
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Error("response leaked password_hash")
	}
}

func TestRegisterEndpointValidationError(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"a","email":"nope","password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "unprocessable_entity" || len(body.Error.Fields) == 0 {
		t.Errorf("expected field errors, got %+v", body.Error)
	}
}

func TestRegisterEndpointDuplicateConflict(t *testing.T) {
	srv := authServer(t)
	const body = `{"username":"ada","email":"ada@example.test","password":"supersecret"}`
	_ = postTo(srv, "/api/v1/auth/register", body)
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "conflict" {
		t.Errorf("code = %q, want conflict", er.Error.Code)
	}
}

// registerAndToken creates an account and returns its access token. See
// registerTokens for the empty-instance owner-claim routing.
func registerAndToken(t *testing.T, srv *Server, body string) string {
	t.Helper()
	return registerTokens(t, srv, body).Token
}

// claimOwnerTokens bootstraps THE admin through the real first-run flow
// (0104): mint the owner-claim token via the service seam (exactly as boot
// does), then redeem it over the API. body is a register-shaped JSON object;
// the token field is injected.
func claimOwnerTokens(t *testing.T, srv *Server, body string) authResponse {
	t.Helper()
	raw, minted, _, err := srv.authsvc.EnsureOwnerClaimToken(context.Background())
	if err != nil || !minted {
		t.Fatalf("EnsureOwnerClaimToken: minted=%v err=%v", minted, err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("unmarshal register body: %v", err)
	}
	fields["token"] = raw
	claimBody, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal claim body: %v", err)
	}
	rec := postTo(srv, "/api/v1/setup/claim-owner", string(claimBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim-owner status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar
}

func getWithAuth(srv *Server, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func postWithAuth(srv *Server, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestRequireRole exercises the role gate via a test-only route (no admin route
// exists in the public surface yet; P9 admin endpoints will mount this).
func TestRequireRole(t *testing.T) {
	srv := authServer(t)
	srv.echo.GET("/api/v1/_test/admin-only", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	}, srv.requireAuth, srv.requireRole("admin"))

	adminTok := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`).Token
	userTok := registerTokens(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`).Token

	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", userTok); rec.Code != http.StatusForbidden {
		t.Errorf("user token = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", adminTok); rec.Code != http.StatusOK {
		t.Errorf("admin token = %d, want 200", rec.Code)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := getWithAuth(srv, "/api/v1/auth/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", er.Error.Code)
	}
}

func TestMeRejectsBadToken(t *testing.T) {
	srv := authServer(t)
	rec := getWithAuth(srv, "/api/v1/auth/me", "not-a-real-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeReturnsCurrentUser(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/auth/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.Username != "ada" || u.Email != "ada@example.test" {
		t.Errorf("unexpected user: %+v", u)
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Error("response leaked password_hash")
	}
}

func TestUpdateMeProfile(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Partial update: set display_name and bio.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Ada L.","bio":"hi"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.DisplayName != "Ada L." || u.Bio != "hi" {
		t.Errorf("unexpected profile: %+v", u)
	}

	// GET /me reflects the change after a fresh read.
	me := getWithAuth(srv, "/api/v1/auth/me", token)
	var got userView
	_ = json.Unmarshal(me.Body.Bytes(), &got)
	if got.DisplayName != "Ada L." || got.Bio != "hi" {
		t.Errorf("me did not reflect update: %+v", got)
	}
}

// TestUpdateMeSensitiveContentPolicy exercises the per-user sensitive-content
// policy override (0100): each enum mode round-trips, "" clears it back to
// inherit (omitted on GET), and an unknown value is a 422.
func TestUpdateMeSensitiveContentPolicy(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// A fresh account inherits the instance policy: the field is omitted.
	var fresh userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", token).Body.Bytes(), &fresh)
	if fresh.SensitiveContentPolicy != nil {
		t.Fatalf("fresh account policy = %v, want nil (inherit)", *fresh.SensitiveContentPolicy)
	}

	// Each of the four modes round-trips through PATCH and a fresh GET.
	for _, mode := range []string{"hide", "warn", "blur", "display"} {
		rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
			`{"sensitive_content_policy":"`+mode+`"}`, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch %q = %d, want 200; body=%s", mode, rec.Code, rec.Body.String())
		}
		var u userView
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		if u.SensitiveContentPolicy == nil || *u.SensitiveContentPolicy != mode {
			t.Fatalf("patch %q response policy = %v", mode, u.SensitiveContentPolicy)
		}
		var got userView
		_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", token).Body.Bytes(), &got)
		if got.SensitiveContentPolicy == nil || *got.SensitiveContentPolicy != mode {
			t.Errorf("me policy after %q = %v", mode, got.SensitiveContentPolicy)
		}
	}

	// "" clears the override back to inherit: GET omits the field again.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
		`{"sensitive_content_policy":""}`, token); rec.Code != http.StatusOK {
		t.Fatalf("clear patch = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var cleared userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", token).Body.Bytes(), &cleared)
	if cleared.SensitiveContentPolicy != nil {
		t.Errorf("policy after clear = %v, want nil (inherit)", *cleared.SensitiveContentPolicy)
	}

	// An unknown value is rejected.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
		`{"sensitive_content_policy":"nonsense"}`, token); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid policy = %d, want 422", rec.Code)
	}
}

func TestUpdateMeValidationAndAuth(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if empty := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{}`, token); empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", empty.Code)
	}
	if anon := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"bio":"x"}`, ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon patch = %d, want 401", anon.Code)
	}
}

func TestLoginEndpointSuccessAndFailure(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	ok := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", ok.Code, ok.Body.String())
	}

	bad := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"wrong"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d, want 401", bad.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(bad.Body.Bytes(), &er)
	if er.Error.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", er.Error.Code)
	}
}

// registerTokens registers an account and returns the full token pair.
// registerTokens creates an account and returns the full auth response. On an
// EMPTY instance it goes through the first-run owner-claim flow instead of
// plain registration, mirroring production since 0104: the first account is
// THE admin, created by redeeming the boot-minted setup token — registration
// never mints admins. Tests that relied on "first registered account becomes
// admin" keep working unchanged through these helpers.
func registerTokens(t *testing.T, srv *Server, body string) authResponse {
	t.Helper()
	if n, err := srv.authsvc.CountUsers(context.Background()); err == nil && n == 0 {
		return claimOwnerTokens(t, srv, body)
	}
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar
}

func TestRegisterReturnsRefreshToken(t *testing.T) {
	ar := registerTokens(t, authServer(t), `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if ar.RefreshToken == "" {
		t.Error("register did not return a refresh_token")
	}
}

func TestRefreshEndpointRotates(t *testing.T) {
	srv := authServer(t)
	ar := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var rotated authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == ar.RefreshToken {
		t.Errorf("refresh token not rotated: old=%q new=%q", ar.RefreshToken, rotated.RefreshToken)
	}

	// The old (now-rotated) token must be rejected.
	reuse := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401", reuse.Code)
	}
}

func TestRefreshEndpointRejectsUnknown(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"nope"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRefreshEndpointValidation(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/refresh", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestLogoutAllRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/logout-all", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	srv := authServer(t)
	first := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	// A second login creates a second session for the same account.
	loginRec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	var second authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := postWithAuth(srv, "/api/v1/auth/logout-all", first.Token)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout-all status = %d, want 204; body=%s", out.Code, out.Body.String())
	}

	for name, tok := range map[string]string{"first": first.RefreshToken, "second": second.RefreshToken} {
		rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+tok+`"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s refresh after logout-all = %d, want 401", name, rec.Code)
		}
	}
}

func TestLogoutEndpointRevokes(t *testing.T) {
	srv := authServer(t)
	ar := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	out := postTo(srv, "/api/v1/auth/logout", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", out.Code)
	}

	// After logout the refresh token can no longer be rotated.
	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh-after-logout status = %d, want 401", rec.Code)
	}
}

// captureResetMailer records the delivered token (reset or verification) so the
// handler tests can drive the confirm step.
type captureResetMailer struct {
	calls int
	token string
	// changedEmail records the address the "your password was changed" notice
	// was sent to; fail makes every send fail, so a test can prove the notice is
	// best-effort and never fails the underlying action.
	changedEmail string
	fail         bool
	// The two-step email change (0129): the confirmation token and the address
	// it was addressed to, then the old/new pair the change NOTICE named. The
	// addressing is the security property, so the tests assert on it.
	changeToken string
	changeTo    string
	noticeOld   string
	noticeNew   string
	// regDecisions records the signup approval/rejection notices, in send order.
	regDecisions  []auth.CapturedRegistrationDecision
	changeNotices int
}

func (m *captureResetMailer) SendPasswordChanged(_ context.Context, email string) error {
	if m.fail {
		return errors.New("mailer down")
	}
	m.calls++
	m.changedEmail = email
	return nil
}

func (m *captureResetMailer) SendPasswordReset(_ context.Context, _, token string) error {
	m.calls++
	m.token = token
	return nil
}

func (m *captureResetMailer) SendEmailVerification(_ context.Context, _, token string) error {
	m.calls++
	m.token = token
	return nil
}

func (m *captureResetMailer) SendEmailChangeVerification(_ context.Context, newEmail, token string) error {
	m.calls++
	m.changeToken = token
	m.changeTo = newEmail
	return nil
}

func (m *captureResetMailer) SendEmailChanged(_ context.Context, oldEmail, newEmail string) error {
	if m.fail {
		return errors.New("mailer down")
	}
	m.changeNotices++
	m.noticeOld = oldEmail
	m.noticeNew = newEmail
	return nil
}

func (m *captureResetMailer) SendNewReportAlert(context.Context, string, string, string, string) error {
	return nil
}

func (m *captureResetMailer) SendContactForm(context.Context, string, string, string, string, string) error {
	return nil
}

// The signup-decision notices are captured so a test can prove that the
// wrong-actor refusals on the approval queue send nothing at all.
func (m *captureResetMailer) SendRegistrationApproved(_ context.Context, email, username, signInURL string, verifyRequired bool) error {
	m.regDecisions = append(m.regDecisions, auth.CapturedRegistrationDecision{
		Decision: "approved", Email: email, Username: username,
		SignInURL: signInURL, VerifyRequired: verifyRequired,
	})
	return nil
}

func (m *captureResetMailer) SendRegistrationRejected(_ context.Context, email, username, note string) error {
	m.regDecisions = append(m.regDecisions, auth.CapturedRegistrationDecision{
		Decision: "rejected", Email: email, Username: username, Note: note,
	})
	return nil
}

func authServerWithMailer(t *testing.T) (*Server, *captureResetMailer) {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute)), mailer
}

func TestPasswordResetFlow(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, want 202", rec.Code)
	}
	if mailer.token == "" {
		t.Fatal("expected a reset token to be delivered")
	}

	rec = postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+mailer.token+`","password":"brand-new-pass"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// The new password logs in; the old one is rejected.
	if ok := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"brand-new-pass"}`); ok.Code != http.StatusOK {
		t.Errorf("login with new password = %d, want 200", ok.Code)
	}
	if bad := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`); bad.Code != http.StatusUnauthorized {
		t.Errorf("login with old password = %d, want 401", bad.Code)
	}
}

func TestPasswordResetRequestIsEnumerationSafe(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"nobody@example.test"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even for an unknown email", rec.Code)
	}
	if mailer.calls != 0 {
		t.Errorf("mailer called %d times for an unknown email, want 0", mailer.calls)
	}
}

func TestPasswordResetRequestValidatesEmail(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"not-an-email"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestPasswordResetConfirmRejectsBadToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"not-a-real-token","password":"brand-new-pass"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPasswordResetConfirmValidatesPassword(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"x","password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// A fresh account is not yet verified.
	var before userView
	if err := json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", reg.Token).Body.Bytes(), &before); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if before.EmailVerified {
		t.Fatal("a fresh account should not be email-verified")
	}

	// Request verification (authed) → 202, token delivered.
	rec := postWithAuth(srv, "/api/v1/auth/verify-email", reg.Token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, want 202", rec.Code)
	}
	if mailer.token == "" {
		t.Fatal("expected a verification token to be delivered")
	}

	// Confirm (public, with the token) → 204.
	rec = postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"`+mailer.token+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// /me now reflects the verified state.
	var after userView
	if err := json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", reg.Token).Body.Bytes(), &after); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if !after.EmailVerified {
		t.Error("email_verified should be true after confirm")
	}
}

func TestEmailVerificationRequestRequiresAuth(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestEmailVerificationConfirmRejectsBadToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"not-a-real-token"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEmailVerificationConfirmValidatesToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestDeactivateAccountFlow(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Wrong password is rejected and leaves the account active.
	bad := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{"password":"wrong"}`, reg.Token)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("wrong-password status = %d, want 403", bad.Code)
	}
	if me := getWithAuth(srv, "/api/v1/auth/me", reg.Token); me.Code != http.StatusOK {
		t.Fatalf("account should still be usable after a failed deactivate: /me = %d", me.Code)
	}

	// Correct password deactivates → 204.
	ok := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{"password":"supersecret"}`, reg.Token)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("deactivate status = %d, want 204; body=%s", ok.Code, ok.Body.String())
	}

	// Login is now refused (account disabled), and the access token stops resolving.
	if login := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`); login.Code != http.StatusForbidden {
		t.Errorf("login after deactivate = %d, want 403", login.Code)
	}
	if me := getWithAuth(srv, "/api/v1/auth/me", reg.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("/me after deactivate = %d, want 401", me.Code)
	}
}

func TestDeactivateAccountRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/me/deactivate", `{"password":"supersecret"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestDeactivateAccountValidatesPassword(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{}`, reg.Token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func (f *authFakeRepo) CountRegistrationRequests(ctx context.Context, status *string) (int64, error) {
	rows, err := f.ListRegistrationRequests(ctx, sqlcgen.ListRegistrationRequestsParams{Status: status, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
