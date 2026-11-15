package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/unsubscribe"
)

func TestUnsubscribeURLFunc(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	id := uuid.New()
	got := unsubscribeURLFunc("https://vidra.example/", secret)(id.String())
	u, err := url.Parse(got)
	if err != nil || u.Scheme != "https" || u.Host != "vidra.example" || u.Path != httpapi.UnsubscribeURLPath {
		t.Fatalf("URL = %q (%v)", got, err)
	}
	if v, ok := unsubscribe.New([]byte(secret)).Verify(u.Query().Get("token")); !ok || v != id {
		t.Error("the built token does not verify")
	}
	for name, f := range map[string]func(string) string{
		"no origin": unsubscribeURLFunc("", secret),
		"no secret": unsubscribeURLFunc("https://vidra.example", ""),
	} {
		if f(id.String()) != "" {
			t.Errorf("%s: want no URL", name)
		}
	}
	if unsubscribeURLFunc("https://vidra.example", secret)("not-a-uuid") != "" || strings.Contains(got, " ") {
		t.Error("a bad user id must yield no URL")
	}
}
