package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakePolarMembershipRepo struct {
	upsertCount    int
	upsertSubID    string
	upsertExpires  time.Time
	upsertTier     string
	cancelCount    int
	cancelSubID    string
	cancelStatus   string
	cancelErr      error
	upsertErr      error
}

func (f *fakePolarMembershipRepo) UpsertActiveByPolar(_ context.Context, _ uuid.UUID, _ uuid.UUID, tierID, polarSubscriptionID string, expiresAt time.Time) (uuid.UUID, error) {
	f.upsertCount++
	f.upsertSubID = polarSubscriptionID
	f.upsertExpires = expiresAt
	f.upsertTier = tierID
	if f.upsertErr != nil {
		return uuid.Nil, f.upsertErr
	}
	return uuid.New(), nil
}

func (f *fakePolarMembershipRepo) SetPolarStatus(_ context.Context, polarSubscriptionID, status string) error {
	f.cancelCount++
	f.cancelSubID = polarSubscriptionID
	f.cancelStatus = status
	return f.cancelErr
}

func makeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature_BadSecretEmpty_Rejects(t *testing.T) {
	svc := NewPolarWebhookService("", &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifySignature([]byte("body"), "sha256=anything"); !errors.Is(err, ErrPolarBadSignature) {
		t.Fatalf("want ErrPolarBadSignature, got %v", err)
	}
}

func TestVerifySignature_HappyPath_AcceptsBothForms(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	secret := "shh"
	sig := makeSignature(secret, body)
	svc := NewPolarWebhookService(secret, &fakePolarMembershipRepo{}, nil)

	if err := svc.VerifySignature(body, sig); err != nil {
		t.Fatalf("bare hex form rejected: %v", err)
	}
	if err := svc.VerifySignature(body, "sha256="+sig); err != nil {
		t.Fatalf("sha256= form rejected: %v", err)
	}
}

func TestVerifySignature_BadSig_Rejects(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	svc := NewPolarWebhookService("shh", &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifySignature(body, "sha256=deadbeef"); !errors.Is(err, ErrPolarBadSignature) {
		t.Fatalf("want ErrPolarBadSignature, got %v", err)
	}
}

func TestHandle_SubscriptionCreated_Activates(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	period := time.Now().Add(30 * 24 * time.Hour).UTC()
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.created",
		SubscriptionID: "polar-sub-1",
		Metadata: map[string]string{
			"channel_id": uuid.New().String(),
			"tier_id":    "vip",
			"user_id":    uuid.New().String(),
		},
		CurrentPeriodEnd: &period,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if repo.upsertCount != 1 {
		t.Fatalf("upsert count = %d, want 1", repo.upsertCount)
	}
	if repo.upsertSubID != "polar-sub-1" {
		t.Fatalf("subscription_id = %q", repo.upsertSubID)
	}
	if repo.upsertTier != "vip" {
		t.Fatalf("tier = %q", repo.upsertTier)
	}
	// Expires should be period + 24h
	if !repo.upsertExpires.After(period) {
		t.Fatalf("expires_at must be > period_end (24h grace)")
	}
}

func TestHandle_SubscriptionUpdated_RefreshesExpires(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	period1 := time.Now().Add(15 * 24 * time.Hour).UTC()
	period2 := period1.Add(30 * 24 * time.Hour)
	subID := "polar-sub-1"
	channelID := uuid.New().String()
	userID := uuid.New().String()

	for _, p := range []time.Time{period1, period2} {
		pp := p
		err := svc.Handle(context.Background(), PolarWebhookEvent{
			EventID:        "evt-" + p.Format(time.RFC3339),
			EventType:      "subscription.updated",
			SubscriptionID: subID,
			Metadata:       map[string]string{"channel_id": channelID, "tier_id": "supporter", "user_id": userID},
			CurrentPeriodEnd: &pp,
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	}
	if repo.upsertCount != 2 {
		t.Fatalf("upsert count = %d, want 2", repo.upsertCount)
	}
}

func TestHandle_SubscriptionCanceled(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-cancel",
		EventType:      "subscription.canceled",
		SubscriptionID: "polar-sub-1",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if repo.cancelCount != 1 {
		t.Fatalf("cancel count = %d, want 1", repo.cancelCount)
	}
	if repo.cancelStatus != "cancelled" {
		t.Fatalf("status = %q, want cancelled", repo.cancelStatus)
	}
}

func TestHandle_MissingMetadataAndCustomer_422(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.created",
		SubscriptionID: "polar-sub-1",
		Metadata:       map[string]string{},
	})
	if !errors.Is(err, ErrPolarMissingUser) {
		t.Fatalf("err = %v, want ErrPolarMissingUser", err)
	}
	if repo.upsertCount != 0 {
		t.Fatalf("upsert must not be called when user unresolvable")
	}
}

func TestHandle_FallsBackToExternalCustomerID(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	customerID := uuid.New().String()
	period := time.Now().Add(30 * 24 * time.Hour)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:            "evt-1",
		EventType:          "subscription.created",
		SubscriptionID:     "polar-sub-1",
		ExternalCustomerID: customerID,
		Metadata:           map[string]string{"channel_id": uuid.New().String(), "tier_id": "supporter"},
		CurrentPeriodEnd:   &period,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if repo.upsertCount != 1 {
		t.Fatalf("upsert not called via external_customer_id fallback")
	}
}

func TestHandle_MissingChannel(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	period := time.Now().Add(30 * 24 * time.Hour)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.created",
		SubscriptionID: "polar-sub-1",
		Metadata: map[string]string{
			"tier_id": "supporter",
			"user_id": uuid.New().String(),
		},
		CurrentPeriodEnd: &period,
	})
	if !errors.Is(err, ErrPolarMissingChannel) {
		t.Fatalf("err = %v, want ErrPolarMissingChannel", err)
	}
}

