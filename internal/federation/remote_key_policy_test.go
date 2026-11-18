package federation

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// outOfPolicyPEM encodes an RSA public key of the given modulus size and
// exponent. The modulus is not a real one; the policy judges shape only.
func outOfPolicyPEM(t *testing.T, bits, e int) string {
	t.Helper()
	n := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	n.Or(n, big.NewInt(1))
	der, err := x509.MarshalPKIXPublicKey(&rsa.PublicKey{N: n, E: e})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func outOfPolicyKeys(t *testing.T) map[string]string {
	return map[string]string{
		"oversize modulus":  outOfPolicyPEM(t, 8200, 65537),
		"undersize modulus": outOfPolicyPEM(t, 1024, 65537),
		"absurd exponent":   outOfPolicyPEM(t, 2048, 1<<40+1),
	}
}

func TestParseRSAPublicKeyRefusesOutOfPolicyKeys(t *testing.T) {
	for name, p := range outOfPolicyKeys(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRSAPublicKey(p); !errors.Is(err, httpsig.ErrKeyPolicy) {
				t.Fatalf("parseRSAPublicKey = %v, want ErrKeyPolicy", err)
			}
		})
	}
}

// An actor document carrying an out-of-policy key is refused at resolution
// and never cached, so it cannot become a usable actor key.
func TestResolveKeyRefusesAndDoesNotCacheOutOfPolicyKey(t *testing.T) {
	for name, p := range outOfPolicyKeys(t) {
		t.Run(name, func(t *testing.T) {
			srv, _ := actorServer(t, p)
			repo := newRemoteRepo()
			svc := NewService(repo, WithAllowPrivateFetch(true))
			if _, err := svc.ResolveKey(context.Background(), srv.URL+"/accounts/bob#main-key"); err == nil {
				t.Fatal("ResolveKey accepted an out-of-policy key")
			}
			if _, ok := repo.remoteActors[srv.URL+"/accounts/bob"]; ok {
				t.Fatal("an actor with an out-of-policy key was cached")
			}
		})
	}
}

// A row cached before the policy existed may hold an out-of-policy key. It is
// refused like any other, and the peer recovers through the rotation retry
// once it publishes a sane key: a valid signature by the new key verifies.
func TestVerifyRecoversFromCachedOutOfPolicyKey(t *testing.T) {
	goodPEM, priv := testActorPEM(t)
	srv, _ := actorServer(t, goodPEM)
	repo := newRemoteRepo()
	actorURL := srv.URL + "/accounts/bob"
	repo.remoteActors[actorURL] = sqlcgen.RemoteActor{
		ActorUrl:     actorURL,
		InboxUrl:     actorURL + "/inbox",
		PublicKeyPem: outOfPolicyPEM(t, 1024, 65537),
	}
	svc := NewService(repo, WithAllowPrivateFetch(true))

	body := []byte(`{"type":"Create"}`)
	req := httptest.NewRequest(http.MethodPost, "https://local.example.test/inbox", nil)
	if err := (httpsig.Signer{KeyID: actorURL + "#main-key", Priv: priv}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	v := httpsig.Verifier{ResolveKey: svc.ResolveKey, RefreshKey: svc.ResolveKeyFresh}
	if _, err := v.Verify(context.Background(), req, body); err != nil {
		t.Fatalf("Verify after the peer published a sane key: %v", err)
	}
}
