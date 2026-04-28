package inner_circle

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type fakeMembershipRepo struct {
	tiers          map[string]string // (user|channel) → active tier
	listMineResult []domain.InnerCircleMembership
	listByCh       []domain.InnerCircleMembership
	createPendCall struct {
		called    bool
		userID    uuid.UUID
		channelID uuid.UUID
		tierID    string
		btcpay    *uuid.UUID
		polar     *string
	}
	cancelErr   error
	expireRet   [2]int
	expireErr   error
}

func (f *fakeMembershipRepo) GetActiveTier(_ context.Context, _ uuid.UUID, _ uuid.UUID) (string, error) {
	return "", nil
}
func (f *fakeMembershipRepo) ListMine(_ context.Context, _ uuid.UUID, _ bool) ([]domain.InnerCircleMembership, error) {
	return f.listMineResult, nil
}
func (f *fakeMembershipRepo) ListByChannel(_ context.Context, _ uuid.UUID, _, _ int) ([]domain.InnerCircleMembership, error) {
	return f.listByCh, nil
}
func (f *fakeMembershipRepo) CreatePending(_ context.Context, userID, channelID uuid.UUID, tierID string, polar *string, btcpay *uuid.UUID, _ time.Time) (uuid.UUID, error) {
	f.createPendCall.called = true
	f.createPendCall.userID = userID
	f.createPendCall.channelID = channelID
	f.createPendCall.tierID = tierID
	f.createPendCall.btcpay = btcpay
	f.createPendCall.polar = polar
	return uuid.New(), nil
}
func (f *fakeMembershipRepo) Cancel(_ context.Context, _, _ uuid.UUID) error { return f.cancelErr }
func (f *fakeMembershipRepo) ExpireDue(_ context.Context, _ time.Duration) (int, int, error) {
	return f.expireRet[0], f.expireRet[1], f.expireErr
}

type fakeBTCPay struct {
	called  bool
	gotMeta map[string]interface{}
	err     error
}

func (f *fakeBTCPay) CreateInvoice(_ context.Context, _ string, _ int64, _ string, _ string, metadata map[string]interface{}) (*domain.BTCPayInvoice, error) {
	f.called = true
	f.gotMeta = metadata
	if f.err != nil {
		return nil, f.err
	}
	return &domain.BTCPayInvoice{ID: uuid.New().String(), AmountSats: 8500}, nil
}

func newSubscribeFixtures(t *testing.T, btcpay BTCPayInvoiceCreator) (*MembershipService, uuid.UUID, uuid.UUID, *fakeMembershipRepo) {
	t.Helper()
	owner := uuid.New()
	channel := &domain.Channel{AccountID: owner}
	tiers := []domain.InnerCircleTierWithCount{{
		InnerCircleTier: domain.InnerCircleTier{TierID: "vip", MonthlySats: 22750, Enabled: true},
	}}
	memRepo := &fakeMembershipRepo{}
	tierRepo := &fakeTierRepo{listResult: tiers}
	channels := &fakeChannelLookup{channel: channel}
	svc := NewMembershipService(memRepo, tierRepo, channels, btcpay)
	return svc, uuid.New(), uuid.New(), memRepo
}

func TestSubscribeBTCPay_HappyPath_ReturnsBTCPayKind(t *testing.T) {
	btcpay := &fakeBTCPay{}
	svc, userID, channelID, memRepo := newSubscribeFixtures(t, btcpay)

	res, err := svc.SubscribeBTCPay(context.Background(), userID, channelID, "vip")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Kind != "btcpay" {
		t.Fatalf("kind = %q, want btcpay", res.Kind)
	}
	if res.Invoice == nil || res.Invoice.ID == "" {
		t.Fatalf("invoice not populated: %+v", res)
	}
	if !btcpay.called {
		t.Fatalf("BTCPay create not invoked")
	}
	if btcpay.gotMeta["type"] != "inner_circle" {
		t.Fatalf("metadata type = %v, want inner_circle", btcpay.gotMeta["type"])
	}
	if btcpay.gotMeta["channel_id"] != channelID.String() {
		t.Fatalf("metadata channel_id missing or wrong")
	}
	if !memRepo.createPendCall.called {
		t.Fatalf("pending row not created")
	}
}