func TestHandle_BadTier(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	period := time.Now().Add(30 * 24 * time.Hour)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.created",
		SubscriptionID: "polar-sub-1",
		Metadata: map[string]string{
			"channel_id": uuid.New().String(),
			"tier_id":    "diamond",
			"user_id":    uuid.New().String(),
		},
		CurrentPeriodEnd: &period,
	})
	if !errors.Is(err, ErrPolarBadTier) {
		t.Fatalf("err = %v, want ErrPolarBadTier", err)
	}
}

func TestHandle_PeriodEndMissing_FallsBackTo30d(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.created",
		SubscriptionID: "polar-sub-1",
		Metadata: map[string]string{
			"channel_id": uuid.New().String(),
			"tier_id":    "supporter",
			"user_id":    uuid.New().String(),
		},
		CurrentPeriodEnd: nil,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if repo.upsertCount != 1 {
		t.Fatalf("upsert not invoked under period_end fallback")
	}
	expected := time.Now().Add(30 * 24 * time.Hour)
	delta := expected.Sub(repo.upsertExpires)
	if delta < -time.Minute || delta > time.Minute {
		t.Fatalf("expires_at = %v, want ~30d from now", repo.upsertExpires)
	}
}

func TestHandle_UnknownEventType_Ignored(t *testing.T) {
	repo := &fakePolarMembershipRepo{}
	svc := NewPolarWebhookService("shh", repo, nil)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:   "evt-1",
		EventType: "subscription.gibberish",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if repo.upsertCount != 0 || repo.cancelCount != 0 {
		t.Fatalf("unknown event type should not call repo")
	}
}

func TestHandle_CancelOfUnknownSubscription_NoError(t *testing.T) {
	repo := &fakePolarMembershipRepo{cancelErr: errors.New("not found")}
	svc := NewPolarWebhookService("shh", repo, nil)
	err := svc.Handle(context.Background(), PolarWebhookEvent{
		EventID:        "evt-1",
		EventType:      "subscription.canceled",
		SubscriptionID: "unknown-sub",
	})
	if err != nil {
		t.Fatalf("expected nil for unknown-subscription cancel, got %v", err)
	}
}

func makeStandardSig(secret, id, ts string, body []byte) string {
	signed := id + "." + ts + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyStandardWebhook_HappyPath(t *testing.T) {
	secret := "shh"
	body := []byte(`{"hello":"world"}`)
	id := "evt_xyz"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := makeStandardSig(secret, id, ts, body)

	svc := NewPolarWebhookService(secret, &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifyStandardWebhook(id, ts, sig, body); err != nil {
		t.Fatalf("expected verify success, got %v", err)
	}
}

func TestVerifyStandardWebhook_StaleTimestamp_Rejects(t *testing.T) {
	secret := "shh"
	body := []byte(`{}`)
	id := "evt_xyz"
	stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	sig := makeStandardSig(secret, id, stale, body)

	svc := NewPolarWebhookService(secret, &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifyStandardWebhook(id, stale, sig, body); !errors.Is(err, ErrPolarBadSignature) {
		t.Fatalf("expected ErrPolarBadSignature for stale timestamp, got %v", err)
	}
}

func TestVerifyStandardWebhook_BadSignature_Rejects(t *testing.T) {
	secret := "shh"
	body := []byte(`{}`)
	id := "evt_xyz"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	svc := NewPolarWebhookService(secret, &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifyStandardWebhook(id, ts, "v1,deadbeefdeadbeef", body); !errors.Is(err, ErrPolarBadSignature) {
		t.Fatalf("expected ErrPolarBadSignature, got %v", err)
	}
}

func TestVerifyStandardWebhook_AcceptsMultipleSignatureEntries(t *testing.T) {
	secret := "shh"
	body := []byte(`{}`)
	id := "evt_xyz"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	correct := makeStandardSig(secret, id, ts, body)
	combined := "v1,wrongbase64== " + correct
	svc := NewPolarWebhookService(secret, &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifyStandardWebhook(id, ts, combined, body); err != nil {
		t.Fatalf("space-separated multi-sig should accept first matching entry: %v", err)
	}
}

func TestVerifyStandardWebhook_EmptyArgs_Rejects(t *testing.T) {
	svc := NewPolarWebhookService("shh", &fakePolarMembershipRepo{}, nil)
	if err := svc.VerifyStandardWebhook("", "0", "v1,sig", []byte("body")); !errors.Is(err, ErrPolarBadSignature) {
		t.Fatalf("expected ErrPolarBadSignature for empty id")
	}
}

// Compile-time guard.
var _ PolarMembershipUpserter = (*fakePolarMembershipRepo)(nil)
