package audit

import (
	"math"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
)

func TestCatalogControlAuditBindsOnlyTypedRequestFences(t *testing.T) {
	actor := ActorSnapshot{Kind: "user", ID: uuid.NewString(), Role: "admin"}
	epoch, predecessor := uuid.New(), uuid.New()
	r := catalog.BootstrapRequest{Scope: "local", Epoch: epoch, ExpectedEpoch: &predecessor}
	ev, err := CatalogBootstrapEvent(actor, r, false, "accepted")
	if err != nil || ev.Action != "search.catalog.bootstrap" || ev.Result != "success" ||
		ev.ResourceType != "search_catalog" || ev.ResourceID != epoch.String() || ev.Actor != actor ||
		len(ev.Changes) != 1 || ev.Changes[0] != (Change{Field: "catalog_epoch", Before: predecessor.String(), After: epoch.String()}) {
		t.Fatalf("bootstrap audit: %+v/%v", ev, err)
	}
	metadata := func(e Event) map[string]string {
		t.Helper()
		if _, err := normalizeEvent(e); err != nil {
			t.Fatal(err)
		}
		m := make(map[string]string)
		for _, f := range e.Metadata {
			m[f.Key] = f.Value
		}
		return m
	}
	m := metadata(ev)
	if m["catalog_scope"] != "local" || m["catalog_source_predecessor"] != predecessor.String() ||
		m["catalog_receiver_predecessor"] != "null" || m["coalesced"] != "false" || m["outcome"] != "accepted" {
		t.Fatalf("bootstrap metadata: %v", m)
	}
	ev, err = CatalogBootstrapEvent(actor, r, true, "accepted")
	if err != nil || len(ev.Changes) != 0 || metadata(ev)["coalesced"] != "true" {
		t.Fatalf("coalesced retry invented a change: %+v/%v", ev, err)
	}
	ev, err = CatalogBootstrapEvent(actor, r, false, "catalog_conflict")
	if err != nil || ev.Result != "failure" || len(ev.Changes) != 0 {
		t.Fatalf("conflict audit: %+v/%v", ev, err)
	}
	full := catalog.FullRequest{Scope: "local", ExpectedEpoch: epoch, ExpectedAllocatedGeneration: math.MaxInt64}
	ev, err = CatalogFullRequestEvent(actor, full, true, "accepted")
	m = metadata(ev)
	if err != nil || ev.Action != "search.catalog.full_requested" || m["catalog_expected_generation"] != strconv.FormatInt(math.MaxInt64, 10) ||
		m["catalog_required_after_generation"] != m["catalog_expected_generation"] || m["coalesced"] != "true" {
		t.Fatalf("full audit lost exact generation: %+v/%v", ev, err)
	}
	full.ExpectedAllocatedGeneration = 0
	ev, err = CatalogFullRequestEvent(actor, full, false, "catalog_recovery_required")
	if err != nil || metadata(ev)["catalog_expected_generation"] != "0" || metadata(ev)["catalog_required_after_generation"] != "" {
		t.Fatalf("refused full claimed an accepted threshold: %+v/%v", ev, err)
	}
}

func TestCatalogControlAuditRefusesInvalidActorRequestAndOutcome(t *testing.T) {
	r := catalog.BootstrapRequest{Scope: "local", Epoch: uuid.New()}
	actor := ActorSnapshot{Kind: "user", ID: uuid.NewString(), Role: "admin"}
	for _, code := range []string{"", "upstream credential", "https://provider.example/private"} {
		if ev, err := CatalogBootstrapEvent(actor, r, false, code); err == nil || ev.Action != "" {
			t.Fatalf("unsafe outcome accepted: %+v/%v", ev, err)
		}
	}
	if _, err := CatalogBootstrapEvent(actor, r, true, "catalog_conflict"); err == nil {
		t.Fatal("failed operation cannot coalesce")
	}
	if _, err := CatalogBootstrapEvent(ActorSnapshot{Kind: "user", ID: "invalid"}, r, false, "accepted"); err == nil {
		t.Fatal("invalid actor accepted")
	}
	r.Epoch = uuid.Nil
	if _, err := CatalogBootstrapEvent(actor, r, false, "accepted"); err == nil {
		t.Fatal("invalid bootstrap request accepted")
	}
	if _, err := CatalogFullRequestEvent(actor, catalog.FullRequest{}, false, "accepted"); err == nil {
		t.Fatal("invalid full request accepted")
	}
}
