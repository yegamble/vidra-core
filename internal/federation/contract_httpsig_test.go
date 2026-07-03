package federation

// Digest/Signature interop contract (fix_plan P10 "federation contract tests
// using fixtures"): a real-shaped fixture body is signed with internal/httpsig
// exactly the way outbound delivery does, and verified through the federation
// service's own ResolveKey (the remote-actor key cache) exactly the way the
// inbox does — proving the two halves of the cavage HTTP-signature scheme
// (Date/Digest/Signature over "(request-target) host date digest") stay
// mutually compatible, byte for byte.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestContractHTTPSigFixtureRoundTrip(t *testing.T) {
	ctx := context.Background()

	// The "remote instance" keypair, minted for this run (never committed).
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	// Its actor sits in the remote-actor cache, as signature verification
	// would have left it.
	repo := newContractRepo()
	shared := "https://peertube.example/inbox"
	repo.remoteActors[ctPTChannel] = sqlcgen.RemoteActor{
		ActorUrl:          ctPTChannel,
		ActorType:         "Group",
		PreferredUsername: "kayak_diaries",
		Domain:            "peertube.example",
		InboxUrl:          ctPTChannel + "/inbox",
		SharedInboxUrl:    &shared,
		PublicKeyPem:      pubPEM,
	}
	svc := NewService(repo, WithBaseURL(contractBase), WithFollowEdgeChecker(edgesFor(ctPTChannel)))

	// Sign the real-shaped Create{Video} fixture exactly like deliverActivity.
	body := readFixture(t, "inbound/peertube_create_video.json")
	req := httptest.NewRequest(http.MethodPost, contractBase+"/inbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/activity+json")
	signer := httpsig.Signer{KeyID: ctPTChannel + "#main-key", Priv: key}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The Digest must be the fediverse-standard SHA-256=<base64(sha256(body))>.
	sum := sha256.Sum256(body)
	if got, want := req.Header.Get("Digest"), "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]); got != want {
		t.Errorf("Digest = %q, want %q", got, want)
	}

	// Verify through the service's own key resolver, like the inbox handler.
	verifier := httpsig.Verifier{ResolveKey: svc.ResolveKey}
	keyID, err := verifier.Verify(ctx, req, body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if keyID != signer.KeyID {
		t.Errorf("verified keyId = %q, want %q", keyID, signer.KeyID)
	}

	// The verified fixture then dispatches end to end: signer → HandleInbox →
	// stored remote video.
	if err := svc.HandleInbox(ctx, ctPTChannel, body); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	if _, ok := repo.remoteVideos[ctPTVideo]; !ok {
		t.Fatalf("signed fixture did not ingest: %+v", repo.remoteVideos)
	}

	// A tampered body must fail the Digest check.
	tampered := bytes.Replace(body, []byte("Whitegull"), []byte("Blackgull"), 1)
	if bytes.Equal(tampered, body) {
		t.Fatal("tamper marker not found in fixture")
	}
	if _, err := verifier.Verify(ctx, req, tampered); err == nil {
		t.Error("Verify accepted a tampered body")
	}

	// A signature from a key the cache does not hold must fail too.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodPost, contractBase+"/inbox", bytes.NewReader(body))
	if err := (httpsig.Signer{KeyID: ctPTChannel + "#main-key", Priv: otherKey}).Sign(req2, body); err != nil {
		t.Fatalf("Sign(other): %v", err)
	}
	if _, err := verifier.Verify(ctx, req2, body); err == nil {
		t.Error("Verify accepted a signature from the wrong private key")
	}
}
