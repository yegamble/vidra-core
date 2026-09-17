package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
)

type fakeIPFSControl struct {
	doc       ipfscontrol.Document
	err       error
	writes    int
	operation ipfscontrol.HostOperation
}

func (f *fakeIPFSControl) Config(context.Context) (ipfscontrol.Document, error) { return f.doc, f.err }
func (f *fakeIPFSControl) Save(_ context.Context, expected int64, c ipfscontrol.Config, _ uuid.UUID) (ipfscontrol.Document, error) {
	f.writes++
	if expected != f.doc.Revision {
		return f.doc, ipfscontrol.ErrConflict
	}
	f.doc.Config = c
	f.doc.Revision++
	return f.doc, f.err
}
func (f *fakeIPFSControl) Request(context.Context, string, int64, uuid.UUID, uuid.UUID) (ipfscontrol.HostOperation, error) {
	f.writes++
	return f.operation, f.err
}
func (f *fakeIPFSControl) Runtime(context.Context) (ipfscontrol.Runtime, error) {
	return ipfscontrol.Runtime{ConfigRevision: f.doc.Revision, Management: ipfscontrol.Management{Mode: "unavailable"}}, f.err
}

func TestIPFSControlAuthConflictAndStrictDocument(t *testing.T) {
	c := ipfscontrol.Config{Provider: "internal", BudgetBytes: 20 << 30, MinFreeBytes: 20 << 30, CopyBytesPerSecond: 2 << 20, Workers: 1}
	fake := &fakeIPFSControl{doc: ipfscontrol.Document{Revision: 1, Config: c}}
	auditRepo := &httpAuditFakeRepo{}
	srv := ipfsServer(t, testConfig(), WithIPFSControl(fake), WithAuditLog(audit.NewService(auditRepo)))
	admin := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	member := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	path := "/api/v1/admin/ipfs/config"
	if r := getWithAuth(srv, path, ""); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := getWithAuth(srv, path, member); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := getWithAuth(srv, path, admin); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := getWithAuth(srv, "/api/v1/ipfs/status", admin); r.Code != 200 {
		t.Fatal("disabled managed status inaccessible", r.Code)
	}
	b, _ := json.Marshal(map[string]any{"expected_revision": 0, "config": c})
	if r := sendJSONAuth(srv, http.MethodPatch, path, string(b), admin); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	writes := fake.writes
	for _, body := range []string{`{"expected_revision":1,"config":{}}`, `{"expected_revision":1,"config":null}`, `{"expected_revision":1,"config":{},"command":"docker rm"}`} {
		if r := sendJSONAuth(srv, http.MethodPatch, path, body, admin); r.Code != 400 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if fake.writes != writes {
		t.Fatal("invalid document reached service")
	}
	b, _ = json.Marshal(map[string]any{"expected_revision": 1, "config": c})
	if r := sendJSONAuth(srv, http.MethodPatch, path, string(b), admin); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	found := false
	for _, row := range auditRepo.rows {
		if row.Action == "admin.ipfs.config.update" && row.Result == "success" {
			found = true
		}
	}
	if !found {
		t.Fatal("configuration save was not audited")
	}
	fake.err = ipfscontrol.ErrExternal
	if r := postJSONWithAuth(srv, "/api/v1/admin/ipfs/restart", admin, `{"expected_revision":2,"request_id":"a5b039ce-10ee-45b0-a01b-462d8174e63b"}`); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
}
