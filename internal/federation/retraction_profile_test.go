//go:build integration

package federation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/federation"
)

// A known inbox can remove a previously disclosed item after the Person opts out.
// An inbox that saw another object, or only has a pending send, cannot.
func TestPrivateAccountRetractionsOnlyReachKnownRecipient(t *testing.T) {
	ctx, st := integrationStore(t)
	q := st.Queries()
	u := seedUser(t, ctx, q, uuid.NewString()[:8])
	const base = "https://videos.example"
	actor := base + "/accounts/" + u.Username
	subject := base + "/comments/" + uuid.NewString()
	var received []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/known" {
			t.Error("new recipient got a retraction")
		}
		received = append(received, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	put := func(inbox string, body any, delivered bool) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID

		if err := st.Pool.QueryRow(ctx, `INSERT INTO federation_deliveries(inbox_url,payload,signing_user_id,signing_username,state) VALUES($1,$2,$3,$4,$5) RETURNING id`, inbox, payload, u.ID, u.Username, map[bool]string{true: "delivered", false: "pending"}[delivered]).Scan(&id); err != nil {
			t.Fatal(err)
		}
	}
	previous := map[string]any{"id": actor + "/activities/create/" + uuid.NewString(), "type": "Create", "actor": actor, "object": map[string]any{"id": subject, "type": "Note", "content": "private text"}}
	put(srv.URL+"/known", previous, true)
	for _, path := range []string{"/known", "/new"} {
		put(srv.URL+path, map[string]any{"id": actor + "/activities/delete/" + uuid.NewString(), "type": "Delete", "actor": actor, "object": subject, "summary": "private text"}, false)
	}
	followID := actor + "/activities/follow/" + uuid.NewString()
	target := "https://peer.example/actor?id=old&kind=channel"
	previousFollow := map[string]any{"id": followID, "type": "Follow", "actor": actor, "object": target}
	put(srv.URL+"/known", previousFollow, true)
	for _, path := range []string{"/known", "/new"} {
		put(srv.URL+path, map[string]any{"id": actor + "/activities/undo/" + uuid.NewString(), "type": "Undo", "actor": actor, "object": previousFollow}, false)
	}
	put(srv.URL+"/known", map[string]any{"id": actor + "/activities/delete/" + uuid.NewString(), "type": "Delete", "actor": actor, "object": base + "/comments/" + uuid.NewString()}, false)
	svc := federation.NewService(q, federation.WithBaseURL(base), federation.WithAllowPrivateFetch(true))
	_, err := svc.DrainDeliveries(ctx, 100)
	if err != nil || len(received) != 2 {
		t.Fatalf("retractions received=%d, err=%v", len(received), err)
	}
	for _, activity := range received {
		actual, _ := json.Marshal(activity)
		if bytes.Contains(actual, []byte("private text")) {
			t.Fatal("retraction contains private text")
		}
		switch activity["type"] {
		case "Delete":
			if activity["object"] != subject {
				t.Error("wrong deleted object")
			}
		case "Undo":
			inner, _ := activity["object"].(map[string]any)
			if inner["id"] != followID || inner["object"] != target {
				t.Error("wrong undone follow")
			}
		default:
			t.Error("unexpected activity")
		}
	}
}
