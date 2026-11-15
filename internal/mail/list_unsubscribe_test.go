package mail

import (
	"context"
	"net/http"
	"strings"
	"testing"

	netmail "net/mail"
)

const unsubURL = "https://vidra.example/api/v1/notifications/unsubscribe?token=abc.def"

// wantHeaderPair is what RFC 8058 requires on one-click mail: BOTH headers, the
// URL in angle brackets, the post header with the exact one-click token.
const (
	wantListUnsubscribe     = "<" + unsubURL + ">"
	wantListUnsubscribePost = "List-Unsubscribe=One-Click"
)

func notificationMessage() Message {
	m := providerMessage()
	m.ListUnsubscribe = unsubURL
	return m
}

// Every transport must put the pair on the wire when the message asks for it
// and NOT otherwise (transactional mail must never carry it: a mailbox
// provider's "unsubscribe" button on a password reset would be a way to
// suppress security mail).
func TestListUnsubscribeHeadersPerTransport(t *testing.T) {
	for _, withHeader := range []bool{true, false} {
		m := providerMessage()
		if withHeader {
			m = notificationMessage()
		}
		name := map[bool]string{true: "with", false: "without"}[withHeader]

		t.Run("resend/"+name, func(t *testing.T) {
			srv := newProviderServer(t, http.StatusOK, `{"id":"x"}`)
			if err := newResendTransport("k", collectTransportOptions(srv.opts())).Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			h, _ := decodeJSON(t, srv.only().body)["headers"].(map[string]any)
			checkPair(t, withHeader, func(k string) string { v, _ := h[k].(string); return v })
		})
		t.Run("brevo/"+name, func(t *testing.T) {
			srv := newProviderServer(t, http.StatusCreated, `{"messageId":"x"}`)
			if err := newBrevoTransport("k", collectTransportOptions(srv.opts())).Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			h, _ := decodeJSON(t, srv.only().body)["headers"].(map[string]any)
			checkPair(t, withHeader, func(k string) string { v, _ := h[k].(string); return v })
		})
		t.Run("postmark/"+name, func(t *testing.T) {
			srv := newProviderServer(t, http.StatusOK, `{"ErrorCode":0,"MessageID":"x"}`)
			if err := newPostmarkTransport("", "k", collectTransportOptions(srv.opts())).Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			list, _ := decodeJSON(t, srv.only().body)["Headers"].([]any)
			for _, e := range list {
				kv := e.(map[string]any)
				got[kv["Name"].(string)], _ = kv["Value"].(string)
			}
			checkPair(t, withHeader, func(k string) string { return got[k] })
		})
		t.Run("mailgun/"+name, func(t *testing.T) {
			srv := newProviderServer(t, http.StatusOK, `{"id":"x"}`)
			if err := newMailgunTransport("mail.vidra.test", RegionUS, "k", collectTransportOptions(srv.opts())).Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			req := srv.only()
			form := parseMultipart(t, req.contentType, req.body)
			checkPair(t, withHeader, func(k string) string { return form["h:"+k] })
		})
		t.Run("smtp/"+name, func(t *testing.T) {
			raw := string(render(m))
			got := func(k string) string {
				for _, line := range strings.Split(raw, "\r\n") {
					if v, ok := strings.CutPrefix(line, k+": "); ok {
						return v
					}
				}
				return ""
			}
			checkPair(t, withHeader, got)
		})
	}
}

func checkPair(t *testing.T, want bool, get func(string) string) {
	t.Helper()
	lu, post := get("List-Unsubscribe"), get("List-Unsubscribe-Post")
	if !want {
		if lu != "" || post != "" {
			t.Errorf("transactional message carries List-Unsubscribe=%q Post=%q", lu, post)
		}
		return
	}
	if lu != wantListUnsubscribe {
		t.Errorf("List-Unsubscribe = %q, want %q", lu, wantListUnsubscribe)
	}
	if post != wantListUnsubscribePost {
		t.Errorf("List-Unsubscribe-Post = %q, want %q", post, wantListUnsubscribePost)
	}
}

