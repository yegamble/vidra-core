package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// httpsigVerifier returns a verify closure using a fixed public key.
func httpsigVerifier(pub *rsa.PublicKey) func(*http.Request, []byte) (string, error) {
	v := httpsig.Verifier{ResolveKey: func(context.Context, string) (*rsa.PublicKey, error) { return pub, nil }}
	return func(r *http.Request, body []byte) (string, error) {
		return v.Verify(r.Context(), r, body)
	}
}

func TestSendAcceptFollowSignsAndDelivers(t *testing.T) {
	channelID := uuid.New()
	follower := "https://remote.example/accounts/bob"
	followID := "https://remote.example/activities/follow/1"

	repo := fakeRepo{
		channels:     map[string]sqlcgen.Channel{"films": {ID: channelID, Handle: "films"}},
		chanKeys:     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		remoteActors: map[string]sqlcgen.RemoteActor{},
	}

	// The follower's inbox verifies the signed Accept it receives.
	var (
		gotBody     []byte
		verifiedKey string
		verifyErr   error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.chanKeys[channelID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse minted channel key: %v", err)
		}
		v := httpsigVerifier(pub)
		verifiedKey, verifyErr = v(r, gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	repo.remoteActors[follower] = sqlcgen.RemoteActor{ActorUrl: follower, InboxUrl: srv.URL}
	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))

	if err := svc.SendAcceptFollow(context.Background(), channelID, "films", follower, followID); err != nil {
		t.Fatalf("SendAcceptFollow: %v", err)
	}
	if verifyErr != nil {
		t.Fatalf("inbox could not verify the Accept signature: %v", verifyErr)
	}
	if want := "https://videos.example/video-channels/films#main-key"; verifiedKey != want {
		t.Errorf("signed by %q, want %q", verifiedKey, want)
	}

	var accept struct {
		Type   string `json:"type"`
		Actor  string `json:"actor"`
		Object struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"object"`
	}
	if err := json.Unmarshal(gotBody, &accept); err != nil {
		t.Fatalf("unmarshal Accept: %v", err)
	}
	if accept.Type != "Accept" || accept.Actor != "https://videos.example/video-channels/films" {
		t.Errorf("accept = %+v", accept)
	}
	if accept.Object.Type != "Follow" || accept.Object.ID != followID {
		t.Errorf("accept.object = %+v, want the Follow %q", accept.Object, followID)
	}
}

func TestUnlockPrivateKeyRawAndSealed(t *testing.T) {
	priv, rawPEM := testPrivatePEM(t)

	// Raw (dev, no cipher).
	got, err := (&Service{}).unlockPrivateKey(rawPEM)
	if err != nil {
		t.Fatalf("unlock raw: %v", err)
	}
	if got.N.Cmp(priv.N) != 0 {
		t.Error("unlocked raw key differs from the original")
	}

	// Sealed (needs the cipher).
	cipher, err := secretbox.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	sealed, err := cipher.Seal([]byte(rawPEM))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	svc := NewService(fakeRepo{}, WithCipher(cipher))
	got2, err := svc.unlockPrivateKey(sealed)
	if err != nil {
		t.Fatalf("unlock sealed: %v", err)
	}
	if got2.N.Cmp(priv.N) != 0 {
		t.Error("unlocked sealed key differs from the original")
	}

	// Sealed value but no cipher → error (never silently mishandle a secret).
	if _, err := (&Service{}).unlockPrivateKey(sealed); err == nil {
		t.Error("unlock of a sealed key without a cipher must fail")
	}
}

func TestDrainDeliveriesSignsDeliversAndMarks(t *testing.T) {
	channelID := uuid.New()
	repo := fakeRepo{
		chanKeys:   map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	var verifyErr error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.chanKeys[channelID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse channel key: %v", err)
		}
		_, verifyErr = httpsigVerifier(pub)(r, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if err := svc.enqueueChannelDelivery(context.Background(), channelID, "films", srv.URL, []byte(`{"type":"Accept"}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if verifyErr != nil {
		t.Errorf("inbox could not verify the delivered signature: %v", verifyErr)
	}
	for _, d := range repo.deliveries {
		if d.state != "delivered" {
			t.Errorf("delivery state = %q, want delivered", d.state)
		}
	}
}

func TestDrainDeliveriesSignsAsAccountActor(t *testing.T) {
	userID := uuid.New()
	repo := fakeRepo{
		acctKeys:   map[uuid.UUID]sqlcgen.GetAccountActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	var (
		verifiedKey string
		verifyErr   error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pub, err := parseRSAPublicKey(repo.acctKeys[userID].PublicKeyPem)
		if err != nil {
			t.Errorf("parse minted account key: %v", err)
		}
		verifiedKey, verifyErr = httpsigVerifier(pub)(r, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	// Mint the account key first (as FollowRemoteChannel does before enqueue).
	if _, err := svc.ensureAccountKey(context.Background(), userID); err != nil {
		t.Fatalf("ensureAccountKey: %v", err)
	}
	if err := svc.enqueueAccountDelivery(context.Background(), userID, "ada", srv.URL, []byte(`{"type":"Follow"}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered = %d, want 1", n)
	}
	if verifyErr != nil {
		t.Fatalf("inbox could not verify the account-actor signature: %v", verifyErr)
	}
	if want := "https://videos.example/accounts/ada#main-key"; verifiedKey != want {
		t.Errorf("signed by %q, want %q", verifiedKey, want)
	}
}

func TestDrainDeliveriesReschedulesOnFailure(t *testing.T) {
	channelID := uuid.New()
	repo := fakeRepo{
		chanKeys:   map[uuid.UUID]sqlcgen.GetChannelActorKeyRow{},
		deliveries: map[uuid.UUID]*fakeDelivery{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	svc := NewService(repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	if err := svc.enqueueChannelDelivery(context.Background(), channelID, "films", srv.URL, []byte(`{}`)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainDeliveries(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDeliveries: %v", err)
	}
	if n != 0 {
		t.Fatalf("delivered = %d, want 0 (target 500s)", n)
	}
	for _, d := range repo.deliveries {
		if d.state != "pending" {
			t.Errorf("state = %q, want pending (rescheduled, not failed before the cap)", d.state)
		}
		if d.row.Attempts != 1 {
			t.Errorf("attempts = %d, want 1", d.row.Attempts)
		}
		if !d.nextAttempt.After(time.Now()) {
			t.Error("next attempt should be scheduled in the future (backoff)")
		}
	}
}

func testPrivatePEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}
