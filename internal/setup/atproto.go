package setup

import (
	"errors"
	"net/url"
	"strings"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/urlsafety"
)

type ATProtoAnswers struct {
	Enabled bool
	// Provider is "local", an HTTPS origin, or empty to keep the saved choice.
	Provider string
}

// ExternalPDSOrigin validates the destination used by new creator links.
func ExternalPDSOrigin(provider string) (string, error) {
	cfg := config.Config{ATProtoEnabled: true, ATProtoPDSURL: provider}
	origin := cfg.DefaultATProtoPDS()
	if _, err := urlsafety.ValidateURL(origin); err != nil {
		return "", errors.New("setup: enter the public HTTPS origin of the external PDS")
	}
	return origin, nil
}

// The pds. prefix needs four bytes within the 253-byte DNS host limit.
func localPDSSite(raw string) bool {
	site, err := url.Parse(raw)
	if err != nil || site.Scheme != "https" || site.User != nil {
		return false
	}
	host := site.Hostname()
	labels := strings.Split(host, ".")
	if len(host) > 249 || len(labels) < 2 || strings.Trim(labels[len(labels)-1], "0123456789") == "" || checkDNSHost(host, raw) != nil {
		return false
	}
	for _, label := range labels {
		if len(label) > 63 {
			return false
		}
	}
	return true
}

func applyATProtoRule(req Request, res *Result) ([]string, error) {
	a := req.Answers.ATProto
	if a == nil {
		return nil, nil
	}
	var added []string
	set := func(key, value string) {
		if _, exists := res.Values[key]; !exists {
			added = append(added, key)
		}
		res.Values[key] = value
	}
	if !a.Enabled {
		if a.Provider != "" {
			return nil, errors.New("setup: choose an ATProto PDS with cross-posting enabled")
		}
		set("ATPROTO_ENABLED", "false")
		return added, nil
	}
	provider := res.Values["ATPROTO_PDS_URL"]
	switch a.Provider {
	case "":
	case "local":
		provider = ""
	default:
		provider = a.Provider
	}
	if provider == "" {
		if !localPDSSite(res.Values["PUBLIC_BASE_URL"]) {
			return nil, errors.New("setup: the local Docker PDS needs an HTTPS site with a public DNS hostname")
		}
	} else {
		origin, err := ExternalPDSOrigin(provider)
		if err != nil {
			return nil, err
		}
		provider = origin
	}
	set("ATPROTO_ENABLED", "true")
	set("ATPROTO_PDS_URL", provider)
	const key = "ATPROTO_KEY_KEK"
	if !needsValue(res.Values[key]) || !needsValue(res.Values[federationKEKKey]) {
		return added, nil
	}
	if req.Existing != nil {
		return nil, errors.New("setup: restore the original ATPROTO_KEY_KEK or its FEDERATION_KEY_KEK fallback. If this is the first use of ATProto and neither key exists, set ATPROTO_KEY_KEK with openssl rand -base64 32")
	}
	secret, err := mint(key, req.Rand)
	if err != nil {
		return nil, err
	}
	set(key, secret)
	res.Generated = append(res.Generated, key)
	return added, nil
}