// The URL goes into a header verbatim, so every transport refuses one that is
// not a single https URL: a CRLF would forge headers, an angle bracket would
// break out of the <...> wrapper, and a non-https scheme (mailto:, http:) is
// not what RFC 8058 allows for one-click.
func TestListUnsubscribeURLIsValidated(t *testing.T) {
	for name, bad := range map[string]string{
		"crlf":        "https://vidra.example/u\r\nBcc: x@y.test",
		"angle":       "https://vidra.example/u>, <mailto:x@y.test",
		"http":        "http://vidra.example/u",
		"mailto":      "mailto:unsub@vidra.example",
		"space":       "https://vidra.example/u v",
		"no host":     "https:///u",
		"comma list":  "https://a.example/u, https://b.example/u",
		"javascript":  "javascript:alert(1)",
		"only scheme": "https://",
	} {
		m := providerMessage()
		m.ListUnsubscribe = bad
		if err := validateMessage(m); err == nil {
			t.Errorf("%s: %q accepted", name, bad)
		}
	}
	m := providerMessage()
	m.ListUnsubscribe = unsubURL
	if err := validateMessage(m); err != nil {
		t.Errorf("good URL rejected: %v", err)
	}
}

func unsubComposer(rec *recordingTransport, fn func(userID string) string) *Composer {
	return NewComposer(ComposerConfig{InstanceName: "Vidra Test", PublicBaseURL: "https://vidra.example"},
		NewStaticResolver(rec, netmail.Address{Address: "no-reply@vidra.test"}, ""),
		WithUnsubscribeURLFunc(fn))
}

// Notification mail (the only opt-out-able mail) gets a per-user URL; every
// transactional message goes through the same send seam and gets none.
func TestComposerSetsListUnsubscribeOnNotificationMailOnly(t *testing.T) {
	rec := &recordingTransport{}
	c := unsubComposer(rec, func(id string) string { return "https://vidra.example/u?t=" + id })
	ctx := context.Background()

	if err := c.SendNotificationEmail(ctx, "ada@example.test", NotificationEmail{
		Type: "comment", UserID: "user-1", ActorName: "bob", VideoID: "v", VideoTitle: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := rec.msgs[0].ListUnsubscribe; got != "https://vidra.example/u?t=user-1" {
		t.Errorf("notification ListUnsubscribe = %q", got)
	}

	rec.msgs = nil
	for name, send := range map[string]func() error{
		"password reset":     func() error { return c.SendPasswordReset(ctx, "ada@example.test", "tok") },
		"email verification": func() error { return c.SendEmailVerification(ctx, "ada@example.test", "tok") },
		"email change":       func() error { return c.SendEmailChangeVerification(ctx, "ada@example.test", "tok") },
		"email changed":      func() error { return c.SendEmailChanged(ctx, "ada@example.test", "new@example.test") },
		"password changed":   func() error { return c.SendPasswordChanged(ctx, "ada@example.test") },
		"test message":       func() error { return c.SendTest(ctx, "ada@example.test") },
	} {
		if err := send(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(rec.msgs) != 6 {
		t.Fatalf("sent %d, want 6", len(rec.msgs))
	}
	for i, m := range rec.msgs {
		if m.ListUnsubscribe != "" {
			t.Errorf("transactional message %d carries ListUnsubscribe %q", i, m.ListUnsubscribe)
		}
	}
}

// No signer wired, no user id, or no public origin: no header rather than a
// broken or guessed one.
func TestComposerOmitsListUnsubscribeWhenItCannotBuildOne(t *testing.T) {
	ctx := context.Background()
	n := NotificationEmail{Type: "comment", UserID: "u1", ActorName: "bob", VideoID: "v", VideoTitle: "t"}

	rec := &recordingTransport{}
	if err := notificationComposer(rec, "https://vidra.example").SendNotificationEmail(ctx, "ada@example.test", n); err != nil {
		t.Fatal(err)
	}
	noID := n
	noID.UserID = ""
	if err := unsubComposer(rec, func(string) string { return "https://x.example/u" }).SendNotificationEmail(ctx, "ada@example.test", noID); err != nil {
		t.Fatal(err)
	}
	if err := unsubComposer(rec, func(string) string { return "" }).SendNotificationEmail(ctx, "ada@example.test", n); err != nil {
		t.Fatal(err)
	}
	// An http:// origin (a dev instance) cannot be one-click; the mail must still
	// go, just without the header.
	if err := unsubComposer(rec, func(string) string { return "http://localhost:8080/u" }).SendNotificationEmail(ctx, "ada@example.test", n); err != nil {
		t.Fatal(err)
	}
	if len(rec.msgs) != 4 {
		t.Fatalf("sent %d messages, want 4", len(rec.msgs))
	}
	for i, m := range rec.msgs {
		if m.ListUnsubscribe != "" {
			t.Errorf("message %d carries ListUnsubscribe %q", i, m.ListUnsubscribe)
		}
	}
}
