package drm

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// newTestClearKey builds the provider over a fake store, with a deterministic
// entropy source so a minted key is a value the test can assert on.
func newTestClearKey(t *testing.T, repo Repository, entropy []byte) *ClearKey {
	t.Helper()
	p, err := New(Config{Provider: ProviderClearKeyTest, KeyKEK: testKEK, Repo: repo})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ck := p.(*ClearKey)
	if entropy != nil {
		ck.rand = bytes.NewReader(entropy)
	}
	return ck
}

// TestPrepareAssetMintsSealsAndIsIdempotent is the key-lifecycle contract.
//
// The idempotence half is the one that matters in production: a second
// PrepareAsset that minted a NEW key would leave the segments already encrypted
// under the old one undecryptable, with no error anywhere, and no way to
// recover the original key.
func TestPrepareAssetMintsSealsAndIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	want := bytes.Repeat([]byte{0xAB}, ContentKeyLen)
	ck := newTestClearKey(t, repo, want)
	video := uuid.New()

	first, err := ck.PrepareAsset(context.Background(), video)
	if err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	if len(first.Key) != ContentKeyLen {
		t.Fatalf("minted key is %d bytes, want %d (CENC defines AES-128 only)", len(first.Key), ContentKeyLen)
	}
	if !bytes.Equal(first.Key, want) {
		t.Fatalf("minted key did not come from the configured entropy source")
	}
	if first.KeyID == uuid.Nil {
		t.Fatal("minted a nil key id")
	}

	second, err := ck.PrepareAsset(context.Background(), video)
	if err != nil {
		t.Fatalf("second PrepareAsset: %v", err)
	}
	if second.KeyID != first.KeyID || !bytes.Equal(second.Key, first.Key) {
		t.Fatalf("PrepareAsset is not idempotent: second call returned a different key, which would orphan everything encrypted under the first")
	}
	if repo.inserts != 1 {
		t.Errorf("inserts = %d, want 1 — the second call must not write", repo.inserts)
	}
}

// TestPrepareAssetStoresOnlySealedKeyMaterial is §10's doctrine, checked against
// the bytes that actually reach the store: the plaintext key must not be
// recoverable from any stored column without the KEK.
func TestPrepareAssetStoresOnlySealedKeyMaterial(t *testing.T) {
	repo := newFakeRepo()
	plaintext := bytes.Repeat([]byte{0x5C}, ContentKeyLen)
	ck := newTestClearKey(t, repo, plaintext)
	video := uuid.New()

	if _, err := ck.PrepareAsset(context.Background(), video); err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	stored := repo.rows[video].ContentKeySealed
	if !strings.HasPrefix(stored, "enc:") {
		t.Fatalf("stored value %q is not sealed — an unprefixed value is a plaintext key in the database", stored)
	}
	// The plaintext must not survive in the stored blob under any of the
	// encodings a key could plausibly leak through.
	for name, needle := range map[string]string{
		"raw":       string(plaintext),
		"std b64":   base64.StdEncoding.EncodeToString(plaintext),
		"url b64":   base64.RawURLEncoding.EncodeToString(plaintext),
		"hex-ish":   strings.ToUpper(base64.StdEncoding.EncodeToString(plaintext)),
		"truncated": base64.StdEncoding.EncodeToString(plaintext)[:8],
	} {
		if strings.Contains(stored, needle) {
			t.Errorf("the %s encoding of the content key appears in the stored value", name)
		}
	}
}

// TestPrepareAssetLosesARaceToTheStoredKey. Two packagers can call PrepareAsset
// for the same video at once. The insert is ON CONFLICT DO NOTHING, so the
// loser's row is never written — and if it returned the key it minted anyway, it
// would encrypt segments under a key the license endpoint will never serve. The
// read-back is what prevents that, and this is the test that would catch its
// removal.
func TestPrepareAssetLosesARaceToTheStoredKey(t *testing.T) {
	repo := newFakeRepo()
	video := uuid.New()
	winnerKey := bytes.Repeat([]byte{0x11}, ContentKeyLen)
	winnerKID := uuid.New()

	// The winner commits between our read (which saw nothing) and our insert,
	// so our insert is the one that does nothing.
	repo.onInsert = func(r *fakeRepo) {
		if _, exists := r.rows[video]; !exists {
			r.rows[video] = fakeRow(t, video, winnerKID, winnerKey)
		}
	}
	ck := newTestClearKey(t, repo, bytes.Repeat([]byte{0x22}, ContentKeyLen))

	got, err := ck.PrepareAsset(context.Background(), video)
	if err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	if got.KeyID != winnerKID || !bytes.Equal(got.Key, winnerKey) {
		t.Fatalf("PrepareAsset returned the key it minted rather than the one that is stored; segments encrypted with it could never be licensed")
	}
}

