// Package drm is the content-protection provider seam
// (docs/productionization/interfaces.md §10, phase-5 enterprise).
//
// It is structured exactly like internal/cdn, and for the same reason. That
// package's claim is "no CDN vendor in core media logic"; this one's is "no DRM
// vendor anywhere in Vidra", and in both cases the claim is enforced by the
// IMPORT LIST rather than asserted in prose. Nothing below imports a vendor
// SDK, names a vendor in an identifier, or branches per vendor: a provider is
// selected once, by operator configuration, and every consumer above this line
// sees one interface.
//
// # What is and is NOT here
//
// This is the pure-Go core of the seam: the interface, the null provider, and a
// ClearKey provider that mints, seals and serves keys. NO MEDIA BYTES ARE
// ENCRYPTED BY ANY OF IT. The packaging step that would call PrepareAsset does
// not exist yet, so on every install this package is inert: no video has a key
// row, GetProtectionMetadata reports no protection for all of them, and the
// playback session it feeds is byte-identical to the one shipped before this
// package existed. That inertness is deliberate and is regression-tested
// (internal/httpapi: TestPlaybackSessionJSONUnchangedByDefault).
//
// # Where PrepareAsset will be called from
//
// At PACKAGING time, and — for the Shaka-packager path this eventually grows —
// as a SEPARATE PASS OVER A FINISHED CLEAR TREE, not as extra arguments fused
// into the ffmpeg invocation. That ordering is not a preference. Phase 3 landed
// a CMAF ladder whose correctness (codec families, capped-CRF budgets, the
// muxer's aspect-ratio sensitivity) was won one ffmpeg flag at a time; adding
// encryption into those arguments would make every future media bug a question
// of whether DRM caused it. Encrypting a tree that has already been proven
// correct keeps the two failure domains apart, and makes "package once, encrypt
// later" a possible migration rather than a re-transcode.
//
// # The content-key doctrine
//
// §10 says content keys never live in the normal database. internal/secretbox
// is how that is honoured: the key is sealed under DRM_KEY_KEK before it is
// ever handed to a query, and the plaintext exists only inside a call on this
// package. DRM_KEY_KEK has NO fallback to FEDERATION_KEY_KEK (which MFA and
// ATProto both take) — see internal/config.validateDRM for why a content key is
// a different trust domain from an actor key.
package drm

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Provider is the whole content-protection interface, in the three-method shape
// interfaces.md §10 fixes. Everything a vendor differs on is behind it.
//
// The three methods answer three different questions asked at three different
// times, which is why they are not one:
//
//   - PrepareAsset is asked at PACKAGING time, once per asset, by whatever is
//     about to encrypt it. NOTHING CALLS IT YET — the packaging integration is
//     a later slice, and see the package comment for the sequencing rule it has
//     to obey.
//   - GetProtectionMetadata is asked at PLAYBACK time: is this asset encrypted,
//     and under what. nil means CLEAR, which is every video on every install
//     today.
//   - LicenseConfiguration is asked by the playback session: where does a
//     player go for a license, and under which key system. nil means none is
//     needed.
//
// A Provider must be safe for concurrent use: one instance serves every
// request.
type Provider interface {
	// PrepareAsset returns the content key this video's media is (or will be)
	// encrypted under, minting one if it has none. It is IDEMPOTENT by
	// contract: calling it twice for a video returns the same key both times,
	// because the alternative — a second call quietly replacing the key that
	// already-packaged segments were encrypted under — orphans that media with
	// no error anywhere.
	//
	// The returned AssetKeys carries a PLAINTEXT key. It is a secret in the
	// strongest sense this codebase has: never logged, never stored unsealed,
	// never returned by an API, and dropped as soon as the packager is done
	// with it.
	PrepareAsset(ctx context.Context, videoID uuid.UUID) (AssetKeys, error)

	// GetProtectionMetadata reports how this video's media is protected, or nil
	// when it is CLEAR. nil is not an error state and not a "not configured"
	// state — it is the ordinary answer for unencrypted media, which is what
	// every video currently is.
	GetProtectionMetadata(ctx context.Context, videoID uuid.UUID) (*Protection, error)

	// LicenseConfiguration reports where a player acquires a license for this
	// video in this session, or nil when it needs none.
	//
	// sessionID is passed even though the ClearKey provider ignores it: a real
	// DRM service issues per-session licenses and needs the session the request
	// belongs to in order to bound, revoke or rate-limit them. Taking it in the
	// signature now is what keeps that provider from having to change this
	// interface later.
	LicenseConfiguration(ctx context.Context, videoID, sessionID uuid.UUID) (*LicenseConfig, error)
}

