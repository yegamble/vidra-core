package atproto

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTXT returns a TXT resolver that answers the given records for
// `_atproto.<handle>` and errors for anything else (mirroring NXDOMAIN).
func fakeTXT(records map[string][]string) func(ctx context.Context, name string) ([]string, error) {
	return func(_ context.Context, name string) ([]string, error) {
		if recs, ok := records[name]; ok {
			return recs, nil
		}
		return nil, errors.New("no such host")
	}
}

func TestResolveHandleDNSAndWellKnown(t *testing.T) {
	// Well-known server serves a DID for the HTTPS-method branch.
	wk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/atproto-did" {
			_, _ = w.Write([]byte("did:plc:fromhttp"))
			return
		}
		http.NotFound(w, r)
	}))
	defer wk.Close()

	t.Run("dns wins on conflict", func(t *testing.T) {
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto.alice.example": {"did=did:plc:fromdns"}})),
			WithHandleWellKnownBase(wk.URL),
		)
		did, err := c.ResolveHandle(context.Background(), "alice.example")
		if err != nil || did != "did:plc:fromdns" {
			t.Fatalf("ResolveHandle = %q, %v; want did:plc:fromdns (DNS precedence)", did, err)
		}
	})

	t.Run("falls back to well-known when no TXT", func(t *testing.T) {
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(nil)),
			WithHandleWellKnownBase(wk.URL),
		)
		did, err := c.ResolveHandle(context.Background(), "alice.example")
		if err != nil || did != "did:plc:fromhttp" {
			t.Fatalf("ResolveHandle = %q, %v; want did:plc:fromhttp", did, err)
		}
	})

	t.Run("neither method resolves", func(t *testing.T) {
		empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
		defer empty.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(nil)),
			WithHandleWellKnownBase(empty.URL),
		)
		if _, err := c.ResolveHandle(context.Background(), "alice.example"); !errors.Is(err, ErrHandleNotFound) {
			t.Fatalf("err = %v, want ErrHandleNotFound", err)
		}
	})

	t.Run("invalid TXT did ignored", func(t *testing.T) {
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto.alice.example": {"did=not-a-did"}})),
			WithHandleWellKnownBase(wk.URL),
		)
		// The garbage TXT is skipped; the well-known method wins.
		did, err := c.ResolveHandle(context.Background(), "alice.example")
		if err != nil || did != "did:plc:fromhttp" {
			t.Fatalf("ResolveHandle = %q, %v; want did:plc:fromhttp", did, err)
		}
	})

	t.Run("well-known body cap", func(t *testing.T) {
		big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 9 KiB of padding before anything a DID could parse from: past the
			// 8 KiB cap the read is truncated to non-DID bytes → ignored.
			_, _ = w.Write([]byte(strings.Repeat("x", 9<<10) + "did:plc:hidden"))
		}))
		defer big.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(nil)),
			WithHandleWellKnownBase(big.URL),
		)
		if _, err := c.ResolveHandle(context.Background(), "alice.example"); !errors.Is(err, ErrHandleNotFound) {
			t.Fatalf("err = %v, want ErrHandleNotFound (oversized body rejected)", err)
		}
	})
}

