package atproto

// ATProto identity resolution for login: handle → DID → PDS, with the
// bidirectional binding that makes a handle trustworthy.
//
// A handle (e.g. alice.bsky.social) is only a claim until BOTH directions agree:
//
//  1. handle → DID: via DNS TXT `_atproto.<handle>` (value `did=<did>`) and/or
//     HTTPS `GET https://<handle>/.well-known/atproto-did` (2xx, body = bare DID).
//     DNS wins on conflict.
//  2. DID → handle: the DID document's `alsoKnownAs` MUST list `at://<handle>`.
//
// Only when the DID names the handle back is the handle authoritative. Skipping
// direction (2) would let anyone who can set one direction impersonate a handle,
// so it is enforced unconditionally (ErrDIDMismatch on failure).
//
// The DID document also yields the PDS: the service entry whose id ends
// `#atproto_pds` with type `AtprotoPersonalDataServer`. did:plc documents come
// from the fixed plc.directory host; did:web documents come from an
// attacker-controlled host and so go through the SSRF guard like every other hop.

import (
	"context"
	"encoding/json"
	"strings"
)

// maxDIDLength bounds a DID string (spec ceiling; also a cheap DoS guard).
const maxDIDLength = 2048

// Identity is a fully resolved, bidirectionally verified ATProto identity.
type Identity struct {
	// DID is the stable account identifier (the login subject).
	DID string
	// Handle is the verified handle (the DID's alsoKnownAs claims it back).
	Handle string
	// PDSURL is the account's Personal Data Server origin (https, no path).
	PDSURL string
}

// WithTXTResolver overrides the DNS TXT lookup used for handle→DID resolution.
// Tests inject a fake; production uses net.DefaultResolver.LookupTXT.
func WithTXTResolver(fn func(ctx context.Context, name string) ([]string, error)) OAuthClientOption {
	return func(c *OAuthClient) {
		if fn != nil {
			c.lookupTXT = fn
		}
	}
}

// WithPLCURL overrides the did:plc directory base URL. DEV/TEST-ONLY: production
// uses the fixed plc.directory host.
func WithPLCURL(u string) OAuthClientOption {
	return func(c *OAuthClient) {
		if u != "" {
			c.plcURL = strings.TrimRight(u, "/")
		}
	}
}

// WithHandleWellKnownBase overrides the https://<handle> origin used for the
// /.well-known/atproto-did fetch. DEV/TEST-ONLY (lets a test serve the
// well-known document from an httptest server).
func WithHandleWellKnownBase(base string) OAuthClientOption {
	return func(c *OAuthClient) {
		if base != "" {
			c.wellKnownBase = strings.TrimRight(base, "/")
		}
	}
}

// didDocument is the subset of a DID document the login flow consumes.
type didDocument struct {
	ID          string           `json:"id"`
	AlsoKnownAs []string         `json:"alsoKnownAs"`
	Service     []didDocumentSvc `json:"service"`
}

type didDocumentSvc struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	ServiceEndpoint string `json:"serviceEndpoint"`
}

// ResolveIdentity resolves a handle to a bidirectionally verified Identity:
// handle→DID, then DID→document (PDS + alsoKnownAs), then it enforces that the
// document claims the handle back. Any failure of that binding is ErrDIDMismatch.
func (c *OAuthClient) ResolveIdentity(ctx context.Context, handle string) (Identity, error) {
	h := normalizeHandle(handle)
	if !validHandle(h) {
		return Identity{}, ErrInvalidHandle
	}
	did, err := c.ResolveHandle(ctx, h)
	if err != nil {
		return Identity{}, err
	}
	doc, err := c.resolveDIDDoc(ctx, did)
	if err != nil {
		return Identity{}, err
	}
	if !alsoKnownAsClaims(doc.AlsoKnownAs, h) {
		return Identity{}, ErrDIDMismatch
	}
	pds, err := c.extractPDS(doc)
	if err != nil {
		return Identity{}, err
	}
	return Identity{DID: did, Handle: h, PDSURL: pds}, nil
}

// ResolveHandle resolves a handle to a DID via DNS TXT and/or the HTTPS
// well-known method. DNS takes precedence on conflict. ErrHandleNotFound when
// neither yields a DID.
func (c *OAuthClient) ResolveHandle(ctx context.Context, handle string) (string, error) {
	h := normalizeHandle(handle)
	if !validHandle(h) {
		return "", ErrInvalidHandle
	}
	if did := c.resolveHandleDNS(ctx, h); did != "" {
		return did, nil
	}
	if did := c.resolveHandleHTTP(ctx, h); did != "" {
		return did, nil
	}
	return "", ErrHandleNotFound
}

// resolveHandleDNS looks up `_atproto.<handle>` TXT records and returns the first
// `did=<did>` value, or "" if none/error (the HTTPS method is the fallback).
func (c *OAuthClient) resolveHandleDNS(ctx context.Context, handle string) string {
	records, err := c.lookupTXT(ctx, "_atproto."+handle)
	if err != nil {
		return ""
	}
	for _, rec := range records {
		if v, ok := strings.CutPrefix(strings.TrimSpace(rec), "did="); ok {
			if did := strings.TrimSpace(v); validDIDSyntax(did) {
				return did
			}
		}
	}
	return ""
}

