package federation

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// testActorPEM returns a fresh RSA public key in PEM SPKI form + the private key.
func testActorPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), key
}

// actorServer serves a minimal actor document and counts hits.
func actorServer(t *testing.T, pubPEM string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/activity+json")
		_, _ = w.Write([]byte(`{
			"id": "` + "http://" + r.Host + `/accounts/bob",
			"type": "Person",
			"preferredUsername": "bob",
			"inbox": "http://` + r.Host + `/accounts/bob/inbox",
			"endpoints": {"sharedInbox": "http://` + r.Host + `/inbox"},
			"publicKey": {"publicKeyPem": ` + quote(pubPEM) + `}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func newRemoteRepo() fakeRepo {
	return fakeRepo{remoteActors: map[string]sqlcgen.RemoteActor{}}
}

func TestResolveKeyFetchesParsesAndCaches(t *testing.T) {
	pubPEM, key := testActorPEM(t)
	srv, hits := actorServer(t, pubPEM)
	repo := newRemoteRepo()
	// allowPrivateFetch so the guard permits the loopback httptest origin.
	svc := NewService(repo, WithAllowPrivateFetch(true))

	keyID := srv.URL + "/accounts/bob#main-key"
	got, err := svc.ResolveKey(context.Background(), keyID)
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	if got.N.Cmp(key.PublicKey.N) != 0 {
		t.Error("resolved key does not match the served key")
	}
	// Cached: the actor URL (fragment stripped) is stored, and a second resolve
	// does not hit the origin again.
	if _, ok := repo.remoteActors[srv.URL+"/accounts/bob"]; !ok {
		t.Error("remote actor was not cached under its fragment-stripped URL")
	}
	if _, err := svc.ResolveKey(context.Background(), keyID); err != nil {
		t.Fatalf("second ResolveKey: %v", err)
	}
	if *hits != 1 {
		t.Errorf("origin hit %d times, want 1 (second resolve should be cached)", *hits)
	}
}

func TestResolveKeyRejectsActorWithoutKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","type":"Person"}`))
	}))
	t.Cleanup(srv.Close)
	svc := NewService(newRemoteRepo(), WithAllowPrivateFetch(true))
	if _, err := svc.ResolveKey(context.Background(), srv.URL+"/a#main-key"); err == nil {
		t.Error("ResolveKey accepted an actor with no public key")
	}
}

func TestRemoteFetchSSRFGuardedByDefault(t *testing.T) {
	srv, hits := actorServer(t, mustPEM(t))
	// Default (allowPrivateFetch=false): the guard must refuse the loopback origin.
	svc := NewService(newRemoteRepo())
	if _, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/bob#main-key"); err == nil {
		t.Error("ResolveKey reached a loopback origin with the SSRF guard on")
	}
	if *hits != 0 {
		t.Errorf("origin was hit %d times despite the guard", *hits)
	}
}

func TestParseRSAPublicKey(t *testing.T) {
	pubPEM, _ := testActorPEM(t)
	if _, err := parseRSAPublicKey(pubPEM); err != nil {
		t.Errorf("valid PEM: %v", err)
	}
	if _, err := parseRSAPublicKey("not a pem"); err == nil {
		t.Error("garbage PEM accepted")
	}
}

func mustPEM(t *testing.T) string {
	t.Helper()
	p, _ := testActorPEM(t)
	return p
}

// An actor document names its own inboxes, and every Accept, Reject and
// channel fan-out this instance signs is POSTed to them. A document on
// evil.example naming victim.example's inbox would make this instance send
// signed activities to a server that never federated with it, so only
// same-host inboxes are cached. The key is still cached: verifying the
// actor's signatures does not depend on where it receives.
func TestCacheFetchedActorDropsCrossHostInboxes(t *testing.T) {
	pubPEM, _ := testActorPEM(t)
	for name, tc := range map[string]struct {
		inbox, shared string
		wantInbox     bool
		wantShared    bool
	}{
		"both foreign":         {"https://victim.example/inbox", "https://victim.example/shared", false, false},
		"shared inbox foreign": {"", "https://victim.example/shared", true, false},
		"inbox foreign":        {"https://victim.example/inbox", "", false, true},
		"both own host":        {"", "", true, true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				own := "http://" + r.Host
				inbox, shared := tc.inbox, tc.shared
				if inbox == "" {
					inbox = own + "/accounts/bob/inbox"
				}
				if shared == "" {
					shared = own + "/inbox"
				}
				_, _ = w.Write([]byte(`{"id":"` + own + `/accounts/bob","type":"Person",` +
					`"inbox":"` + inbox + `","endpoints":{"sharedInbox":"` + shared + `"},` +
					`"publicKey":{"publicKeyPem":` + quote(pubPEM) + `}}`))
			}))
			t.Cleanup(srv.Close)
			repo := newRemoteRepo()
			svc := NewService(repo, WithAllowPrivateFetch(true))
			if _, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/bob#main-key"); err != nil {
				t.Fatalf("ResolveKey: %v", err)
			}
			ra := repo.remoteActors[srv.URL+"/accounts/bob"]
			if got := ra.InboxUrl != ""; got != tc.wantInbox {
				t.Errorf("inbox cached = %v (%q), want %v", got, ra.InboxUrl, tc.wantInbox)
			}
			if got := ra.SharedInboxUrl != nil; got != tc.wantShared {
				t.Errorf("shared inbox cached = %v, want %v", got, tc.wantShared)
			}
		})
	}
}

// fetchBoundsClock is a settable clock for the actor-fetch gate.
type fetchBoundsClock struct{ t time.Time }

