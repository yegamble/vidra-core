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
	"testing"

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