// resolveHandleHTTP fetches https://<handle>/.well-known/atproto-did (SSRF
// guarded; the host is attacker-controlled) and returns the bare DID body, or ""
// on any failure.
func (c *OAuthClient) resolveHandleHTTP(ctx context.Context, handle string) string {
	base := c.wellKnownBase
	if base == "" {
		base = "https://" + handle
	}
	body, err := c.getGuarded(ctx, "atproto-did", base+"/.well-known/atproto-did", maxWellKnownDID)
	if err != nil {
		return ""
	}
	did := strings.TrimSpace(string(body))
	if !validDIDSyntax(did) {
		return ""
	}
	return did
}

// ResolveDID resolves a DID to its document and returns the atproto PDS origin.
// The Handle is left empty — only ResolveIdentity, which also verifies the
// handle→DID direction, may assert a handle.
func (c *OAuthClient) ResolveDID(ctx context.Context, did string) (Identity, error) {
	doc, err := c.resolveDIDDoc(ctx, did)
	if err != nil {
		return Identity{}, err
	}
	pds, err := c.extractPDS(doc)
	if err != nil {
		return Identity{}, err
	}
	return Identity{DID: did, PDSURL: pds}, nil
}

// resolveDIDDoc fetches and validates a DID document for did:plc / did:web,
// enforcing that the document id equals the DID.
func (c *OAuthClient) resolveDIDDoc(ctx context.Context, did string) (didDocument, error) {
	if !validDIDSyntax(did) {
		return didDocument{}, ErrUnsupportedDID
	}
	var docURL string
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		// Fixed, trusted host; the DID syntax is validated above before it is
		// interpolated into the path.
		docURL = c.plcURL + "/" + did
	case strings.HasPrefix(did, "did:web:"):
		host, err := didWebHost(did)
		if err != nil {
			return didDocument{}, err
		}
		// Attacker-controlled host → SSRF guarded like every other fetch. https in
		// production; the allowPrivate dev/test knob relaxes to http (mirroring the
		// getGuarded scheme policy) so a loopback did:web server can be reached.
		scheme := "https"
		if c.allowPrivate {
			scheme = "http"
		}
		docURL = scheme + "://" + host + "/.well-known/did.json"
	default:
		return didDocument{}, ErrUnsupportedDID
	}
	body, err := c.getGuarded(ctx, "did-doc", docURL, maxMetadataBody)
	if err != nil {
		return didDocument{}, err
	}
	var doc didDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return didDocument{}, ErrDIDMismatch
	}
	if doc.ID != did {
		return didDocument{}, ErrDIDMismatch
	}
	return doc, nil
}

// didWebHost decodes the host of a did:web (only the plain host form,
// did:web:<hostname>, is supported; path-form did:web with `:` segments is not
// used by ATProto PDS accounts). Percent-encoded ports (`%3A`) are decoded.
func didWebHost(did string) (string, error) {
	rest := strings.TrimPrefix(did, "did:web:")
	if rest == "" || strings.Contains(rest, ":") {
		// A colon here would be the path form (unsupported); a bare host may carry
		// a percent-encoded port, handled below.
		return "", ErrUnsupportedDID
	}
	host := strings.ReplaceAll(rest, "%3A", ":")
	host = strings.ReplaceAll(host, "%3a", ":")
	if host == "" {
		return "", ErrUnsupportedDID
	}
	return host, nil
}

// extractPDS returns the atproto PDS origin from a DID document: the service
// entry whose id ends `#atproto_pds` with type AtprotoPersonalDataServer and a
// valid origin serviceEndpoint (https in production). ErrPDSNotFound when
// absent/invalid.
func (c *OAuthClient) extractPDS(doc didDocument) (string, error) {
	for _, svc := range doc.Service {
		if strings.HasSuffix(svc.ID, "#atproto_pds") && svc.Type == "AtprotoPersonalDataServer" {
			endpoint := strings.TrimSpace(svc.ServiceEndpoint)
			if !c.validOrigin(endpoint) {
				return "", ErrPDSNotFound
			}
			return strings.TrimRight(endpoint, "/"), nil
		}
	}
	return "", ErrPDSNotFound
}

// alsoKnownAsClaims reports whether the DID document's alsoKnownAs list contains
// exactly `at://<handle>` — the reverse binding that authorises the handle.
func alsoKnownAsClaims(aka []string, handle string) bool {
	want := "at://" + handle
	for _, entry := range aka {
		if strings.TrimSpace(entry) == want {
			return true
		}
	}
	return false
}

// validDIDSyntax is a permissive DID syntax check: `did:<method>:<id>`, ASCII,
// within the length ceiling. It does not resolve the DID.
func validDIDSyntax(did string) bool {
	if len(did) < len("did:x:y") || len(did) > maxDIDLength {
		return false
	}
	if !strings.HasPrefix(did, "did:") {
		return false
	}
	parts := strings.SplitN(did, ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return false
	}
	for _, r := range did {
		if r < 0x21 || r > 0x7e {
			return false // non-printable / non-ASCII
		}
	}
	return true
}
