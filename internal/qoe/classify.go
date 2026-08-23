package qoe

import (
	"net/url"
	"sort"
	"strings"
)

// Deriving the delivery source, and why the server does it.
//
// The server does not currently tell a client which source served a byte:
// serveMediaAsset redirects on the first non-api-proxy source and emits no
// marker. The obvious fix — add a response header naming the source — is the
// wrong one twice over. It puts an internal delivery decision on every media
// response for a third party's cache to see and key on, and it does not even
// work for the case that matters: after a 307 the bytes come from the CDN or the
// object store, and Vidra sets no headers on that response at all.
//
// So the client reports the ORIGIN it actually fetched from — a fact it always
// has, because it is the final URL of its own request — and the server maps that
// origin onto the closed vocabulary. Server-side classification is what keeps
// cardinality bounded: an origin the server does not recognise becomes exactly
// one value, SourceOther, and never a dimension value of its own. A client
// cannot expand the vocabulary by reporting a new hostname, which is precisely
// what "the client tells us the source" would have allowed.

// Classifier maps a fetched URL onto a DeliverySource using the instance's own
// configuration. The zero value classifies everything it cannot recognise as
// SourceOther, which is the correct answer for an instance that has configured
// none of these.
type Classifier struct {
	// bases are the configured prefixes, longest first. Longest-first is
	// load-bearing rather than tidy: an operator may host a CDN or a gateway
	// under a path of the public origin (https://example.com/cdn/...), and
	// matching the public origin first would classify every one of those as
	// api-proxy while looking entirely correct.
	bases []classifierBase
}

type classifierBase struct {
	prefix string
	source DeliverySource
}

// NewClassifier builds a Classifier from the instance's configured origins.
//
//   - cdnBase is DELIVERY_CDN_BASE_URL (phase-4 item 2). It may carry a path.
//   - ipfsGateway is IPFS_GATEWAY_URL.
//   - objectStore is the public base of the object store presigned URLs point
//     at, built from STORAGE_S3_ENDPOINT + scheme by the caller. Empty on a
//     local-filesystem install, which cannot presign at all.
//   - publicBase is PUBLIC_BASE_URL, this instance's own origin.
//
// Any of them may be empty; an empty base simply never matches. Values that do
// not parse are dropped rather than rejected: this is a telemetry classifier,
// and a malformed CDN base is already a boot-validated error elsewhere
// (config.Validate) — failing here as well would turn a config typo into a
// second, more confusing failure.
func NewClassifier(cdnBase, ipfsGateway, objectStore, publicBase string) *Classifier {
	c := &Classifier{}
	add := func(raw string, source DeliverySource) {
		if p := normalizeBase(raw); p != "" {
			c.bases = append(c.bases, classifierBase{prefix: p, source: source})
		}
	}
	add(cdnBase, SourceCDN)
	add(ipfsGateway, SourceIPFSGateway)
	add(objectStore, SourcePresigned)
	add(publicBase, SourceAPIProxy)
	sort.SliceStable(c.bases, func(i, j int) bool {
		return len(c.bases[i].prefix) > len(c.bases[j].prefix)
	})
	return c
}

// Classify maps one fetched URL onto the vocabulary.
//
// An ORIGIN-RELATIVE URL ("/api/v1/videos/.../master.m3u8") is api-proxy by
// definition — a relative fetch cannot have gone anywhere but this origin — and
// is classified that way even on an instance with no PUBLIC_BASE_URL set, which
// is the default local install.
//
// Everything unrecognised is SourceOther. That includes the case an operator
// will hit first: a CDN in front of the API that nobody told Vidra about. It
// shows up as 'other' rather than as a wrong answer, which is the intended
// behaviour — the fix is to configure DELIVERY_CDN_BASE_URL, and 'other' is the
// signal that says so.
func (c *Classifier) Classify(raw string) DeliverySource {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceOther
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return SourceAPIProxy
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return SourceOther
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return SourceOther
	}
	// Compare on scheme+host+path only. The query is where a presigned URL keeps
	// its signature and where a cache-buster keeps its ?v= — neither is part of
	// an origin, and including them would mean a signed URL never matched its own
	// object store.
	candidate := scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
	if c != nil {
		for _, b := range c.bases {
			if matchesBase(candidate, b.prefix) {
				return b.source
			}
		}
	}
	return SourceOther
}

// matchesBase reports whether candidate is base or lives under it. The boundary
// check is what stops "https://cdn.example.com.attacker.test/x" from matching a
// base of "https://cdn.example.com": a bare strings.HasPrefix would say yes, and
// the resulting row would attribute an attacker-chosen host's latency to the
// operator's CDN.
func matchesBase(candidate, base string) bool {
	if candidate == base {
		return true
	}
	if !strings.HasPrefix(candidate, base) {
		return false
	}
	return candidate[len(base)] == '/'
}

// normalizeBase lowercases the scheme and host, drops any query/fragment and
// trailing slash, and rejects anything that is not an absolute http(s) URL.
func normalizeBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
}
