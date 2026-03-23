// Package main implements a lightweight mock ActivityPub server for integration testing.
// It handles inbox, actor, WebFinger, and NodeInfo endpoints.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// inboxEntry records a received activity with signature metadata.
type inboxEntry struct {
	Activity     map[string]interface{} `json:"activity"`
	HasSignature bool                   `json:"has_signature"`
}

type server struct {
	mu           sync.Mutex
	inbox        []inboxEntry
	publicKeyPEM string
	actorBase    string
}

func newServer(actorBase string) *server {
	// Generate RSA keypair for the mock actor
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("failed to generate RSA key: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		log.Fatalf("failed to marshal public key: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))

	return &server{
		publicKeyPEM: pubPEM,
		actorBase:    actorBase,
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeActivityJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/activity+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newRouter() http.Handler {
	s := newServer("http://test.local")
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Shared inbox — accepts any Activity JSON
	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}
		var activity map[string]interface{}
		if err := json.Unmarshal(body, &activity); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		hasSignature := r.Header.Get("Signature") != ""
		entry := inboxEntry{
			Activity:     activity,
			HasSignature: hasSignature,
		}
		s.mu.Lock()
		s.inbox = append(s.inbox, entry)
		s.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})

	// Actor endpoint
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		username := strings.TrimPrefix(r.URL.Path, "/users/")
		if username == "" {
			http.NotFound(w, r)
			return
		}
		actorURL := fmt.Sprintf("%s/users/%s", s.actorBase, username)
		actor := map[string]interface{}{
			"@context": []string{
				"https://www.w3.org/ns/activitystreams",
				"https://w3id.org/security/v1",
			},
			"type":              "Person",
			"id":                actorURL,
			"preferredUsername": username,
			"name":              username,
			"inbox":             actorURL + "/inbox",
			"outbox":            actorURL + "/outbox",
			"followers":         actorURL + "/followers",
			"following":         actorURL + "/following",
			"publicKey": map[string]interface{}{
				"id":           actorURL + "#main-key",
				"owner":        actorURL,
				"publicKeyPem": s.publicKeyPEM,
			},
		}
		writeActivityJSON(w, http.StatusOK, actor)
	})

	// WebFinger endpoint
	mux.HandleFunc("/.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		resource := r.URL.Query().Get("resource")
		if resource == "" {
			http.Error(w, "resource parameter required", http.StatusBadRequest)
			return
		}
		// Parse acct:user@domain
		username := "testuser"
		if strings.HasPrefix(resource, "acct:") {
			parts := strings.SplitN(strings.TrimPrefix(resource, "acct:"), "@", 2)
			if len(parts) > 0 {
				username = parts[0]
			}
		}
		actorURL := fmt.Sprintf("%s/users/%s", s.actorBase, username)
		jrd := map[string]interface{}{
			"subject": resource,
			"links": []map[string]interface{}{
				{
					"rel":  "self",
					"type": "application/activity+json",
					"href": actorURL,
				},
			},
		}
		w.Header().Set("Content-Type", "application/jrd+json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(jrd)
	})

	// NodeInfo discovery
	mux.HandleFunc("/.well-known/nodeinfo", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]interface{}{
			"links": []map[string]interface{}{
				{
					"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
					"href": s.actorBase + "/nodeinfo/2.0",
				},
			},
		}
		writeJSON(w, http.StatusOK, info)
	})

	// NodeInfo schema
	mux.HandleFunc("/nodeinfo/2.0", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]interface{}{
			"version": "2.0",
			"software": map[string]interface{}{
				"name":    "vidra-mock-activitypub",
				"version": "0.1.0",
			},
			"protocols":         []string{"activitypub"},
			"usage":             map[string]interface{}{"users": map[string]int{"total": 1}},
			"openRegistrations": false,
		}
		writeJSON(w, http.StatusOK, info)
	})

	// Debug: list received inbox activities with signature info
	mux.HandleFunc("/test/inbox", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		entries := make([]inboxEntry, len(s.inbox))
		copy(entries, s.inbox)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, entries)
	})

	return mux
}

func main() {
	port := "8080"
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      newRouter(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Printf("Mock ActivityPub server listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
