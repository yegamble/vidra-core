package storagemigration

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory stand-in for the generated query set. It reproduces
// the STATE GUARDS of the real SQL (terminal writes only apply to a row in the
// state the claim put it in, the claim only takes rows from a live campaign),
// because those guards are load-bearing and a fake that ignored them would let
// the tests pass on code the database would reject.
type fakeRepo struct {
	mu         sync.Mutex
	campaigns  []sqlcgen.StorageMigration
	objects    map[string]*sqlcgen.StorageMigrationObject
	fileHashes map[string]string
	now        func() time.Time
	// identity is what instance_identity holds; identityErr stands in for the
	// install whose migrations have not been run, where the identity cannot be
	// read at all and the destination therefore cannot be claimed.
	identity    uuid.UUID
	identityErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		objects:    map[string]*sqlcgen.StorageMigrationObject{},
		fileHashes: map[string]string{},
		now:        time.Now,
		identity:   uuid.New(),
	}
}

func terminalCampaign(state string) bool {
	return state == StateDone || state == StateCancelled || state == StateFailed
}

func (f *fakeRepo) find(id uuid.UUID) *sqlcgen.StorageMigration {
	for i := range f.campaigns {
		if f.campaigns[i].ID == id {
			return &f.campaigns[i]
		}
	}
	return nil
}

func (f *fakeRepo) CreateStorageMigration(_ context.Context, arg sqlcgen.CreateStorageMigrationParams) (sqlcgen.StorageMigration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.campaigns {
		if !terminalCampaign(c.State) {
			return sqlcgen.StorageMigration{}, errUnique
		}
	}
	now := f.now()
	row := sqlcgen.StorageMigration{
		ID: uuid.New(), SourceDesc: arg.SourceDesc, TargetDesc: arg.TargetDesc,
		State: StateEnumerating, CreatedAt: now, UpdatedAt: now,
	}
	f.campaigns = append(f.campaigns, row)
	return row, nil
}

func (f *fakeRepo) GetStorageMigration(_ context.Context, id uuid.UUID) (sqlcgen.StorageMigration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c != nil {
		return *c, nil
	}
	return sqlcgen.StorageMigration{}, pgx.ErrNoRows
}

func (f *fakeRepo) GetActiveStorageMigration(context.Context) (sqlcgen.StorageMigration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.campaigns) - 1; i >= 0; i-- {
		if !terminalCampaign(f.campaigns[i].State) {
			return f.campaigns[i], nil
		}
	}
	return sqlcgen.StorageMigration{}, pgx.ErrNoRows
}

func (f *fakeRepo) ListStorageMigrations(_ context.Context, limit int32) ([]sqlcgen.StorageMigration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlcgen.StorageMigration, 0, len(f.campaigns))
	for i := len(f.campaigns) - 1; i >= 0 && int32(len(out)) < limit; i-- {
		out = append(out, f.campaigns[i])
	}
	return out, nil
}

func (f *fakeRepo) SetStorageMigrationState(_ context.Context, arg sqlcgen.SetStorageMigrationStateParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(arg.ID)
	if c == nil || terminalCampaign(c.State) {
		return nil
	}
	c.State, c.LastError, c.UpdatedAt = arg.State, arg.LastError, f.now()
	return nil
}

func (f *fakeRepo) SetStorageMigrationError(_ context.Context, arg sqlcgen.SetStorageMigrationErrorParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(arg.ID)
	if c == nil || terminalCampaign(c.State) || c.LastError == arg.LastError {
		return nil
	}
	c.LastError, c.UpdatedAt = arg.LastError, f.now()
	return nil
}

func (f *fakeRepo) MarkStorageMigrationCutover(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil || (c.State != StateCopying && c.State != StateSynced) {
		return nil
	}
	c.State, c.LastError, c.UpdatedAt = StateCutover, "", f.now()
	if !c.ObservedCutoverAt.Valid {
		c.ObservedCutoverAt = pgtype.Timestamptz{Time: f.now(), Valid: true}
	}
	return nil
}

func (f *fakeRepo) CancelStorageMigration(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil || terminalCampaign(c.State) {
		return 0, nil
	}
	c.State, c.UpdatedAt = StateCancelled, f.now()
	return 1, nil
}

func (f *fakeRepo) RefreshStorageMigrationCounters(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return nil
	}
	var total, done, failed int64
	for _, o := range f.objects {
		if o.CampaignID != id {
			continue
		}
		total++
		switch o.State {
		case ObjectVerified, ObjectSourceDeleted:
			done++
		case ObjectFailed:
			failed++
		}
	}
	c.ObjectsTotal, c.ObjectsDone, c.ObjectsFailed = total, done, failed
	return nil
}

