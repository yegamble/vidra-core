package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestDeliveryHonorsProfileWithdrawal(t *testing.T) {
	ctx := context.Background()
	const base = "https://videos.example"
	repo := newActorRepo()
	repo.deliveries = map[uuid.UUID]*fakeDelivery{}
	ada := repo.usersByName["ada"].ID
	var received [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received = append(received, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	svc := NewService(repo, WithBaseURL(base), WithAllowPrivateFetch(true))
	group := base + "/video-channels/films"
	old := base + "/accounts/ada"
	video, _ := json.Marshal(map[string]any{"type": "Create", "object": map[string]any{"type": "Video", "attributedTo": []AttributedItem{{Type: "Group", ID: group}, {Type: "Person", ID: old}}}})
	if err := svc.enqueueChannelDelivery(ctx, repo.channels["films"].ID, "films", srv.URL, video); err != nil {
		t.Fatal(err)
	}
	if err := svc.enqueueAccountDelivery(ctx, ada, "ada", srv.URL, []byte(`{"type":"Follow"}`)); err != nil {
		t.Fatal(err)
	}
	// The queued bytes predate the preference change.
	delete(repo.usersByName, "ada")
	delete(repo.usersByID, ada)
	n, err := svc.DrainDeliveries(ctx, 10)
	if err != nil || n != 1 || len(received) != 1 {
		t.Fatalf("received %d, delivered %d, error %v", len(received), n, err)
	}
	for _, d := range repo.deliveries {
		if d.state == "delivered" && string(d.row.Payload) != string(received[0]) {
			t.Error("receipt differs from the bytes sent")
		}
	}
	if strings.Contains(string(received[0]), old) || !strings.Contains(string(received[0]), group) {
		t.Error("expected channel-only attribution")
	}
}

func TestCurrentProfilePayloadKeepsPublicAndRemoteMentions(t *testing.T) {
	const base = "https://videos.example"
	svc := NewService(newActorRepo(), WithBaseURL(base))
	payload := []byte(`{"type":"Update","object":{"type":"Note","content":"authored text","n":9007199254740993,"tag":[{"type":"Mention","href":"https://videos.example/accounts/ada"},{"type":"Mention","href":"https://videos.example/accounts/withdrawn"},{"type":"Mention","href":"https://elsewhere.example/accounts/elsewhere"}]}}`)
	updated, err := svc.currentProfilePayload(context.Background(), sqlcgen.ClaimDueDeliveriesRow{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	body := string(updated)
	for _, want := range []string{"/accounts/ada", "/accounts/elsewhere", "authored text", "9007199254740993"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "withdrawn") {
		t.Error("withdrawn mention retained")
	}
}
