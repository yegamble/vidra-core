package audit

import (
	"strconv"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/catalog"
)

// CatalogBootstrapEvent records a typed CAS tuple, never document content or
// upstream output. The control transaction must persist it with accepted intent.
func CatalogBootstrapEvent(actor ActorSnapshot, r catalog.BootstrapRequest, coalesced bool, outcome string) (Event, error) {
	if err := r.Validate(); err != nil {
		return Event{}, err
	}
	ev, err := catalogControlEvent(actor, r.Epoch, coalesced, outcome)
	if err != nil {
		return Event{}, err
	}
	ev.Action = "search.catalog.bootstrap"
	predecessor := func(id *uuid.UUID) string {
		if id == nil {
			return "null"
		}
		return id.String()
	}
	ev.Metadata = append(ev.Metadata,
		MetadataField{"catalog_source_predecessor", predecessor(r.ExpectedEpoch)},
		MetadataField{"catalog_receiver_predecessor", predecessor(r.ExpectedReceiverEpoch)})
	if outcome == "accepted" && !coalesced {
		before := ""
		if r.ExpectedEpoch != nil {
			before = r.ExpectedEpoch.String()
		}
		ev.Changes = []Change{{Field: "catalog_epoch", Before: before, After: r.Epoch.String()}}
	}
	return validatedCatalogEvent(ev)
}

// CatalogFullRequestEvent distinguishes requested fences from a successfully
// persisted freshness requirement. A receipt/audit never establishes readiness.
func CatalogFullRequestEvent(actor ActorSnapshot, r catalog.FullRequest, coalesced bool, outcome string) (Event, error) {
	if err := r.Validate(); err != nil {
		return Event{}, err
	}
	ev, err := catalogControlEvent(actor, r.ExpectedEpoch, coalesced, outcome)
	if err != nil {
		return Event{}, err
	}
	ev.Action = "search.catalog.full_requested"
	g := strconv.FormatInt(r.ExpectedAllocatedGeneration, 10)
	ev.Metadata = append(ev.Metadata, MetadataField{"catalog_expected_generation", g})
	if outcome == "accepted" {
		ev.Metadata = append(ev.Metadata, MetadataField{"catalog_required_after_generation", g})
	}
	return validatedCatalogEvent(ev)
}

func catalogControlEvent(actor ActorSnapshot, epoch uuid.UUID, coalesced bool, outcome string) (Event, error) {
	if actor.Kind != "user" || actor.Role != "admin" || (coalesced && outcome != "accepted") {
		return Event{}, ErrInvalidEvent
	}
	switch outcome {
	case "accepted", "catalog_conflict", "catalog_recovery_required", "catalog_busy",
		"catalog_not_configured", "catalog_receiver_unreachable", "catalog_receiver_incompatible", "operation_failed":
	default:
		return Event{}, ErrInvalidEvent
	}
	result := "failure"
	if outcome == "accepted" {
		result = "success"
	}
	return Event{Domain: "search", Actor: actor, Result: result, ResourceType: "search_catalog", ResourceID: epoch.String(),
		Metadata: []MetadataField{{"catalog_scope", catalog.ScopeLocal}, {"coalesced", strconv.FormatBool(coalesced)}, {"outcome", outcome}}}, nil
}

func validatedCatalogEvent(ev Event) (Event, error) {
	n, err := normalizeEvent(ev)
	if err != nil {
		return Event{}, err
	}
	return n.Event, nil
}
