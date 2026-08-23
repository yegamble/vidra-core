package drm

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// testKEK is a 32-byte KEK in the encoding config validates and secretbox
// expects. A second, different one proves that opening with the wrong key is a
// distinguishable failure rather than garbage.
var (
	testKEK      = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32))
	testOtherKEK = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("z"), 32))
)

// fakeRepo is the content-key store, in memory. It reproduces the ONE property
// of the real query set that the provider's correctness depends on: the insert
// is ON CONFLICT DO NOTHING, so a second insert for a video that already has a
// key changes nothing and reports no error.
type fakeRepo struct {
	rows      map[uuid.UUID]sqlcgenRow
	getErr    error
	insertErr error
	gets      int
	inserts   int
	// onInsert runs at the START of an insert, before the conflict check, so a
	// test can make somebody else's row appear between our read and our write —
	// which is exactly the concurrent-packager race the read-back exists for.
	onInsert func(r *fakeRepo)
}

// sqlcgenRow keeps the generated row type readable in test fixtures.
type sqlcgenRow = sqlcgen.GetVideoDRMKeyRow

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]sqlcgenRow{}}
}

func (r *fakeRepo) GetVideoDRMKey(_ context.Context, videoID uuid.UUID) ([]sqlcgenRow, error) {
	r.gets++
	if r.getErr != nil {
		return nil, r.getErr
	}
	row, ok := r.rows[videoID]
	if !ok {
		return nil, nil
	}
	return []sqlcgenRow{row}, nil
}

func (r *fakeRepo) InsertVideoDRMKey(_ context.Context, arg sqlcgen.InsertVideoDRMKeyParams) error {
	r.inserts++
	if r.onInsert != nil {
		r.onInsert(r)
	}
	if r.insertErr != nil {
		return r.insertErr
	}
	if _, exists := r.rows[arg.VideoID]; exists {
		return nil // ON CONFLICT DO NOTHING
	}
	r.rows[arg.VideoID] = sqlcgenRow{
		VideoID:          arg.VideoID,
		KeyID:            arg.KeyID,
		ContentKeySealed: arg.ContentKeySealed,
	}
	return nil
}

// seal is the test's own sealer, so a fixture row can be built the way the
// production path would have built it.
func seal(t *testing.T, kek string, key []byte) string {
	t.Helper()
	c, err := secretbox.NewCipherFromBase64(kek)
	if err != nil {
		t.Fatalf("build cipher: %v", err)
	}
	sealed, err := c.Seal(key)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

// TestNoDRMIsInert pins the shipped configuration. Every install runs this
// provider, so any answer other than "clear, no license, no error" would be a
// behaviour change for every video that exists.
func TestNoDRMIsInert(t *testing.T) {
	var p Provider = NoDRM{}
	id := uuid.New()

	keys, err := p.PrepareAsset(context.Background(), id)
	if err != nil {
		t.Fatalf("PrepareAsset error = %v, want nil — preparing an asset for no encryption succeeds by doing nothing", err)
	}
	if keys.KeyID != uuid.Nil || keys.Key != nil {
		t.Errorf("PrepareAsset = %+v, want the zero AssetKeys", keys)
	}
	prot, err := p.GetProtectionMetadata(context.Background(), id)
	if err != nil || prot != nil {
		t.Errorf("GetProtectionMetadata = (%v, %v), want (nil, nil) — unencrypted media has no protection", prot, err)
	}
	lic, err := p.LicenseConfiguration(context.Background(), id, uuid.New())
	if err != nil || lic != nil {
		t.Errorf("LicenseConfiguration = (%v, %v), want (nil, nil)", lic, err)
	}
}

// TestNewProviderSelection. The failing rows all share one shape: a
// configuration that would have produced an instance believing it protects
// content while it does not, or one writing content keys unsealed. Both must be
// boot failures, never silent downgrades.
func TestNewProviderSelection(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
		// wantClearKey asserts the concrete provider, because "New returned
		// something" is not the property — "New returned the provider that was
		// asked for" is.
		wantClearKey bool
	}{
		{
			name: "unset is the null provider",
			cfg:  Config{},
		},
		{
			name: "explicit none",
			cfg:  Config{Provider: ProviderNone},
		},
		{
			// A KEK set alongside no provider is inert here rather than an
			// error: internal/config is where that combination is refused, and
			// duplicating the refusal would give two places to disagree.
			name: "none ignores a KEK",
			cfg:  Config{Provider: ProviderNone, KeyKEK: testKEK, Repo: newFakeRepo()},
		},
		{
			name:         "clearkey with a store and a KEK",
			cfg:          Config{Provider: ProviderClearKeyTest, KeyKEK: testKEK, Repo: newFakeRepo()},
			wantClearKey: true,
		},
		{
			name:    "clearkey with no KEK would store plaintext keys",
			cfg:     Config{Provider: ProviderClearKeyTest, Repo: newFakeRepo()},
			wantErr: true,
		},
		{
			name:    "clearkey with no store would forget every key it minted",
			cfg:     Config{Provider: ProviderClearKeyTest, KeyKEK: testKEK},
			wantErr: true,
		},
		{
			name:    "clearkey with a KEK that is not 32 bytes",
			cfg:     Config{Provider: ProviderClearKeyTest, KeyKEK: base64.StdEncoding.EncodeToString([]byte("short")), Repo: newFakeRepo()},
			wantErr: true,
		},
		{
			name:    "clearkey with a KEK that is not base64",
			cfg:     Config{Provider: ProviderClearKeyTest, KeyKEK: "not base64 at all!!", Repo: newFakeRepo()},
			wantErr: true,
		},
		{
			// The failure this closed set exists for: a typo must not become
			// "no protection".
			name:    "a misspelled provider is refused, not downgraded",
			cfg:     Config{Provider: "clearkey", KeyKEK: testKEK, Repo: newFakeRepo()},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("New(%+v) = nil error, want a refusal", tc.cfg.Provider)
				}
				if p != nil {
					t.Errorf("New returned a provider alongside an error; a rejected configuration must yield nothing to run with")
				}
				return
			}
			if err != nil {
				t.Fatalf("New error = %v, want nil", err)
			}
			if _, isClearKey := p.(*ClearKey); isClearKey != tc.wantClearKey {
				t.Fatalf("provider is *ClearKey = %v, want %v (got %T)", isClearKey, tc.wantClearKey, p)
			}
			if !tc.wantClearKey {
				if _, isNone := p.(NoDRM); !isNone {
					t.Fatalf("provider = %T, want drm.NoDRM — an unconfigured install must get the null object, never nil", p)
				}
			}
		})
	}
}