// SchemeCENC is ISO Common Encryption's full-sample AES-CTR scheme ("cenc"),
// the one both Widevine and PlayReady take and the one EME ClearKey is defined
// against. FairPlay's 'cbcs' pattern scheme is the other half of the world and
// is deliberately absent until something can actually produce it.
const SchemeCENC = "cenc"

// KeySystemClearKey is the W3C EME key system every browser implements without
// a licence, a CDM or a vendor relationship. It provides NO security — the key
// travels to the player in the clear over TLS and any viewer can read it — and
// it exists here for exactly one purpose: proving the whole path (packaging,
// manifest signalling, session configuration, license request, playback) works
// end to end before a commercial key system is wired to it.
const KeySystemClearKey = "org.w3.clearkey"

// ContentKeyLen is the length of a CENC content key: 16 bytes. AES-128 is the
// only key length Common Encryption defines, so this is a property of the
// standard rather than a choice.
const ContentKeyLen = 16

// ErrNoKeys reports that a video has no content key. It is distinguished from a
// storage failure so a caller can tell "this video is not protected" (an
// ordinary answer, 404 at the license endpoint) from "the database is down" (a
// 500), which a nil-or-error signature could not.
var ErrNoKeys = errors.New("drm: no content keys for this video")

// ErrUnknownKeyID reports that a license request named no key id belonging to
// this video. Separate from ErrNoKeys internally so the two cases are testable,
// but the HTTP surface answers both the same way on purpose: telling a caller
// which of the two happened tells them whether a video is protected, which is
// not their business.
var ErrUnknownKeyID = errors.New("drm: no such key id for this video")

// ErrKeyMaterial reports a stored key that could not be opened or is not
// ContentKeyLen bytes: a wrong or rotated DRM_KEY_KEK, a truncated restore, or
// tampering. It never carries the offending value.
var ErrKeyMaterial = errors.New("drm: stored content key could not be opened")

// AssetKeys is what packaging needs to encrypt one asset.
type AssetKeys struct {
	// KeyID is the CENC KID. PUBLIC: it travels in the manifest, the PSSH box,
	// the EME `encrypted` event and the license request. It names a key.
	KeyID uuid.UUID
	// Key is the raw ContentKeyLen-byte AES key. SECRET, plaintext, in-memory
	// only. Never log it, never put it in an error, never persist it unsealed.
	Key []byte
}

// Protection is what a player and a manifest need to know about an encrypted
// asset. It is the answer to "is this encrypted, and under what", and nothing
// more: it deliberately carries no key material.
type Protection struct {
	// Scheme is the CENC protection scheme, e.g. SchemeCENC.
	Scheme string
	// KeyID is the KID a player will see in the media and will ask a license
	// for.
	KeyID uuid.UUID
}

// KeySystemConfig is one key system a player may use, and where its licenses
// come from.
type KeySystemConfig struct {
	// KeySystem is the EME key system identifier, e.g. KeySystemClearKey.
	KeySystem string
	// LicenseURL is where the player POSTs its license request. It is
	// ORIGIN-RELATIVE when this instance serves the licenses itself, and
	// absolute when a third-party license service does — the same convention
	// the playback session's manifest URLs already use.
	LicenseURL string
}

// LicenseConfig is the set of key systems a player may choose between for one
// session, in the provider's order of preference.
type LicenseConfig struct {
	KeySystems []KeySystemConfig
}

