package drm

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ClearKey is the EME ClearKey provider (DRM_PROVIDER=clearkey-test).
//
// IT PROTECTS NOTHING, and that is not a shortcoming — it is what ClearKey IS.
// The license endpoint hands the content key to any viewer who is authorised to
// watch the video, in the clear, over TLS. A viewer with a debugger has the key.
// Its value is that it exercises the ENTIRE path a commercial key system needs
// — key minting, sealing at rest, manifest signalling, session configuration,
// an authorised license request, a CDM decrypting real segments — using a key
// system every browser already implements, with no vendor, no CDM licence and
// no per-request cost. When Widevine or PlayReady is wired later, everything
// except this file has already been proven.
//
// The "-test" in the configured value is deliberate: an operator cannot select
// this provider without typing the word, and the boot log says so again.
//
// # Inert until something packages
//
// Every read path below is gated on a key ROW EXISTING for the video, and the
// only thing that creates one is PrepareAsset, which nothing calls yet. So
// selecting this provider today changes no response: no video has a key, so
// GetProtectionMetadata and LicenseConfiguration both report nothing and the
// license endpoint 404s. That is the correct behaviour for a provider whose
// packaging half has not landed, and it is what makes this slice safe to ship.
type ClearKey struct {
	repo   Repository
	cipher *secretbox.Cipher
	// rand is the entropy source for content keys; nil means crypto/rand. Tests
	// inject a deterministic reader. It is never used for anything an attacker
	// sees, so there is no fallback: a failed read fails the mint.
	rand io.Reader
}

// Compile-time proof that ClearKey satisfies both the provider seam and the
// license-issuing seam httpapi asserts for.
var (
	_ Provider       = (*ClearKey)(nil)
	_ ClearKeyIssuer = (*ClearKey)(nil)
)

// newClearKey builds the provider from a validated Config. Both dependencies
// are REQUIRED and neither is defaulted: a ClearKey provider with no key store
// could mint keys it immediately forgot, and one with no cipher would write
// plaintext content keys into the database — the one outcome §10's doctrine
// exists to prevent. internal/config refuses both combinations at boot; these
// checks are the second half of that pair, so the two can never drift into a
// silently-unsealed install.
func newClearKey(cfg Config) (*ClearKey, error) {
	if cfg.Repo == nil {
		return nil, errors.New("drm: clearkey-test needs a content-key store")
	}
	if cfg.KeyKEK == "" {
		return nil, errors.New("drm: clearkey-test needs DRM_KEY_KEK — content keys are never stored unsealed")
	}
	cipher, err := secretbox.NewCipherFromBase64(cfg.KeyKEK)
	if err != nil {
		// The KEK itself never reaches the message: secretbox's errors describe
		// the SHAPE of the failure (not base64, not 32 bytes) and never echo the
		// value, and this wrap preserves that.
		return nil, fmt.Errorf("drm: DRM_KEY_KEK is unusable: %w", err)
	}
	return &ClearKey{repo: cfg.Repo, cipher: cipher}, nil
}

// PrepareAsset returns this video's content key, minting and sealing one the
// first time it is asked.
//
// READ, INSERT-IF-ABSENT, READ BACK. The read-back is not belt and braces: two
// packagers racing on the same video would otherwise each believe their own
// freshly minted key is the stored one, and the loser would encrypt segments
// under a key the license endpoint will never serve. The insert is ON CONFLICT
// DO NOTHING (see drm_keys.sql), so the second call's insert is a no-op and its
// read-back returns the winner's key — both callers end up with the same key,
// which is the only outcome that does not silently corrupt an asset.
//
// The returned Key is PLAINTEXT and is a secret; see Provider.PrepareAsset.
func (c *ClearKey) PrepareAsset(ctx context.Context, videoID uuid.UUID) (AssetKeys, error) {
	if existing, err := c.lookup(ctx, videoID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNoKeys) {
		return AssetKeys{}, err
	}

	keyID := uuid.New()
	key := make([]byte, ContentKeyLen)
	src := c.rand
	if src == nil {
		src = rand.Reader
	}
	if _, err := io.ReadFull(src, key); err != nil {
		return AssetKeys{}, fmt.Errorf("drm: mint content key: %w", err)
	}
	sealed, err := c.cipher.Seal(key)
	if err != nil {
		return AssetKeys{}, fmt.Errorf("drm: seal content key: %w", err)
	}
	if err := c.repo.InsertVideoDRMKey(ctx, sqlcgen.InsertVideoDRMKeyParams{
		VideoID:          videoID,
		KeyID:            keyID,
		ContentKeySealed: sealed,
	}); err != nil {
		return AssetKeys{}, fmt.Errorf("drm: store content key: %w", err)
	}
	// Read back rather than return what was just minted: on a conflict the
	// insert did nothing and the stored key is somebody else's.
	return c.lookup(ctx, videoID)
}