func (f *fakeRepo) UpsertStorageMigrationObjects(_ context.Context, arg sqlcgen.UpsertStorageMigrationObjectsParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var added int64
	for _, key := range arg.ObjectKeys {
		if _, ok := f.objects[key]; ok {
			continue
		}
		now := f.now()
		f.objects[key] = &sqlcgen.StorageMigrationObject{
			ObjectKey: key, CampaignID: arg.CampaignID, State: ObjectPending,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
		added++
	}
	return added, nil
}

func (f *fakeRepo) ClaimDueStorageMigrationObjects(_ context.Context, limit int32) ([]sqlcgen.ClaimDueStorageMigrationObjectsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.objects))
	for key, o := range f.objects {
		// Due, still-actionable, and belonging to a campaign that is still
		// claiming — the three halves of the real WHERE clause.
		if o.State != ObjectPending || o.NextAttemptAt.After(f.now()) {
			continue
		}
		c := f.find(o.CampaignID)
		if c == nil || (c.State != StateCopying && c.State != StateSynced) {
			continue
		}
		keys = append(keys, key)
	}
	// Total order, exactly like the real ORDER BY (next_attempt_at, object_key).
	sort.Slice(keys, func(i, j int) bool {
		a, b := f.objects[keys[i]], f.objects[keys[j]]
		if !a.NextAttemptAt.Equal(b.NextAttemptAt) {
			return a.NextAttemptAt.Before(b.NextAttemptAt)
		}
		return a.ObjectKey < b.ObjectKey
	})
	out := make([]sqlcgen.ClaimDueStorageMigrationObjectsRow, 0, limit)
	for _, key := range keys {
		if int32(len(out)) >= limit {
			break
		}
		o := f.objects[key]
		o.State, o.NextAttemptAt, o.UpdatedAt = ObjectCopying, f.now().Add(30*time.Minute), f.now()
		out = append(out, sqlcgen.ClaimDueStorageMigrationObjectsRow{
			ObjectKey: o.ObjectKey, CampaignID: o.CampaignID, Attempts: o.Attempts,
		})
	}
	return out, nil
}

func (f *fakeRepo) RenewStorageMigrationObjectLease(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.objects[key]; ok && o.State == ObjectCopying {
		o.NextAttemptAt = f.now().Add(30 * time.Minute)
	}
	return nil
}

func (f *fakeRepo) MarkStorageMigrationObjectVerified(_ context.Context, arg sqlcgen.MarkStorageMigrationObjectVerifiedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[arg.ObjectKey]
	if !ok || o.State != ObjectCopying {
		return nil
	}
	o.State, o.Sha256, o.ByteSize, o.LastError, o.UpdatedAt = ObjectVerified, arg.Sha256, arg.ByteSize, "", f.now()
	return nil
}

func (f *fakeRepo) RescheduleStorageMigrationObject(_ context.Context, arg sqlcgen.RescheduleStorageMigrationObjectParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[arg.ObjectKey]
	if !ok || o.State != ObjectCopying {
		return nil
	}
	o.State, o.Attempts, o.NextAttemptAt, o.LastError, o.UpdatedAt =
		ObjectPending, o.Attempts+1, arg.NextAttemptAt, arg.LastError, f.now()
	return nil
}

func (f *fakeRepo) FailStorageMigrationObject(_ context.Context, arg sqlcgen.FailStorageMigrationObjectParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[arg.ObjectKey]
	if !ok || o.State != ObjectCopying {
		return nil
	}
	o.State, o.Attempts, o.LastError, o.UpdatedAt = ObjectFailed, o.Attempts+1, arg.LastError, f.now()
	return nil
}

func (f *fakeRepo) MarkStorageMigrationObjectSourceDeleted(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	if !ok || o.State != ObjectVerified {
		return nil
	}
	o.State, o.UpdatedAt = ObjectSourceDeleted, f.now()
	return nil
}

func (f *fakeRepo) ListVerifiedStorageMigrationObjects(_ context.Context, arg sqlcgen.ListVerifiedStorageMigrationObjectsParams) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for key, o := range f.objects {
		if o.CampaignID == arg.CampaignID && o.State == ObjectVerified {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if int32(len(keys)) > arg.Limit {
		keys = keys[:arg.Limit]
	}
	return keys, nil
}

func (f *fakeRepo) countStates(id uuid.UUID, states ...string) int64 {
	var n int64
	for _, o := range f.objects {
		if o.CampaignID != id {
			continue
		}
		for _, s := range states {
			if o.State == s {
				n++
				break
			}
		}
	}
	return n
}

func (f *fakeRepo) CountUncopiedStorageMigrationObjects(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countStates(id, ObjectPending, ObjectCopying), nil
}

func (f *fakeRepo) CountUnfinishedStorageMigrationObjects(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countStates(id, ObjectPending, ObjectCopying, ObjectVerified), nil
}

func (f *fakeRepo) CountStorageMigrationObjectsByState(_ context.Context, id uuid.UUID) ([]sqlcgen.CountStorageMigrationObjectsByStateRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int64{}
	for _, o := range f.objects {
		if o.CampaignID == id {
			counts[o.State]++
		}
	}
	states := make([]string, 0, len(counts))
	for s := range counts {
		states = append(states, s)
	}
	sort.Strings(states)
	out := make([]sqlcgen.CountStorageMigrationObjectsByStateRow, 0, len(states))
	for _, s := range states {
		out = append(out, sqlcgen.CountStorageMigrationObjectsByStateRow{State: s, Count: counts[s]})
	}
	return out, nil
}

func (f *fakeRepo) GetInstanceIdentity(context.Context) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.identityErr != nil {
		return uuid.UUID{}, f.identityErr
	}
	return f.identity, nil
}

func (f *fakeRepo) GetVideoFileSHA256ByStorageKey(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sum, ok := f.fileHashes[key]; ok {
		return sum, nil
	}
	return "", pgx.ErrNoRows
}

// state reads one object's state, for assertions.
func (f *fakeRepo) state(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.objects[key]; ok {
		return o.State
	}
	return ""
}

func (f *fakeRepo) object(key string) sqlcgen.StorageMigrationObject {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.objects[key]; ok {
		return *o
	}
	return sqlcgen.StorageMigrationObject{}
}

// errUnique reproduces Postgres 23505 so the Start path's race handling is
// exercised by the same code that runs in production.
var errUnique = &pgError{code: "23505"}

type pgError struct{ code string }

func (e *pgError) Error() string    { return "duplicate key value violates unique constraint" }
func (e *pgError) SQLState() string { return e.code }
func (e *pgError) Is(target error) bool {
	var other *pgError
	return errors.As(target, &other) && other.code == e.code
}
