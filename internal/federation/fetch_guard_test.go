package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// countingTransport answers every request with an empty 404 and counts it, so
// a test can tell "refused before the network" from "fetched and failed".
type countingTransport struct{ n *atomic.Int64 }

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

// federationFetches drives every federation fetch site with one URL and
// returns the errors (nil = the site did not refuse).
func federationFetches(svc *Service, raw string) map[string]error {
	ctx := context.Background()
	out := map[string]error{}
	_, out["actor"] = svc.ResolveKey(ctx, raw+"#main-key")
	_, out["object"] = svc.fetchVideoObject(ctx, raw)
	_, out["outbox"] = svc.fetchOutboxPage(ctx, raw)
	_, out["webfinger"] = svc.fetchJRD(ctx, raw)
	return out
}

// Relaxing private fetches (labs, e2e) must never open the cloud metadata
// endpoint: every federation guard is built with KeepMetadataBlocked.
func TestFederationGuardsKeepMetadataBlockedWhenPrivateAllowed(t *testing.T) {
	var n atomic.Int64
	svc := NewService(newRemoteRepo(), WithAllowPrivateFetch(true),
		WithFetchClient(&http.Client{Transport: countingTransport{n: &n}}))
	for site, err := range federationFetches(svc, "http://169.254.169.254/latest/meta-data") {
		if err == nil {
			t.Errorf("%s: a metadata-address URL was accepted", site)
		}
	}
	if err := svc.deliverActivity(context.Background(), httpsig.Signer{}, "http://169.254.169.254/inbox", []byte("{}")); err == nil {
		t.Error("delivery: a metadata-address inbox was accepted")
	}
	if got := n.Load(); got != 0 {
		t.Fatalf("a metadata-address URL reached the network %d times", got)
	}
}

// In production (private fetches not allowed) a federation document is
// fetched over https only: a plain-http keyId, actor, object, outbox or
// WebFinger URL is refused before any request is made.
func TestProductionFederationFetchesRequireHTTPS(t *testing.T) {
	var n atomic.Int64
	svc := NewService(newRemoteRepo(), WithFetchClient(&http.Client{Transport: countingTransport{n: &n}}))
	for site, err := range federationFetches(svc, "http://peer.example.test/accounts/bob") {
		if err == nil {
			t.Errorf("%s: a plain-http URL was accepted", site)
		}
	}
	if got := n.Load(); got != 0 {
		t.Fatalf("plain-http federation URLs reached the network %d times", got)
	}
	// The https form of the same URLs is attempted (and 404s): the refusal
	// above is about the scheme, not the host.
	federationFetches(svc, "https://peer.example.test/accounts/bob")
	if n.Load() == 0 {
		t.Fatal("https federation URLs were never attempted")
	}
}

// A remote actor cached with an http:// URL (before the https-only rule, or
// by a lab) is refused in production, cache hit or not: the scheme is checked
// before the cache is read, and that is deliberate — the rule is about the
// URL, and a row does not make a URL acceptable by having been stored. Outside
// production the same cached row still resolves. Neither path touches the
// network. docs/operations.md has the query that counts such rows.
func TestCachedHTTPActorIsRefusedInProductionAndResolvesOutsideIt(t *testing.T) {
	const actor = "http://peer.example.test/accounts/bob"
	pubPEM, _ := testActorPEM(t)
	ctx := context.Background()
	for _, prod := range []bool{true, false} {
		var n atomic.Int64
		repo := newRemoteRepo()
		repo.remoteActors[actor] = sqlcgen.RemoteActor{ActorUrl: actor, PublicKeyPem: pubPEM}
		svc := NewService(repo, WithAllowPrivateFetch(!prod),
			WithFetchClient(&http.Client{Transport: countingTransport{n: &n}}))

		_, keyErr := svc.ResolveKey(ctx, actor+"#main-key")
		_, resolveErr := svc.ResolveRemoteActor(ctx, actor)
		for name, err := range map[string]error{"ResolveKey": keyErr, "ResolveRemoteActor": resolveErr} {
			switch {
			case prod && !errors.Is(err, errInsecureFetchScheme):
				t.Errorf("production %s: err = %v, want errInsecureFetchScheme", name, err)
			case !prod && err != nil:
				t.Errorf("non-production %s: err = %v, want the cached actor", name, err)
			}
		}
		if got := n.Load(); got != 0 {
			t.Errorf("prod=%v: a cached actor reached the network %d times", prod, got)
		}
	}
}
