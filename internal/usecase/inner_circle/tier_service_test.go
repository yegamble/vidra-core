package inner_circle

import (
	"context"
	"errors"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type fakeTierRepo struct {
	listResult []domain.InnerCircleTierWithCount
	listErr    error
	upsertCall []TierUpsertInput
	upsertErr  error
}

func (f *fakeTierRepo) ListByChannel(_ context.Context, _ uuid.UUID) ([]domain.InnerCircleTierWithCount, error) {
	return f.listResult, f.listErr
}

func (f *fakeTierRepo) UpsertAll(_ context.Context, _ uuid.UUID, items []TierUpsertInput) error {
	f.upsertCall = append([]TierUpsertInput{}, items...)
	return f.upsertErr
}

type fakeChannelLookup struct {
	channel *domain.Channel
	err     error
}

func (f *fakeChannelLookup) GetByID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return f.channel, f.err
}

func validUpsert(tier string) TierUpsertInput {
	return TierUpsertInput{
		TierID:          tier,
		MonthlyUSDCents: 299,
		MonthlySats:     8500,
		Perks:           []string{"perk"},
		Enabled:         true,
	}
}

func TestTierService_List_ChannelNotFound(t *testing.T) {
	svc := NewTierService(&fakeTierRepo{}, &fakeChannelLookup{err: errors.New("nope")})
	_, err := svc.List(context.Background(), uuid.New())
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}

func TestTierService_List_HappyPath(t *testing.T) {
	tier := domain.InnerCircleTierWithCount{InnerCircleTier: domain.InnerCircleTier{TierID: "vip"}, MemberCount: 7}
	svc := NewTierService(
		&fakeTierRepo{listResult: []domain.InnerCircleTierWithCount{tier}},
		&fakeChannelLookup{channel: &domain.Channel{}},
	)
	got, err := svc.List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0].MemberCount != 7 {
		t.Fatalf("unexpected list result: %+v", got)
	}
}

func TestTierService_Update_NonOwner_403(t *testing.T) {
	owner := uuid.New()
	caller := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	err := svc.Update(context.Background(), uuid.New(), caller, []TierUpsertInput{validUpsert("vip")})
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("err = %v, want ErrNotChannelOwner", err)
	}
}

func TestTierService_Update_BadTierID_400(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	err := svc.Update(context.Background(), uuid.New(), owner, []TierUpsertInput{{TierID: "diamond", MonthlyUSDCents: 99, MonthlySats: 100}})
	if err == nil {
		t.Fatalf("expected error for bad tier id")
	}
}

func TestTierService_Update_DuplicateTier_400(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	items := []TierUpsertInput{validUpsert("vip"), validUpsert("vip")}
	err := svc.Update(context.Background(), uuid.New(), owner, items)
	if !errors.Is(err, ErrTooManyTiers) {
		t.Fatalf("err = %v, want ErrTooManyTiers", err)
	}
}

func TestTierService_Update_TooManyTiers(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	items := []TierUpsertInput{
		validUpsert("supporter"),
		validUpsert("vip"),
		validUpsert("elite"),
		validUpsert("supporter"),
	}
	err := svc.Update(context.Background(), uuid.New(), owner, items)
	if !errors.Is(err, ErrTooManyTiers) {
		t.Fatalf("err = %v, want ErrTooManyTiers", err)
	}
}

func TestTierService_Update_PerksTooLong(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	long := make([]byte, maxPerkLength+1)
	for i := range long {
		long[i] = 'a'
	}
	items := []TierUpsertInput{{TierID: "vip", MonthlyUSDCents: 0, MonthlySats: 0, Perks: []string{string(long)}}}
	err := svc.Update(context.Background(), uuid.New(), owner, items)
	if !errors.Is(err, ErrPerksTooLarge) {
		t.Fatalf("err = %v, want ErrPerksTooLarge", err)
	}
}

func TestTierService_Update_TooManyPerks(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	perks := make([]string, maxPerksPerTier+1)
	for i := range perks {
		perks[i] = "p"
	}
	items := []TierUpsertInput{{TierID: "vip", MonthlyUSDCents: 0, MonthlySats: 0, Perks: perks}}
	err := svc.Update(context.Background(), uuid.New(), owner, items)
	if !errors.Is(err, ErrPerksTooLarge) {
		t.Fatalf("err = %v, want ErrPerksTooLarge", err)
	}
}

func TestTierService_Update_NegativePrice(t *testing.T) {
	owner := uuid.New()
	svc := NewTierService(
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
	)
	items := []TierUpsertInput{{TierID: "vip", MonthlyUSDCents: -1, MonthlySats: 0}}
	if err := svc.Update(context.Background(), uuid.New(), owner, items); err == nil {
		t.Fatalf("expected error for negative price")
	}
}

func TestTierService_Update_HappyPath(t *testing.T) {
	owner := uuid.New()
	repo := &fakeTierRepo{}
	svc := NewTierService(repo, &fakeChannelLookup{channel: &domain.Channel{AccountID: owner}})
	items := []TierUpsertInput{validUpsert("supporter"), validUpsert("vip")}
	if err := svc.Update(context.Background(), uuid.New(), owner, items); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(repo.upsertCall) != 2 {
		t.Fatalf("upsert called with %d items, want 2", len(repo.upsertCall))
	}
}

func TestTierService_Update_ChannelNotFound(t *testing.T) {
	svc := NewTierService(&fakeTierRepo{}, &fakeChannelLookup{err: errors.New("nope")})
	err := svc.Update(context.Background(), uuid.New(), uuid.New(), []TierUpsertInput{validUpsert("vip")})
	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}
