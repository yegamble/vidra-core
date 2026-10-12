package federation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrBadInteraction means the handle or the target of a remote-interaction
// lookup is malformed (handle not user@host, or target not an id on THIS
// instance).
var ErrBadInteraction = errors.New("federation: bad remote-interaction handle or target")

// ErrNoSubscribeTemplate means the handle's server answered WebFinger but
// advertises no usable subscribe template (or an unsafe one).
var ErrNoSubscribeTemplate = errors.New("federation: remote server advertises no subscribe template")

// ResolveSubscribeURL is the OUTBOUND half of remote interaction: a visitor
// types their own fediverse handle (user@host), we WebFinger THEIR server —
// through the SSRF-guarded fetch every other remote lookup uses — and return
// that server's subscribe template with {uri} replaced by the (escaped) local
// target, ready for the browser to navigate to.
//
// target must be an id on this instance (baseURL prefix): the endpoint exists
// to send a visitor to their home server about OUR content, not to mint
// arbitrary links on a third party's behalf. The returned URL is only ever
// navigated by the visitor's browser, never fetched by us, but it is still
// required to be https (http only where private fetches are allowed, i.e.
// dev/e2e) with a host and no userinfo.
func (s *Service) ResolveSubscribeURL(ctx context.Context, handle, target string) (string, error) {
	name, domain, ok := splitInteractionHandle(handle)
	if !ok {
		return "", ErrBadInteraction
	}
	if !strings.HasPrefix(target, s.baseURL+"/") {
		return "", ErrBadInteraction
	}
	tmpl, err := webFingerTry(ctx, s, name, domain, func(j *JRD) (string, error) {
		for _, l := range j.Links {
			if l.Rel == SubscribeRel && strings.Contains(l.Template, "{uri}") {
				return l.Template, nil
			}
		}
		return "", ErrNoSubscribeTemplate
	})
	if err != nil {
		return "", err
	}
	out := strings.ReplaceAll(tmpl, "{uri}", url.QueryEscape(target))
	u, err := url.Parse(out)
	if err != nil || u.Host == "" || u.User != nil ||
		(u.Scheme != "https" && !(u.Scheme == "http" && s.allowPrivateFetch)) {
		return "", fmt.Errorf("%w: unsafe template", ErrNoSubscribeTemplate)
	}
	return out, nil
}

// splitInteractionHandle parses "user@host" / "@user@host" / "acct:user@host".
// The host must look like a bare host[:port] — no path, query, userinfo or
// whitespace — because it is interpolated into a URL we will fetch.
func splitInteractionHandle(handle string) (name, domain string, ok bool) {
	h := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(handle), "acct:"), "@")
	name, domain, ok = strings.Cut(h, "@")
	if !ok || name == "" || domain == "" || strings.ContainsAny(name+domain, " \t\r\n/\\?#@%") {
		return "", "", false
	}
	return name, domain, true
}