// TestNewErrorNamesTheOffendingProvider. The value is a provider NAME, never key
// material, so echoing it is safe — and it is the only thing that makes the boot
// failure actionable.
func TestNewErrorNamesTheOffendingProvider(t *testing.T) {
	_, err := New(Config{Provider: "widevien"})
	if err == nil {
		t.Fatal("New accepted an unknown provider")
	}
	for _, want := range []string{"widevien", ProviderNone, ProviderClearKeyTest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestKEKNeverAppearsInAnError. A KEK is a secret and a boot error is logged;
// the two must never meet.
func TestKEKNeverAppearsInAnError(t *testing.T) {
	_, err := New(Config{Provider: ProviderClearKeyTest, KeyKEK: testKEK, Repo: nil})
	if err == nil {
		t.Fatal("New accepted clearkey-test with no store")
	}
	if strings.Contains(err.Error(), testKEK) {
		t.Fatalf("the KEK appears in a boot error: %v", err)
	}
	_, err = New(Config{Provider: ProviderClearKeyTest, KeyKEK: "zzzz", Repo: newFakeRepo()})
	if err == nil {
		t.Fatal("New accepted an unusable KEK")
	}
	if strings.Contains(err.Error(), "zzzz") {
		t.Fatalf("the KEK value appears in a boot error: %v", err)
	}
}

// errBoom stands in for any storage failure.
var errBoom = errors.New("boom")

// TestStorageFailureIsNotMistakenForClearMedia. "The database is down" and "this
// video is not protected" produce the same nil in a naive signature, and the
// consequence of confusing them is a player told to play encrypted bytes in the
// clear.
func TestStorageFailureIsNotMistakenForClearMedia(t *testing.T) {
	repo := newFakeRepo()
	repo.getErr = errBoom
	p, err := New(Config{Provider: ProviderClearKeyTest, KeyKEK: testKEK, Repo: repo})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.GetProtectionMetadata(context.Background(), uuid.New()); err == nil {
		t.Fatal("GetProtectionMetadata swallowed a storage failure and reported clear media")
	}
	if _, err := p.LicenseConfiguration(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("LicenseConfiguration swallowed a storage failure")
	}
}
