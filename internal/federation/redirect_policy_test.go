package federation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// redirectWorld is two real HTTP origins: "origin" (allowed) and "other". The
// SSRF guard is relaxed for loopback, so the default guarded client — and its
// real redirect handling — is what runs.
type redirectWorld struct {
	repo      fakeRepo
	svc       *Service
	origin    *httptest.Server
	other     *httptest.Server
	otherHits *atomic.Int32
	originMux *http.ServeMux
	otherMux  *http.ServeMux
}

func newRedirectWorld(t *testing.T, blockOther bool) *redirectWorld {
	t.Helper()
	w := &redirectWorld{originMux: http.NewServeMux(), otherMux: http.NewServeMux(), otherHits: &atomic.Int32{}}
	w.origin = httptest.NewServer(w.originMux)
	t.Cleanup(w.origin.Close)
	w.other = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.otherHits.Add(1)
		w.otherMux.ServeHTTP(rw, r)
	}))
	t.Cleanup(w.other.Close)
	w.repo = newFollowRepo(uuid.New())
	w.repo.blockedDomains = map[string]bool{}
	if blockOther {
		// The dev form the block list accepts for a ported origin: host:port.
		w.repo.blockedDomains[strings.TrimPrefix(w.other.URL, "http://")] = true
	}
	w.svc = NewService(w.repo, WithBaseURL("https://videos.example"), WithAllowPrivateFetch(true))
	return w
}

func redirectTo(target string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) { http.Redirect(rw, r, target, http.StatusFound) }
}

func serveJSON(body string) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/activity+json")
		_, _ = rw.Write([]byte(body))
	}
}

func actorDoc(id string) string {
	return `{"id":"` + id + `","type":"Person","preferredUsername":"ada","inbox":"` + id + `/inbox","publicKey":{"publicKeyPem":"PEM"}}`
}

const jrdBody = `{"links":[{"rel":"self","type":"application/activity+json","href":"https://x.example/users/ada"}]}`

func TestFederationRedirectToBlockedHostIsRefused(t *testing.T) {
	w := newRedirectWorld(t, true)
	w.originMux.Handle("/.well-known/webfinger", redirectTo(w.other.URL+"/.well-known/webfinger"))
	w.otherMux.Handle("/.well-known/webfinger", serveJSON(jrdBody))

	_, err := w.svc.fetchJRD(context.Background(), w.origin.URL+"/.well-known/webfinger")
	if !errors.Is(err, errRedirectToBlockedHost) {
		t.Fatalf("fetchJRD err = %v, want errRedirectToBlockedHost", err)
	}
	if n := w.otherHits.Load(); n != 0 {
		t.Fatalf("blocked host was contacted %d times", n)
	}
}

func TestFederationRedirectBetweenAllowedHostsStillWorks(t *testing.T) {
	w := newRedirectWorld(t, false)
	w.originMux.Handle("/.well-known/webfinger", redirectTo(w.other.URL+"/.well-known/webfinger"))
	w.otherMux.Handle("/.well-known/webfinger", serveJSON(jrdBody))

	jrd, err := w.svc.fetchJRD(context.Background(), w.origin.URL+"/.well-known/webfinger")
	if err != nil {
		t.Fatalf("fetchJRD: %v", err)
	}
	if len(jrd.Links) != 1 {
		t.Fatalf("links = %+v", jrd.Links)
	}
}

func TestActorFetchRedirectToBlockedHostCachesNothing(t *testing.T) {
	w := newRedirectWorld(t, true)
	actorURL := w.origin.URL + "/users/ada"
	w.originMux.Handle("/users/ada", redirectTo(w.other.URL+"/users/ada"))
	w.otherMux.Handle("/users/ada", serveJSON(actorDoc(actorURL)))

	if _, err := w.svc.ResolveRemoteActor(context.Background(), actorURL); err == nil {
		t.Fatal("ResolveRemoteActor followed a redirect to a blocked host")
	}
	if len(w.repo.remoteActors) != 0 {
		t.Fatalf("cached after a refused redirect: %+v", w.repo.remoteActors)
	}
	if n := w.otherHits.Load(); n != 0 {
		t.Fatalf("blocked host was contacted %d times", n)
	}
}

