package ipfscontrol

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostClientUsesOnlyConfiguredUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ipfs-host-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "manager.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	id := "a5b039ce-10ee-45b0-a01b-462d8174e63b"
	mode := "valid"
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "ipfs-manager" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected destination or credential")
		}
		switch mode {
		case "redirect":
			w.Header().Set("Location", "http://example.invalid/")
			w.WriteHeader(302)
		case "oversize":
			_, _ = w.Write([]byte(strings.Repeat(" ", 65537)))
		case "unknown":
			_, _ = w.Write([]byte(`{"protocol_version":1,"docker_socket":"/var/run/docker.sock"}`))
		case "secret":
			w.WriteHeader(500)
			_, _ = w.Write([]byte("SECRET diagnostic"))
		default:
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(HostStatus{ProtocolVersion: 1, ObservedState: "running", ObservedAt: time.Now()})
			} else {
				if r.URL.Path != "/v1/restart" {
					t.Error(r.URL.Path)
				}
				var e HostEnvelope
				if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
					t.Error(err)
				}
				if e.OperationID != id || e.Sequence != 3 || e.ConfigRevision != 2 {
					t.Errorf("bad envelope: %+v", e)
				}
				w.WriteHeader(202)
				_ = json.NewEncoder(w).Encode(HostOperationResponse{Operation: HostOperation{ID: id, Sequence: 3, ConfigRevision: 2, State: "pending"}})
			}
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	client, err := NewHostClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	env := HostEnvelope{ProtocolVersion: 1, OperationID: id, Sequence: 3, ConfigRevision: 2, Config: HostConfig{BudgetBytes: 1 << 30, CopyBytesPerSecond: 1 << 20, Workers: 1}}
	if _, err := client.Dispatch(context.Background(), "restart", env); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"redirect", "oversize", "unknown", "secret"} {
		mode = bad
		if _, err := client.Status(context.Background()); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe %s response: %v", bad, err)
		}
	}
	if _, err := client.Dispatch(context.Background(), "rm", env); err == nil {
		t.Fatal("arbitrary operation accepted")
	}
	env.Sequence = 0
	if _, err := client.Dispatch(context.Background(), "apply", env); err == nil {
		t.Fatal("unfenced operation accepted")
	}
	if _, err := NewHostClient("relative.sock"); err == nil {
		t.Fatal("relative socket accepted")
	}
}

func TestHostStatusRejectsUnfencedOperations(t *testing.T) {
	id := "a5b039ce-10ee-45b0-a01b-462d8174e63b"
	valid := HostStatus{ProtocolVersion: 1, ObservedState: "running", ObservedAt: time.Now()}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*HostStatus){
		func(s *HostStatus) { s.LastOperationSequence = 1 },
		func(s *HostStatus) { s.LastOperationID = &id },
		func(s *HostStatus) {
			s.Operation = &HostOperation{ID: id, Sequence: 1, ConfigRevision: 1, State: "succeeded"}
		},
	} {
		s := valid
		change(&s)
		if s.Validate() == nil {
			t.Fatal("inconsistent host operation accepted")
		}
	}
}