// didDocJSON builds a DID document body.
func didDocJSON(id, handle, pdsEndpoint string) string {
	doc := didDocument{
		ID:          id,
		AlsoKnownAs: []string{"at://" + handle},
		Service: []didDocumentSvc{{
			ID:              "#atproto_pds",
			Type:            "AtprotoPersonalDataServer",
			ServiceEndpoint: pdsEndpoint,
		}},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func TestResolveIdentityPLCAndBidirectional(t *testing.T) {
	const did = "did:plc:abc123"
	const handle = "alice.example"
	const pds = "https://pds.example"

	t.Run("happy path", func(t *testing.T) {
		plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/"+did {
				_, _ = w.Write([]byte(didDocJSON(did, handle, pds)))
				return
			}
			http.NotFound(w, r)
		}))
		defer plc.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto." + handle: {"did=" + did}})),
			WithPLCURL(plc.URL),
		)
		id, err := c.ResolveIdentity(context.Background(), "@Alice.Example")
		if err != nil {
			t.Fatalf("ResolveIdentity: %v", err)
		}
		if id.DID != did || id.Handle != handle || id.PDSURL != pds {
			t.Errorf("identity = %+v; want did=%s handle=%s pds=%s", id, did, handle, pds)
		}
	})

	t.Run("alsoKnownAs must claim the handle", func(t *testing.T) {
		plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Document claims a DIFFERENT handle → not authoritative for ours.
			_, _ = w.Write([]byte(didDocJSON(did, "someone-else.example", pds)))
		}))
		defer plc.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto." + handle: {"did=" + did}})),
			WithPLCURL(plc.URL),
		)
		if _, err := c.ResolveIdentity(context.Background(), handle); !errors.Is(err, ErrDIDMismatch) {
			t.Fatalf("err = %v, want ErrDIDMismatch", err)
		}
	})

	t.Run("document id must equal the DID", func(t *testing.T) {
		plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(didDocJSON("did:plc:someoneelse", handle, pds)))
		}))
		defer plc.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto." + handle: {"did=" + did}})),
			WithPLCURL(plc.URL),
		)
		if _, err := c.ResolveIdentity(context.Background(), handle); !errors.Is(err, ErrDIDMismatch) {
			t.Fatalf("err = %v, want ErrDIDMismatch", err)
		}
	})

	t.Run("missing PDS service", func(t *testing.T) {
		plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"` + did + `","alsoKnownAs":["at://` + handle + `"],"service":[]}`))
		}))
		defer plc.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto." + handle: {"did=" + did}})),
			WithPLCURL(plc.URL),
		)
		if _, err := c.ResolveIdentity(context.Background(), handle); !errors.Is(err, ErrPDSNotFound) {
			t.Fatalf("err = %v, want ErrPDSNotFound", err)
		}
	})

	t.Run("did-doc body cap", func(t *testing.T) {
		plc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Valid JSON prefix, but the closing brace is pushed past the 512 KiB
			// cap → truncated read → unparseable → rejected.
			_, _ = w.Write([]byte(`{"id":"` + did + `","alsoKnownAs":["` + strings.Repeat("a", 600<<10) + `"]}`))
		}))
		defer plc.Close()
		c := NewOAuthClient(
			WithOAuthClientAllowPrivate(true),
			WithTXTResolver(fakeTXT(map[string][]string{"_atproto." + handle: {"did=" + did}})),
			WithPLCURL(plc.URL),
		)
		if _, err := c.ResolveIdentity(context.Background(), handle); !errors.Is(err, ErrDIDMismatch) {
			t.Fatalf("err = %v, want ErrDIDMismatch (oversized doc rejected)", err)
		}
	})
}

func TestResolveIdentityDIDWeb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/did.json" {
			http.NotFound(w, r)
			return
		}
		host := r.Host // e.g. 127.0.0.1:PORT
		did := "did:web:" + strings.ReplaceAll(host, ":", "%3A")
		_, _ = w.Write([]byte(didDocJSON(did, "web.example", "https://pds.web.example")))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	did := "did:web:" + strings.ReplaceAll(host, ":", "%3A")
	c := NewOAuthClient(
		WithOAuthClientAllowPrivate(true),
		WithTXTResolver(fakeTXT(map[string][]string{"_atproto.web.example": {"did=" + did}})),
	)
	id, err := c.ResolveIdentity(context.Background(), "web.example")
	if err != nil {
		t.Fatalf("ResolveIdentity(did:web): %v", err)
	}
	if id.DID != did || id.PDSURL != "https://pds.web.example" {
		t.Errorf("identity = %+v", id)
	}
}

func TestDIDWebHostAndUnsupported(t *testing.T) {
	if h, err := didWebHost("did:web:example.com"); err != nil || h != "example.com" {
		t.Errorf("didWebHost(bare) = %q, %v", h, err)
	}
	if h, err := didWebHost("did:web:127.0.0.1%3A8080"); err != nil || h != "127.0.0.1:8080" {
		t.Errorf("didWebHost(port) = %q, %v", h, err)
	}
	// Path-form did:web (literal colon) is unsupported.
	if _, err := didWebHost("did:web:example.com:user:alice"); !errors.Is(err, ErrUnsupportedDID) {
		t.Errorf("path-form did:web err = %v, want ErrUnsupportedDID", err)
	}

	c := NewOAuthClient(WithOAuthClientAllowPrivate(true), WithTXTResolver(fakeTXT(map[string][]string{
		"_atproto.alice.example": {"did=did:example:unsupported"},
	})))
	if _, err := c.ResolveIdentity(context.Background(), "alice.example"); !errors.Is(err, ErrUnsupportedDID) {
		t.Errorf("unsupported DID method err = %v, want ErrUnsupportedDID", err)
	}
}

func TestValidDIDSyntax(t *testing.T) {
	valid := []string{"did:plc:abc", "did:web:example.com"}
	invalid := []string{"", "did:", "did:plc:", "notadid", "did::x", strings.Repeat("did:plc:", 400)}
	for _, d := range valid {
		if !validDIDSyntax(d) {
			t.Errorf("validDIDSyntax(%q) = false, want true", d)
		}
	}
	for _, d := range invalid {
		if validDIDSyntax(d) {
			t.Errorf("validDIDSyntax(%q) = true, want false", d)
		}
	}
}