// An actor document is cached under the URL asked for, so another host — even
// an unblocked one — must not be able to answer for it via a redirect.
func TestActorFetchCrossHostRedirectIsRefused(t *testing.T) {
	w := newRedirectWorld(t, false)
	actorURL := w.origin.URL + "/users/ada"
	w.originMux.Handle("/users/ada", redirectTo(w.other.URL+"/users/ada"))
	w.otherMux.Handle("/users/ada", serveJSON(actorDoc(actorURL)))

	_, err := w.svc.ResolveRemoteActor(context.Background(), actorURL)
	if !errors.Is(err, errCrossHostRedirect) {
		t.Fatalf("err = %v, want errCrossHostRedirect", err)
	}
	if len(w.repo.remoteActors) != 0 {
		t.Fatalf("cached after a cross-host redirect: %+v", w.repo.remoteActors)
	}
}

func TestActorFetchSameHostRedirectIsFollowed(t *testing.T) {
	w := newRedirectWorld(t, false)
	actorURL := w.origin.URL + "/users/ada"
	w.originMux.Handle("/users/ada", redirectTo(w.origin.URL+"/accounts/ada"))
	w.originMux.Handle("/accounts/ada", serveJSON(actorDoc(actorURL)))

	if _, err := w.svc.ResolveRemoteActor(context.Background(), actorURL); err != nil {
		t.Fatalf("ResolveRemoteActor: %v", err)
	}
	if _, ok := w.repo.remoteActors[actorURL]; !ok {
		t.Fatalf("actor not cached: %+v", w.repo.remoteActors)
	}
}

func TestActorFetchRefusesForeignID(t *testing.T) {
	w := newRedirectWorld(t, false)
	actorURL := w.origin.URL + "/users/ada"
	w.originMux.Handle("/users/ada", serveJSON(actorDoc(w.other.URL+"/users/ada")))

	if _, err := w.svc.ResolveRemoteActor(context.Background(), actorURL); err == nil {
		t.Fatal("cached an actor whose id names another host")
	}
	if len(w.repo.remoteActors) != 0 {
		t.Fatalf("cached: %+v", w.repo.remoteActors)
	}
}

func TestObjectFetchCrossHostRedirectIsRefused(t *testing.T) {
	w := newRedirectWorld(t, false)
	objURL := w.origin.URL + "/videos/watch/abc"
	w.originMux.Handle("/videos/watch/abc", redirectTo(w.other.URL+"/videos/watch/abc"))
	w.otherMux.Handle("/videos/watch/abc", serveJSON(`{"id":"`+objURL+`","type":"Video"}`))

	if _, err := w.svc.fetchVideoObject(context.Background(), objURL); !errors.Is(err, errCrossHostRedirect) {
		t.Fatalf("err = %v, want errCrossHostRedirect", err)
	}
	if n := w.otherHits.Load(); n != 0 {
		t.Fatalf("other host was contacted %d times", n)
	}
}

func TestFederationRedirectLoopLimitUnchanged(t *testing.T) {
	w := newRedirectWorld(t, false)
	w.originMux.Handle("/.well-known/webfinger", redirectTo(w.origin.URL+"/.well-known/webfinger"))

	if _, err := w.svc.fetchJRD(context.Background(), w.origin.URL+"/.well-known/webfinger"); !errors.Is(err, urlsafety.ErrTooManyRedirects) {
		t.Fatalf("err = %v, want urlsafety.ErrTooManyRedirects", err)
	}
}

type erroringBlockRepo struct{ fakeRepo }

func (erroringBlockRepo) IsInstanceBlocked(context.Context, string) (bool, error) {
	return false, errors.New("db down")
}

func TestRedirectHostBlockedCanonicalisesAndFailsClosed(t *testing.T) {
	repo := newFollowRepo(uuid.New())
	repo.blockedDomains = map[string]bool{"blocked.example": true, "xn--bcher-kva.example": true}
	svc := NewService(repo)
	for _, raw := range []string{
		"https://Blocked.Example./x",
		"https://blocked.example:8443/x",
		"https://bücher.example/x",
	} {
		u, _ := url.Parse(raw)
		if blocked, err := svc.redirectHostBlocked(context.Background(), u); err != nil || !blocked {
			t.Errorf("%s: blocked=%v err=%v, want blocked", raw, blocked, err)
		}
	}
	u, _ := url.Parse("https://fine.example/x")
	if blocked, _ := svc.redirectHostBlocked(context.Background(), u); blocked {
		t.Error("fine.example reported blocked")
	}

	failing := NewService(erroringBlockRepo{repo})
	policy := failing.redirectPolicy(nil)
	from, _ := http.NewRequest(http.MethodGet, "https://a.example/", nil)
	to, _ := http.NewRequest(http.MethodGet, "https://b.example/", nil)
	if err := policy(to, []*http.Request{from}); !errors.Is(err, errRedirectToBlockedHost) {
		t.Fatalf("lookup error: err = %v, want the hop refused", err)
	}
}
