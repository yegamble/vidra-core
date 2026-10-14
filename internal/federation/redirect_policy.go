package federation

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/idna"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// Errors a federation fetch returns when its redirect policy refuses a hop.
var (
	errRedirectToBlockedHost = errors.New("federation: redirect to a blocked instance refused")
	errCrossHostRedirect     = errors.New("federation: cross-host redirect refused")
)

// stdlibMaxRedirects is net/http's own default chain limit, kept for an injected
// client that brings no CheckRedirect of its own.
const stdlibMaxRedirects = 10

type sameHostRedirectsKey struct{}

// withSameHostRedirects marks a request whose redirects must stay on the host
// it was sent to. Actor and object documents carry an id whose host the caller
// compares to the URL it ASKED for; a hop to another host would let that other
// host answer for this one.
func withSameHostRedirects(ctx context.Context) context.Context {
	return context.WithValue(ctx, sameHostRedirectsKey{}, true)
}

func sameHostRedirects(ctx context.Context) bool {
	v, _ := ctx.Value(sameHostRedirectsKey{}).(bool)
	return v
}

// remoteClient is the HTTP client every outbound federation request uses: the
// injected one (tests) or the SSRF-guarded default, with the redirect policy
// layered over whichever CheckRedirect it already has. The SSRF guard
// re-validates each hop's ADDRESS; it knows nothing of the instance block list,
// so without this a permitted host could hand a fetch to a blocked one.
func (s *Service) remoteClient(guard urlsafety.Guard) *http.Client {
	var c http.Client
	if s.fetchClient != nil {
		c = *s.fetchClient
	} else {
		c = *guard.NewClient(remoteFetchTimeout)
	}
	c.CheckRedirect = s.redirectPolicy(c.CheckRedirect)
	return &c
}

// redirectPolicy wraps next (the client's own hop check: chain limit, SSRF
// re-validation) with the federation rules. A hop that stays on the original
// host is not new reach and is allowed; any other hop is refused when the
// request asked for same-host redirects, or when its host is on the block list.
// A block-list lookup error refuses the hop: fail closed.
func (s *Service) redirectPolicy(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if next != nil {
			if err := next(req, via); err != nil {
				return err
			}
		} else if len(via) >= stdlibMaxRedirects {
			return urlsafety.ErrTooManyRedirects
		}
		if len(via) == 0 || strings.EqualFold(req.URL.Host, via[0].URL.Host) {
			return nil
		}
		if sameHostRedirects(req.Context()) {
			return errCrossHostRedirect
		}
		blocked, err := s.hostBlocked(req.Context(), req.URL)
		if err != nil || blocked {
			return errRedirectToBlockedHost
		}
		return nil
	}
}

// hostBlocked reports whether a URL's host is on the admin block list. The
// list stores a lower-case host, or host:port, so the bare canonical host and
// its host:port form (the scheme's default port when none is given) are asked. A host that does not
// canonicalise is reported blocked: it could not be checked.
func (s *Service) hostBlocked(ctx context.Context, u *url.URL) (bool, error) {
	host, ok := canonicalBlockHost(u.Hostname())
	if !ok {
		return true, nil
	}
	keys := []string{host}
	port := u.Port()
	if port == "" {
		// An entry may name the scheme's default port explicitly.
		port = map[string]string{"https": "443", "http": "80"}[strings.ToLower(u.Scheme)]
	}
	if port != "" {
		keys = append(keys, host+":"+port)
	}
	if raw := strings.ToLower(u.Host); !slices.Contains(keys, raw) {
		keys = append(keys, raw)
	}
	for _, k := range keys {
		blocked, err := s.repo.IsInstanceBlocked(ctx, k)
		if err != nil || blocked {
			return true, err
		}
	}
	return false, nil
}

// canonicalBlockHost reduces a hostname to the form the block list is keyed by:
// lower-case, trailing dot removed, IDN in its ASCII form. It is the follow
// path's normalisation.
// TODO: replace with federation.NormalizeFollowHost / instancemod.BlockMatches
// once those shared helpers are on main.
func canonicalBlockHost(hostname string) (string, bool) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if h == "" {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil || ascii == "" {
		return "", false
	}
	return ascii, true
}