// GetProtectionMetadata reports CENC protection when this video has a key, and
// nil — clear media — when it does not.
//
// The key ROW is the authority, not the configuration. An instance that turns
// this provider on does not thereby claim its existing library is encrypted;
// only a video that has actually been through PrepareAsset is protected. That
// is what keeps switching the provider on from breaking playback of everything
// already published.
func (c *ClearKey) GetProtectionMetadata(ctx context.Context, videoID uuid.UUID) (*Protection, error) {
	keys, err := c.lookupKeyID(ctx, videoID)
	if errors.Is(err, ErrNoKeys) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Protection{Scheme: SchemeCENC, KeyID: keys}, nil
}

// LicenseConfiguration points the player at this instance's own ClearKey
// endpoint, or reports nil when the video has no key to license.
//
// sessionID is unused here: a ClearKey license is a function of the video and
// the caller's authorisation, and nothing about it varies per session. A
// commercial provider would use it — see Provider.LicenseConfiguration.
func (c *ClearKey) LicenseConfiguration(ctx context.Context, videoID, _ uuid.UUID) (*LicenseConfig, error) {
	if _, err := c.lookupKeyID(ctx, videoID); err != nil {
		if errors.Is(err, ErrNoKeys) {
			return nil, nil
		}
		return nil, err
	}
	return &LicenseConfig{KeySystems: []KeySystemConfig{{
		KeySystem:  KeySystemClearKey,
		LicenseURL: ClearKeyLicensePath(videoID),
	}}}, nil
}

// IssueClearKeyLicense answers one EME ClearKey license request.
//
// AUTHORISATION IS NOT DONE HERE. The caller (internal/httpapi) has already
// reproduced the media routes' visibility decision before this is reached; this
// function's job is the key lookup and the wire format, and mixing the two would
// give the license path an authorisation rule of its own that could drift from
// the one the segments are served under.
//
// Only key ids that actually belong to this video are answered. A request naming
// someone else's KID gets ErrUnknownKeyID rather than that key — a license
// endpoint that served any KID it was asked for would turn one authorised viewer
// into a key oracle for the whole library.
func (c *ClearKey) IssueClearKeyLicense(ctx context.Context, videoID uuid.UUID, kids []string) (*ClearKeyLicense, error) {
	asset, err := c.lookup(ctx, videoID)
	if err != nil {
		return nil, err
	}
	want := base64.RawURLEncoding.EncodeToString(asset.KeyID[:])
	for _, kid := range kids {
		// Constant-time on principle rather than necessity: a KID is public, so
		// there is no secret to leak by comparing it, but the day this loop
		// compares anything else the habit is already in place.
		if len(kid) == len(want) && subtle.ConstantTimeCompare([]byte(kid), []byte(want)) == 1 {
			return &ClearKeyLicense{
				Keys: []ClearKeyJWK{{
					KTY: jwkKeyTypeOctet,
					KID: want,
					K:   base64.RawURLEncoding.EncodeToString(asset.Key),
				}},
				Type: LicenseTypeTemporary,
			}, nil
		}
	}
	return nil, ErrUnknownKeyID
}

// lookup reads and OPENS this video's content key. The plaintext lives only in
// the returned value.
func (c *ClearKey) lookup(ctx context.Context, videoID uuid.UUID) (AssetKeys, error) {
	rows, err := c.repo.GetVideoDRMKey(ctx, videoID)
	if err != nil {
		return AssetKeys{}, fmt.Errorf("drm: read content key: %w", err)
	}
	if len(rows) == 0 {
		return AssetKeys{}, ErrNoKeys
	}
	key, err := c.cipher.Open(rows[0].ContentKeySealed)
	if err != nil {
		// The sealed value is NOT wrapped into the error: it is ciphertext, but
		// it is ciphertext of a content key, and an error body or a log line is
		// not where it belongs. What the operator needs to know is which video
		// and which failure class.
		return AssetKeys{}, fmt.Errorf("%w (video %s): wrong or rotated DRM_KEY_KEK, or the row was tampered with", ErrKeyMaterial, videoID)
	}
	if len(key) != ContentKeyLen {
		return AssetKeys{}, fmt.Errorf("%w (video %s): opened %d bytes, want %d", ErrKeyMaterial, videoID, len(key), ContentKeyLen)
	}
	return AssetKeys{KeyID: rows[0].KeyID, Key: key}, nil
}

