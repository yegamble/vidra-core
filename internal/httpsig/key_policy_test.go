package httpsig

import (
	"context"
	"crypto"
	"crypto/rsa"
	"errors"
	"math/big"
	"testing"
)

// fakePublicKey builds an RSA public key with a modulus of exactly bits bits.
// It is not a real modulus — the policy judges size and exponent, never
// primality — which keeps the oversize case free of an 8k-bit key generation.
func fakePublicKey(bits, e int) *rsa.PublicKey {
	n := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	n.Or(n, big.NewInt(1))
	return &rsa.PublicKey{N: n, E: e}
}

func TestCheckKeyPolicy(t *testing.T) {
	for name, tc := range map[string]struct {
		pub *rsa.PublicKey
		ok  bool
	}{
		"2048 / 65537":         {fakePublicKey(2048, 65537), true},
		"4096 / 65537":         {fakePublicKey(4096, 65537), true},
		"8192 / 3":             {fakePublicKey(8192, 3), true},
		"nil key":              {nil, false},
		"undersize 1024":       {fakePublicKey(1024, 65537), false},
		"oversize 8200":        {fakePublicKey(8200, 65537), false},
		"oversize 16384":       {fakePublicKey(16384, 65537), false},
		"exponent 1":           {fakePublicKey(2048, 1), false},
		"even exponent":        {fakePublicKey(2048, 65536), false},
		"exponent over 31 bit": {fakePublicKey(2048, 1<<40+1), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := CheckKeyPolicy(tc.pub)
			if tc.ok && err != nil {
				t.Fatalf("CheckKeyPolicy refused an in-policy key: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrKeyPolicy) {
				t.Fatalf("CheckKeyPolicy = %v, want ErrKeyPolicy", err)
			}
		})
	}
}

// An out-of-policy key handed back by a resolver must end in the ordinary
// signature failure WITHOUT the RSA verification ever running on it — whether
// it came from ResolveKey or from the rotation retry.
func TestVerifyNeverRunsOnOutOfPolicyKey(t *testing.T) {
	calls := 0
	orig := verifyPKCS1v15
	verifyPKCS1v15 = func(pub *rsa.PublicKey, h crypto.Hash, hashed, sig []byte) error {
		calls++
		return orig(pub, h, hashed, sig)
	}
	t.Cleanup(func() { verifyPKCS1v15 = orig })

	key := testKey(t)
	body := []byte(`{"type":"Create"}`)
	for name, bad := range map[string]*rsa.PublicKey{
		"oversize":        fakePublicKey(8200, 65537),
		"undersize":       fakePublicKey(1024, 65537),
		"absurd exponent": fakePublicKey(2048, 1<<40+1),
	} {
		t.Run(name, func(t *testing.T) {
			calls = 0
			req := newReq(t, body)
			if err := (Signer{KeyID: "https://peer.example.test/a#main-key", Priv: key}).Sign(req, body); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			v := Verifier{ResolveKey: resolver(bad), RefreshKey: resolver(bad)}
			_, err := v.Verify(context.Background(), req, body)
			if !errors.Is(err, errSignatureFailed) {
				t.Fatalf("Verify = %v, want the ordinary signature failure", err)
			}
			if calls != 0 {
				t.Fatalf("RSA verification ran %d times on an out-of-policy key", calls)
			}
		})
	}
}