// Provider names, the accepted values of DRM_PROVIDER.
//
// The set is CLOSED and validated at boot (internal/config.validateDRM), so a
// typo refuses to start rather than silently selecting the null provider and
// serving unprotected media to an operator who believes otherwise. That failure
// mode — "DRM is on, and it is not" — is the one this constant exists to make
// impossible.
const (
	// ProviderNone is the default: no content protection anywhere.
	ProviderNone = "none"
	// ProviderClearKeyTest is EME ClearKey. TEST ONLY, and named so in the
	// value itself: it hands the content key to any authorised viewer in the
	// clear. It proves the plumbing; it protects nothing.
	ProviderClearKeyTest = "clearkey-test"
)

// Config is the operator's whole description of content protection, in the
// shape cdn.Config already establishes: a plain struct that cmd/api fills from
// validated configuration, with no environment reading of its own.
type Config struct {
	// Provider is one of the ProviderX constants. Empty means ProviderNone.
	Provider string
	// KeyKEK is DRM_KEY_KEK: standard-base64 of exactly 32 bytes. Required by
	// every provider that stores key material; a SECRET, held only long enough
	// to build a cipher.
	KeyKEK string
	// Repo is the content-key store. Required by every provider that stores key
	// material.
	Repo Repository
}

// Repository is the data access this package needs. *sqlcgen.Queries satisfies
// it, and tests substitute a fake — which is what lets the key lifecycle, the
// sealing round trip and the ClearKey wire format all be proven by `make ci` on
// a runner with no database.
//
// It is deliberately two methods with no update and no delete. See
// internal/store/queries/drm_keys.sql: a content key that cannot be replaced by
// accident is a property of the query set, not of the caller's discipline.
type Repository interface {
	GetVideoDRMKey(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.GetVideoDRMKeyRow, error)
	InsertVideoDRMKey(ctx context.Context, arg sqlcgen.InsertVideoDRMKeyParams) error
}

// New builds the configured provider.
//
// An unconfigured install gets NoDRM rather than a nil Provider: a null object
// means there is exactly ONE code path above this line, and no consumer has to
// remember a nil check before every call. (cdn.New returns nil for the same
// underlying reason — there, "no CDN" has to be absent from a source LIST, so
// nil is the honest answer; here "no DRM" is an answer to a question, so a
// provider that answers it is.)
//
// A provider that needs key material and has none configured is an ERROR, not a
// silent downgrade to NoDRM. internal/config refuses the same combination at
// boot, so this can only fire if the two ever disagree — and an instance that
// started with DRM silently off would be the exact failure this package's
// closed provider set exists to prevent.
func New(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "", ProviderNone:
		return NoDRM{}, nil
	case ProviderClearKeyTest:
		p, err := newClearKey(cfg)
		if err != nil {
			// An explicit nil, not the typed nil *ClearKey: returned through a
			// Provider interface, a typed nil is NOT nil, and a caller checking
			// the provider rather than the error would get an object whose
			// every method panics.
			return nil, err
		}
		return p, nil
	default:
		// The value is echoed because it is a provider NAME — never key
		// material — and naming it is the only thing that makes the boot
		// failure actionable.
		return nil, fmt.Errorf("drm: unknown provider %q (known: %s, %s)",
			cfg.Provider, ProviderNone, ProviderClearKeyTest)
	}
}

// NoDRM is the null provider, and the shipped configuration.
//
// It reports no protection and no license configuration for every video, which
// is not a degraded mode: unencrypted media genuinely has neither. PrepareAsset
// returns an empty AssetKeys and no error, because "prepare an asset for no
// encryption" succeeds by doing nothing — an error there would make a packager
// that wants to be DRM-agnostic branch on the provider, which is the coupling
// this whole seam exists to remove.
type NoDRM struct{}

// PrepareAsset returns no keys. See the type comment for why that is a success.
func (NoDRM) PrepareAsset(context.Context, uuid.UUID) (AssetKeys, error) {
	return AssetKeys{}, nil
}

// GetProtectionMetadata reports clear media, always.
func (NoDRM) GetProtectionMetadata(context.Context, uuid.UUID) (*Protection, error) {
	return nil, nil
}

// LicenseConfiguration reports that no license is needed, always.
func (NoDRM) LicenseConfiguration(context.Context, uuid.UUID, uuid.UUID) (*LicenseConfig, error) {
	return nil, nil
}