// lookupKeyID reads only the PUBLIC half — the KID — and never opens the sealed
// key. The read paths that feed manifests and session responses need nothing
// else, and not decrypting is the cheapest way to guarantee they cannot leak
// what they never held.
func (c *ClearKey) lookupKeyID(ctx context.Context, videoID uuid.UUID) (uuid.UUID, error) {
	rows, err := c.repo.GetVideoDRMKey(ctx, videoID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("drm: read content key: %w", err)
	}
	if len(rows) == 0 {
		return uuid.Nil, ErrNoKeys
	}
	return rows[0].KeyID, nil
}

// ClearKeyIssuer is the license-serving half of ClearKey, kept OFF the Provider
// interface on purpose.
//
// §10 fixes Provider at three methods, and serving licenses is not one of them:
// most providers do not serve licenses at all — a commercial key system's
// licenses come from the vendor's service, which is why LicenseConfiguration
// returns a URL rather than a license. internal/httpapi type-asserts for this
// interface and answers 404 when the configured provider does not implement it,
// so "this instance issues ClearKey licenses" is a capability a provider opts
// into rather than a method every future provider has to stub out.
type ClearKeyIssuer interface {
	IssueClearKeyLicense(ctx context.Context, videoID uuid.UUID, kids []string) (*ClearKeyLicense, error)
}

// The EME ClearKey wire vocabulary (W3C Encrypted Media Extensions, "License
// Request Format" / "License Format"). These are the spelling the browser's CDM
// expects; they are not Vidra's to choose.
const (
	// jwkKeyTypeOctet is JWK's symmetric-key type ("oct", RFC 7518 §6.1).
	jwkKeyTypeOctet = "oct"
	// LicenseTypeTemporary is a session-lifetime license: the key is not
	// persisted by the CDM and dies with the MediaKeySession. The alternative
	// ("persistent-license") is for offline playback, which Vidra does not
	// offer, so this is the only value ever emitted.
	LicenseTypeTemporary = "temporary"
)

// MaxLicenseKeyIDs caps how many key ids one license request may name. A
// ClearKey asset has exactly one KID today; the cap exists so a request cannot
// make the server do unbounded work per call, and is generous enough that
// per-track keys (a later slice) will not trip it.
const MaxLicenseKeyIDs = 16

// ClearKeyRequest is the EME ClearKey license request body: base64url-unpadded
// key ids, exactly as the CDM produces them.
type ClearKeyRequest struct {
	KIDs []string `json:"kids"`
	// Type echoes the MediaKeySession type ("temporary"). Accepted and ignored:
	// the CDM sends it, and rejecting a request over it would break players for
	// no gain when only one value is ever valid.
	Type string `json:"type,omitempty"`
}

// ClearKeyJWK is one key in the response: a JWK with the symmetric key type.
//
// Both KID and K are base64url WITHOUT padding, over the RAW 16 bytes. That
// encoding is mandatory, not stylistic — a padded value ("...==") is rejected by
// browsers' CDMs, and it is the single most common way a hand-written ClearKey
// endpoint fails while looking correct.
type ClearKeyJWK struct {
	KTY string `json:"kty"`
	KID string `json:"kid"`
	// K is the CONTENT KEY, base64url-unpadded. It is the secret this whole
	// package protects at rest, handed to an authorised player in the clear —
	// which is the entire reason ClearKey is a test provider. Never logged.
	K string `json:"k"`
}

// ClearKeyLicense is the EME ClearKey license response body.
type ClearKeyLicense struct {
	Keys []ClearKeyJWK `json:"keys"`
	Type string        `json:"type"`
}

// ClearKeyLicensePath is the origin-relative path of this instance's ClearKey
// license endpoint for one video.
//
// It lives here rather than in internal/httpapi because a provider's job
// includes saying where its licenses come from — for a third-party key system
// this would be the vendor's absolute URL. The route registered in httpapi must
// match it, which TestClearKeyLicenseRouteMatchesProviderPath asserts, so the
// two spellings cannot drift.
func ClearKeyLicensePath(videoID uuid.UUID) string {
	return "/api/v1/videos/" + videoID.String() + "/license/clearkey"
}
