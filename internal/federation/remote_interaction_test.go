package federation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// The service's REAL guarded client (no WithFetchClient) must refuse to
// connect to a name that resolves to loopback. The server speaks TLS so a
// plain http.Client would fail differently (certificate error, not a blocked
// address): asserting ErrBlockedAddress proves the dial-time guard ran.
func TestResolveSubscribeURLRealClientRefusesLoopbackAtConnectTime(t *testing.T) {
	hits := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer ts.Close()
	_, port, _ := strings.Cut(strings.TrimPrefix(ts.URL, "https://"), ":")

	svc := NewService(newActorRepo(), WithBaseURL("https://videos.example"))
	_, err := svc.ResolveSubscribeURL(context.Background(), "bob@localhost:"+port, "https://videos.example/video-channels/films")
	if !errors.Is(err, urlsafety.ErrBlockedAddress) {
		t.Fatalf("err = %v, want ErrBlockedAddress from the dial-time guard", err)
	}
	if hits != 0 {
		t.Errorf("loopback server was contacted %d times", hits)
	}
}