func (c *fetchBoundsClock) now() time.Time { return c.t }

// rotatingActorServer serves an actor document whose key the test can swap,
// and counts every fetch.
func rotatingActorServer(t *testing.T, pem *string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/activity+json")
		_, _ = w.Write([]byte(`{"id":"http://` + r.Host + `/accounts/bob","type":"Person",` +
			`"inbox":"http://` + r.Host + `/accounts/bob/inbox","publicKey":{"publicKeyPem":` + quote(*pem) + `}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func signedInboxRequest(t *testing.T, keyID string, priv *rsa.PrivateKey) (*http.Request, []byte) {
	t.Helper()
	body := []byte(`{"type":"Create"}`)
	req := httptest.NewRequest(http.MethodPost, "https://local.example.test/inbox", nil)
	if err := (httpsig.Signer{KeyID: keyID, Priv: priv}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return req, body
}

// newFetchBoundsService returns a service over repo with a settable clock and
// the same verifier wiring the inbox handler uses.
func newFetchBoundsService(repo fakeRepo) (*Service, *fetchBoundsClock, httpsig.Verifier) {
	svc := NewService(repo, WithAllowPrivateFetch(true))
	clk := &fetchBoundsClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc.actorFetches.now = clk.now
	return svc, clk, httpsig.Verifier{ResolveKey: svc.ResolveKey, RefreshKey: svc.ResolveKeyFresh}
}

// Fifty forged signatures naming one cached actor buy one key re-fetch, not
// fifty: the rotation retry runs at most once per actor per cooldown.
func TestForgedSignaturesAgainstCachedActorBuyOneRefetch(t *testing.T) {
	realPEM, _ := testActorPEM(t)
	_, forger := testActorPEM(t)
	srv, hits := rotatingActorServer(t, &realPEM)
	repo := newRemoteRepo()
	actorURL := srv.URL + "/accounts/bob"
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{ActorUrl: actorURL, PublicKeyPem: realPEM}
	_, _, v := newFetchBoundsService(repo)

	for i := 0; i < 50; i++ {
		req, body := signedInboxRequest(t, actorURL+"#main-key", forger)
		if _, err := v.Verify(context.Background(), req, body); err == nil {
			t.Fatal("a forged signature verified")
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("50 forged requests caused %d outbound fetches, want 1", got)
	}
}

// Fifty requests naming a keyId that does not resolve cause one fetch: the
// failure is remembered for the cooldown, then tried again.
func TestUnresolvableKeyIDIsNegativeCached(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	svc, clk, _ := newFetchBoundsService(newRemoteRepo())

	for i := 0; i < 50; i++ {
		if _, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/ghost#main-key"); err == nil {
			t.Fatal("an unresolvable keyId resolved")
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("50 unresolvable keyIds caused %d outbound fetches, want 1", got)
	}
	clk.t = clk.t.Add(actorFetchCooldown + time.Second)
	_, _ = svc.ResolveKey(context.Background(), srv.URL+"/accounts/ghost#main-key")
	if got := hits.Load(); got != 2 {
		t.Fatalf("after the cooldown: %d fetches, want 2", got)
	}
}

// A blocked host is never fetched — not to resolve a new actor, and not to
// refresh a cached one.
func TestBlockedHostIsNeverFetched(t *testing.T) {
	pemStr, _ := testActorPEM(t)
	srv, hits := rotatingActorServer(t, &pemStr)
	repo := newRemoteRepo()
	repo.blockedDomains = map[string]bool{"127.0.0.1": true}
	cached := srv.URL + "/accounts/cached"
	repo.remoteActors[cached] = sqlcgen.RemoteActor{ActorUrl: cached, PublicKeyPem: pemStr}
	svc, _, _ := newFetchBoundsService(repo)

	if _, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/bob#main-key"); err == nil {
		t.Error("an actor on a blocked host resolved")
	}
	if _, err := svc.ResolveKeyFresh(context.Background(), cached+"#main-key"); err == nil {
		t.Error("a cached actor on a blocked host was refreshed")
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("a blocked host was fetched %d times", got)
	}
}

// A peer that rotates its key is picked up on the first failure, and a second
// rotation inside the cooldown is picked up once the cooldown has passed.
func TestRotatedKeyPickedUpOnFirstFailureAndAfterCooldown(t *testing.T) {
	oldPEM, _ := testActorPEM(t)
	newPEM, newKey := testActorPEM(t)
	servedPEM := newPEM
	srv, hits := rotatingActorServer(t, &servedPEM)
	repo := newRemoteRepo()
	actorURL := srv.URL + "/accounts/bob"
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{ActorUrl: actorURL, PublicKeyPem: oldPEM}
	_, clk, v := newFetchBoundsService(repo)

	req, body := signedInboxRequest(t, actorURL+"#main-key", newKey)
	if _, err := v.Verify(context.Background(), req, body); err != nil {
		t.Fatalf("first request after a rotation: %v", err)
	}

	thirdPEM, thirdKey := testActorPEM(t)
	servedPEM = thirdPEM
	req, body = signedInboxRequest(t, actorURL+"#main-key", thirdKey)
	if _, err := v.Verify(context.Background(), req, body); err == nil {
		t.Fatal("a second rotation inside the cooldown was fetched")
	}
	clk.t = clk.t.Add(actorFetchCooldown + time.Second)
	req, body = signedInboxRequest(t, actorURL+"#main-key", thirdKey)
	if _, err := v.Verify(context.Background(), req, body); err != nil {
		t.Fatalf("rotation after the cooldown: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2", got)
	}
}