// fakeRow builds a stored row the way the production path would have.
func fakeRow(t *testing.T, video, kid uuid.UUID, key []byte) sqlcgenRow {
	t.Helper()
	return sqlcgenRow{VideoID: video, KeyID: kid, ContentKeySealed: seal(t, testKEK, key)}
}

// TestProtectionAndLicenseFollowTheKeyRow. Selecting a provider does not make an
// existing library encrypted; only a video that has actually been through
// PrepareAsset is protected. That is what keeps switching the provider on from
// breaking playback of everything already published — and it is why this slice
// is inert on every install.
func TestProtectionAndLicenseFollowTheKeyRow(t *testing.T) {
	repo := newFakeRepo()
	ck := newTestClearKey(t, repo, bytes.Repeat([]byte{0x33}, ContentKeyLen))
	video := uuid.New()

	prot, err := ck.GetProtectionMetadata(context.Background(), video)
	if err != nil || prot != nil {
		t.Fatalf("before PrepareAsset: protection = (%v, %v), want (nil, nil) — an unpackaged video is clear", prot, err)
	}
	lic, err := ck.LicenseConfiguration(context.Background(), video, uuid.New())
	if err != nil || lic != nil {
		t.Fatalf("before PrepareAsset: license = (%v, %v), want (nil, nil)", lic, err)
	}

	asset, err := ck.PrepareAsset(context.Background(), video)
	if err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}

	prot, err = ck.GetProtectionMetadata(context.Background(), video)
	if err != nil {
		t.Fatalf("protection after PrepareAsset: %v", err)
	}
	if prot == nil {
		t.Fatal("a packaged video reports no protection")
	}
	if prot.Scheme != SchemeCENC || prot.KeyID != asset.KeyID {
		t.Errorf("protection = %+v, want scheme %q and key id %s", prot, SchemeCENC, asset.KeyID)
	}

	lic, err = ck.LicenseConfiguration(context.Background(), video, uuid.New())
	if err != nil {
		t.Fatalf("license after PrepareAsset: %v", err)
	}
	if lic == nil || len(lic.KeySystems) != 1 {
		t.Fatalf("license configuration = %+v, want exactly one key system", lic)
	}
	if lic.KeySystems[0].KeySystem != KeySystemClearKey {
		t.Errorf("key system = %q, want %q", lic.KeySystems[0].KeySystem, KeySystemClearKey)
	}
	if got, want := lic.KeySystems[0].LicenseURL, ClearKeyLicensePath(video); got != want {
		t.Errorf("license URL = %q, want %q", got, want)
	}
}

// TestClearKeyLicenseWireFormat is the EME contract, and the encoding half of it
// is the single most common way a hand-written ClearKey endpoint fails while
// looking correct: base64url WITH padding is rejected by browsers' CDMs.
func TestClearKeyLicenseWireFormat(t *testing.T) {
	repo := newFakeRepo()
	key := bytes.Repeat([]byte{0x7E}, ContentKeyLen)
	ck := newTestClearKey(t, repo, key)
	video := uuid.New()

	asset, err := ck.PrepareAsset(context.Background(), video)
	if err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	kid := base64.RawURLEncoding.EncodeToString(asset.KeyID[:])

	license, err := ck.IssueClearKeyLicense(context.Background(), video, []string{kid})
	if err != nil {
		t.Fatalf("IssueClearKeyLicense: %v", err)
	}
	if license.Type != LicenseTypeTemporary {
		t.Errorf("type = %q, want %q", license.Type, LicenseTypeTemporary)
	}
	if len(license.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(license.Keys))
	}
	jwk := license.Keys[0]
	if jwk.KTY != "oct" {
		t.Errorf("kty = %q, want oct", jwk.KTY)
	}
	for name, v := range map[string]string{"kid": jwk.KID, "k": jwk.K} {
		if strings.ContainsAny(v, "=+/") {
			t.Errorf("%s = %q is not base64url-unpadded; a CDM rejects padded and standard-alphabet values", name, v)
		}
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("%s does not decode as base64url: %v", name, err)
		}
		if len(raw) != ContentKeyLen {
			t.Errorf("%s decodes to %d bytes, want %d", name, len(raw), ContentKeyLen)
		}
	}
	if jwk.KID != kid {
		t.Errorf("kid = %q, want the requested %q", jwk.KID, kid)
	}
	gotKey, _ := base64.RawURLEncoding.DecodeString(jwk.K)
	if !bytes.Equal(gotKey, key) {
		t.Error("k is not the stored content key")
	}
}

