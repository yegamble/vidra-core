package setup

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

// secretKind is the encoding a generated secret must use. It is not cosmetic:
// POSTGRES_PASSWORD and REDIS_PASSWORD are substituted into postgres:// and
// redis:// DSNs by the compose chain, where base64's '+' and '/' would need
// percent-encoding — hence hex. The KEKs are decoded with
// base64.StdEncoding.DecodeString by internal/config and must be exactly 32
// bytes — hence base64.
type secretKind int

const (
	secretHex secretKind = iota + 1
	secretBase64
)

// secretSpec is one entry of the manifest below.
type secretSpec struct {
	kind secretKind
	// size is the number of RANDOM BYTES drawn, before encoding. The encoded
	// string is longer (2x for hex, ~1.34x for base64).
	size int
	// kek marks a key-encryption key: rotating it orphans data already sealed in
	// the database, with no re-wrap job anywhere. Rotation therefore demands
	// explicit confirmation (see Request.ConfirmDestructive).
	kek bool
	// openssl is the equivalent shell command, echoed in operator-facing messages
	// so the shape stays greppable against the template's own comments.
	openssl string
	// why is the one-line consequence printed when a KEK rotation is refused.
	why string
}

// secretManifest is the set of variables this engine can MINT, and the shape each
// one must have. Every entry is cross-checked against two sources that must not
// drift: the `generate:` comment on the variable in the meta repo's
// env/production.env.example, and the validation in internal/config/config.go
// that the api boots with.
//
//	JWT_SECRET             openssl rand -base64 48   config: >=32 chars in production, dev default refused
//	POSTGRES_PASSWORD      openssl rand -hex 32      compose: substituted into a postgres:// DSN
//	REDIS_PASSWORD         openssl rand -hex 32      compose: --requirepass + both redis:// DSNs
//	SEARCH_INTERNAL_SECRET openssl rand -hex 32      config: >=32 chars in production when SEARCH_SERVICE_URL is set
//	MFA_KEY_KEK            openssl rand -base64 32   config: base64 of EXACTLY 32 bytes
//	FEDERATION_KEY_KEK     openssl rand -base64 32   config: base64 of EXACTLY 32 bytes
//	ATPROTO_KEY_KEK        openssl rand -base64 32   config: base64 of EXACTLY 32 bytes (falls back to FEDERATION_KEY_KEK)
//	DRM_KEY_KEK            openssl rand -base64 32   config: base64 of EXACTLY 32 bytes (NO fallback — see below)
//	LIVE_INGEST_SECRET     openssl rand -hex 24      shared secret on the internal /live/ingest hooks
//
// DRM_KEY_KEK is the one KEK with no fallback to FEDERATION_KEY_KEK. A content
// key and an ActivityPub actor key are different trust domains (internal/config
// validateDRM says why), so an install that turns DRM on must mint a second
// secret rather than reuse one it already has.
//
// SEARCH_INTERNAL_SECRET is ONE variable on purpose: the compose chain feeds it
// to the api as SEARCH_INTERNAL_SECRET and to the search service as
// INTERNAL_SECRET from that single substitution, so core and search cannot
// disagree — do not add a second key for the search side.
//
// A variable in this manifest is only ever minted when the FILE BEING GENERATED
// assigns it (an active KEY= line). The template ships FEDERATION_KEY_KEK,
// ATPROTO_KEY_KEK and LIVE_INGEST_SECRET commented out — their features are off
// — so those stay absent and unminted; the manifest covers them for the day a
// template turns them on, for --rotate, and for a value carried over from an
// existing file.
//
// Deliberately NOT here: STORAGE_S3_ACCESS_KEY, STORAGE_S3_SECRET_KEY and
// SMTP_PASSWORD are issued by another system, so they can only be answers; and
// OWNER_CLAIM_TOKEN is a dev/test override that internal/config REFUSES in
// production (the real token is minted by the api at boot).
var secretManifest = map[string]secretSpec{
	"JWT_SECRET":             {kind: secretBase64, size: 48, openssl: "openssl rand -base64 48"},
	"POSTGRES_PASSWORD":      {kind: secretHex, size: 32, openssl: "openssl rand -hex 32"},
	"REDIS_PASSWORD":         {kind: secretHex, size: 32, openssl: "openssl rand -hex 32"},
	"SEARCH_INTERNAL_SECRET": {kind: secretHex, size: 32, openssl: "openssl rand -hex 32"},
	"MFA_KEY_KEK": {kind: secretBase64, size: 32, kek: true, openssl: "openssl rand -base64 32",
		why: "it seals every TOTP secret already in the database, and no re-wrap job exists — every user with two-factor enabled is locked out and must re-enrol"},
	"FEDERATION_KEY_KEK": {kind: secretBase64, size: 32, kek: true, openssl: "openssl rand -base64 32",
		why: "it seals the ActivityPub actor private keys already in the database, and ATPROTO_KEY_KEK falls back to it, so linked Bluesky credentials break too"},
	"ATPROTO_KEY_KEK": {kind: secretBase64, size: 32, kek: true, openssl: "openssl rand -base64 32",
		why: "it seals the linked-Bluesky app passwords already in the database; every linked account must be re-authorised"},
	"DRM_KEY_KEK": {kind: secretBase64, size: 32, kek: true, openssl: "openssl rand -base64 32",
		why: "it seals the CENC content keys already in the database, and those keys are the ONLY thing that can decrypt media already packaged under them — there is no re-wrap job and no second copy, so every encrypted video becomes permanently unplayable and must be re-packaged from its source"},
	"LIVE_INGEST_SECRET": {kind: secretHex, size: 24, openssl: "openssl rand -hex 24"},
}

// generate draws a fresh value. r is the entropy source (nil means crypto/rand);
// tests inject a deterministic reader so the golden files are stable.
func (s secretSpec) generate(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, s.size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("setup: read %d random bytes: %w", s.size, err)
	}
	switch s.kind {
	case secretHex:
		return hex.EncodeToString(buf), nil
	case secretBase64:
		return base64.StdEncoding.EncodeToString(buf), nil
	default:
		return "", fmt.Errorf("setup: unknown secret kind %d", s.kind)
	}
}

// SecretVars lists the variables this engine can generate, sorted. Exported for
// the CLI's --rotate help text (and, later, the wizard's rotate affordance).
func SecretVars() []string {
	out := make([]string, 0, len(secretManifest))
	for k := range secretManifest {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// needsValue reports whether a value has to be filled in: either blank, or one
// of the template's deliberately malformed <...> placeholders
// (`<generate: openssl rand -hex 32>`, `<your Spaces access key>`).
func needsValue(v string) bool {
	t := strings.TrimSpace(v)
	return t == "" || IsPlaceholder(t)
}

// IsPlaceholder recognises the template's <...> placeholder convention. Telling
// it apart from a blank matters twice over: a blank is a legitimate "unset" for
// plenty of keys (INSTANCE_DESCRIPTION, SMTP_USERNAME), while a leftover
// placeholder is a value that WOULD reach a container — `<your Spaces access
// key>` even satisfies config's "STORAGE_S3_ACCESS_KEY is required" check — so
// Check rejects placeholders and tolerates blanks.
//
// Exported because a front-end offering a current value as a default has to know
// not to offer a placeholder as one.
func IsPlaceholder(v string) bool {
	t := strings.TrimSpace(v)
	return strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") && len(t) > 1
}
