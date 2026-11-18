package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/httpsig"
)

// TestInboxAnswers429WhenTheActorFetchBudgetIsSpent: an inbox POST whose signer
// could not be fetched because the inbound-path fetch budget is spent is told
// "later" — 429 with Retry-After and no-store — not the 401 a bad signature
// gets. A peer that reads 401 has no way to tell "retry" from "rejected", and
// PeerTube charges every non-2xx against the follow.
//
// Every request names a fresh actor on one host, so each spends that host's
// budget; the actor documents 404, so the requests that are admitted fail as an
// ordinary fetch failure — still 401, with no Retry-After.
func TestInboxAnswers429WhenTheActorFetchBudgetIsSpent(t *testing.T) {
	peer := httptest.NewServer(http.NotFoundHandler())
	defer peer.Close()

	cfg := fedTestConfig()
	repo := newFedRepoFor(cfg)
	svc := federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL), federation.WithAllowPrivateFetch(true))
	srv := New(cfg, nil, nil, WithFederationService(svc))
	key, _ := signerKeyPEM(t)

	post := func(i int) *httptest.ResponseRecorder {
		actor := peer.URL + "/accounts/a" + strconv.Itoa(i)
		body := []byte(`{"id":"` + actor + `/act/1","type":"Follow","actor":"` + actor +
			`","object":"https://videos.example/video-channels/films"}`)
		req := httptest.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
		if err := (httpsig.Signer{KeyID: actor + "#main-key", Priv: key}).Sign(req, body); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	var refused *httptest.ResponseRecorder
	for i := 0; i < 50 && refused == nil; i++ {
		rec := post(i)
		switch rec.Code {
		case http.StatusUnauthorized:
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				t.Fatalf("request %d: a fetch failure carries Retry-After %q", i, ra)
			}
		case http.StatusTooManyRequests:
			refused = rec
		default:
			t.Fatalf("request %d: status = %d, want 401 or 429; body=%s", i, rec.Code, rec.Body.String())
		}
	}
	if refused == nil {
		t.Fatal("no request was refused for budget: want 429 once the host budget is spent")
	}
	if got := refused.Header().Get("Retry-After"); got != inboxFetchBudgetRetryAfter {
		t.Errorf("Retry-After = %q, want %q", got, inboxFetchBudgetRetryAfter)
	}
	if got := refused.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
