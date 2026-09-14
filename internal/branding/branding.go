// Package branding holds this software's human-facing identity.
//
// It exists so the product NAME is spelled exactly once in the repo. Every
// surface a visitor, a signed-in user or a mail recipient can read takes the
// name from here, and that is what makes the white-label instance setting
// branding_hide_software_name (internal/instancesettings) enforceable: there is
// one literal to suppress, and a consumer that re-spelled it could not be
// gated. The package is a dependency-free leaf on purpose — internal/auth,
// internal/donation and internal/httpapi all read it, and instancesettings
// (which owns the toggle) cannot be imported by internal/auth without an import
// cycle (instancesettings -> video -> ... -> auth).
//
// It deliberately does NOT hold the machine-readable identifiers that also
// contain "vidra". NodeInfo software.name, GET /version, /schemaz, the six
// vidra_* cookie names, the X-Vidra-* header names, the __vidra_edge URL param,
// the JWT iss/aud defaults, the User-Agent strings, the Prometheus metric names
// and the HMAC domain-separation constants are PROTOCOL identifiers, not a
// display name: renaming any of them breaks federation interop, deploy probes,
// live sessions or clients, so they keep their own literals and are never
// white-labelled.
package branding

// SoftwareName is the product's human-facing name, as a reader sees it.
const SoftwareName = "Vidra"