func TestSubscribeBTCPay_BadTier_400(t *testing.T) {
	svc, userID, channelID, _ := newSubscribeFixtures(t, &fakeBTCPay{})
	_, err := svc.SubscribeBTCPay(context.Background(), userID, channelID, "diamond")
	if err == nil {
		t.Fatalf("expected error for bad tier id")
	}
}

func TestSubscribeBTCPay_DisabledTier_NotFound(t *testing.T) {
	owner := uuid.New()
	channel := &domain.Channel{AccountID: owner}
	tiers := []domain.InnerCircleTierWithCount{{
		InnerCircleTier: domain.InnerCircleTier{TierID: "vip", Enabled: false},
	}}
	memRepo := &fakeMembershipRepo{}
	svc := NewMembershipService(memRepo,
		&fakeTierRepo{listResult: tiers},
		&fakeChannelLookup{channel: channel},
		&fakeBTCPay{},
	)
	_, err := svc.SubscribeBTCPay(context.Background(), uuid.New(), uuid.New(), "vip")
	if !errors.Is(err, ErrTierNotFound) {
		t.Fatalf("err = %v, want ErrTierNotFound", err)
	}
}

func TestSubscribeBTCPay_NoBTCPayService_Error(t *testing.T) {
	svc, userID, channelID, _ := newSubscribeFixtures(t, nil)
	_, err := svc.SubscribeBTCPay(context.Background(), userID, channelID, "vip")
	if err == nil {
		t.Fatalf("expected error when btcpay service is nil")
	}
}

func TestCreatePendingPolar_WritesRow(t *testing.T) {
	memRepo := &fakeMembershipRepo{}
	svc := NewMembershipService(memRepo,
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{}},
		&fakeBTCPay{},
	)
	id, err := svc.CreatePendingPolar(context.Background(), uuid.New(), uuid.New(), "elite", "polar-sess-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id == uuid.Nil {
		t.Fatalf("returned nil ID")
	}
	if !memRepo.createPendCall.called {
		t.Fatalf("CreatePending not called")
	}
	if memRepo.createPendCall.tierID != "elite" {
		t.Fatalf("tier_id passed = %q, want elite", memRepo.createPendCall.tierID)
	}
	if memRepo.createPendCall.polar == nil || *memRepo.createPendCall.polar != "polar-sess-1" {
		t.Fatalf("polar session id not propagated")
	}
}

func TestCreatePendingPolar_BadTier(t *testing.T) {
	svc := NewMembershipService(&fakeMembershipRepo{}, &fakeTierRepo{}, &fakeChannelLookup{channel: &domain.Channel{}}, &fakeBTCPay{})
	if _, err := svc.CreatePendingPolar(context.Background(), uuid.New(), uuid.New(), "diamond", ""); err == nil {
		t.Fatalf("expected error for bad tier id")
	}
}

func TestCancelMine_DelegatesToRepo(t *testing.T) {
	memRepo := &fakeMembershipRepo{}
	svc := NewMembershipService(memRepo, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{})
	if err := svc.CancelMine(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestListByChannel_NonOwner_403(t *testing.T) {
	owner := uuid.New()
	caller := uuid.New()
	svc := NewMembershipService(
		&fakeMembershipRepo{},
		&fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
		&fakeBTCPay{},
	)
	_, err := svc.ListByChannel(context.Background(), uuid.New(), caller, 25, 0)
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("err = %v, want ErrNotChannelOwner", err)
	}
}

func TestListByChannel_OwnerHappy(t *testing.T) {
	owner := uuid.New()
	memRepo := &fakeMembershipRepo{listByCh: []domain.InnerCircleMembership{{}}}
	svc := NewMembershipService(
		memRepo, &fakeTierRepo{},
		&fakeChannelLookup{channel: &domain.Channel{AccountID: owner}},
		&fakeBTCPay{},
	)
	got, err := svc.ListByChannel(context.Background(), uuid.New(), owner, 25, 0)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d members, want 1", len(got))
	}
}

func TestRunExpiry_Delegates(t *testing.T) {
	memRepo := &fakeMembershipRepo{expireRet: [2]int{3, 1}}
	svc := NewMembershipService(memRepo, &fakeTierRepo{}, &fakeChannelLookup{}, &fakeBTCPay{})
	a, p, err := svc.RunExpiry(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if a != 3 || p != 1 {
		t.Fatalf("got (%d, %d), want (3, 1)", a, p)
	}
}