// TestClearKeyLicenseRefusals. A license endpoint that answered any KID it was
// handed would turn one authorised viewer into a key oracle for the library.
func TestClearKeyLicenseRefusals(t *testing.T) {
	repo := newFakeRepo()
	ck := newTestClearKey(t, repo, bytes.Repeat([]byte{0x44}, ContentKeyLen))

	unpackaged := uuid.New()
	if _, err := ck.IssueClearKeyLicense(context.Background(), unpackaged, []string{"anything"}); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("license for a video with no keys: err = %v, want ErrNoKeys", err)
	}

	video := uuid.New()
	if _, err := ck.PrepareAsset(context.Background(), video); err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	// Another video's KID, correctly encoded — the interesting case, because it
	// is well-formed and simply not this video's.
	foreign := base64.RawURLEncoding.EncodeToString(func() []byte { u := uuid.New(); return u[:] }())
	if _, err := ck.IssueClearKeyLicense(context.Background(), video, []string{foreign}); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("license for a foreign key id: err = %v, want ErrUnknownKeyID", err)
	}
	for _, junk := range []string{"", "not base64url!!", "AAAA"} {
		if _, err := ck.IssueClearKeyLicense(context.Background(), video, []string{junk}); !errors.Is(err, ErrUnknownKeyID) {
			t.Errorf("license for %q: err = %v, want ErrUnknownKeyID", junk, err)
		}
	}
}

// TestWrongKEKIsADistinguishableFailure. A rotated or mistyped DRM_KEY_KEK must
// not look like "this video is not protected": that would tell a player to play
// encrypted bytes in the clear. It must also not put the sealed value into the
// error, which is where an operator's logs would end up holding it.
func TestWrongKEKIsADistinguishableFailure(t *testing.T) {
	repo := newFakeRepo()
	video := uuid.New()
	sealedUnderAnotherKey := seal(t, testOtherKEK, bytes.Repeat([]byte{0x99}, ContentKeyLen))
	repo.rows[video] = sqlcgenRow{VideoID: video, KeyID: uuid.New(), ContentKeySealed: sealedUnderAnotherKey}

	ck := newTestClearKey(t, repo, nil)

	_, err := ck.IssueClearKeyLicense(context.Background(), video, []string{"x"})
	if !errors.Is(err, ErrKeyMaterial) {
		t.Fatalf("err = %v, want ErrKeyMaterial", err)
	}
	if strings.Contains(err.Error(), sealedUnderAnotherKey) {
		t.Error("the sealed content key appears in the error")
	}
	// The PUBLIC read paths do not open the key at all, so they keep working:
	// a manifest still knows which KID the media names.
	if prot, perr := ck.GetProtectionMetadata(context.Background(), video); perr != nil || prot == nil {
		t.Errorf("GetProtectionMetadata = (%v, %v); reading the public KID must not require opening the sealed key", prot, perr)
	}
}

// TestPrepareAssetSurfacesAnInsertFailure. A key that was minted but not stored
// must never be handed to a packager: it would encrypt an asset nothing can
// ever license.
func TestPrepareAssetSurfacesAnInsertFailure(t *testing.T) {
	repo := newFakeRepo()
	repo.insertErr = errBoom
	ck := newTestClearKey(t, repo, bytes.Repeat([]byte{0x55}, ContentKeyLen))

	if _, err := ck.PrepareAsset(context.Background(), uuid.New()); err == nil {
		t.Fatal("PrepareAsset returned a key it failed to store")
	}
}
