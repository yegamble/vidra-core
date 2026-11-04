package store

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func workPtr[T any](v T) *T { return &v }

func workRow() sqlcgen.ReadCatalogDeliveryStateRow {
	at := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	return sqlcgen.ReadCatalogDeliveryStateRow{
		Epoch: uuid.New(), AllocatedGeneration: 2, ForceFull: true, ForceFullAfterGeneration: 1,
		LastErrorCode: workPtr("delivery_failed"), LastErrorID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ReceiverPredecessor: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		VerifiedGeneration:  workPtr(int64(1)), VerifiedAt: at, VerifiedPageCount: workPtr(int32(1)), VerifiedDocumentCount: workPtr(int64(2)),
		VerifiedMembershipSha256: workPtr(strings.Repeat("a", 64)), VerifiedPagesSha256: workPtr(strings.Repeat("b", 64)),
		PendingGeneration: workPtr(int64(2)), PendingCapturedAt: at, PendingPageCount: workPtr(int32(1)), PendingDocumentCount: workPtr(int64(2)),
		PendingCanonicalBytes: workPtr(int64(4096)), PendingMembershipSha256: workPtr(strings.Repeat("a", 64)), PendingPagesSha256: workPtr(strings.Repeat("b", 64)),
		PendingEndRequestBody: []byte(" frozen END bytes\n"), // opaque here; strict envelope preflight is separate
	}
}

func TestMapCatalogWorkCopiesExactEvidenceAndAcceptsInitialEmptyAndGap(t *testing.T) {
	r := workRow()
	w, err := mapCatalogWork(r)
	if err != nil || w.Evidence.Epoch != r.Epoch || w.Evidence.Pending.Identity.Generation != 2 || w.Evidence.Verified.Identity.Generation != 1 || !w.ForceFull || w.ForceFullAfterGeneration != 1 || w.LastErrorCode != *r.LastErrorCode || *w.LastErrorID != uuid.UUID(r.LastErrorID.Bytes) || *w.ReceiverPredecessor != uuid.UUID(r.ReceiverPredecessor.Bytes) || !bytes.Equal(w.PendingEndRequestBody, r.PendingEndRequestBody) {
		t.Fatalf("lost source evidence: %+v/%v", w, err)
	}
	r.PendingEndRequestBody[0] = 'X'
	r.PendingCapturedAt.Time = time.Time{}
	*r.VerifiedMembershipSha256 = "changed"
	if w.PendingEndRequestBody[0] != ' ' || w.PendingCapturedAt.IsZero() || w.Evidence.Verified.Manifest.MembershipSHA256 != strings.Repeat("a", 64) {
		t.Fatal("DTO aliases row storage")
	}
	initial := sqlcgen.ReadCatalogDeliveryStateRow{Epoch: uuid.New(), ForceFull: true}
	if w, err := mapCatalogWork(initial); err != nil || w.Evidence.Verified != nil || w.Evidence.Pending != nil || w.PendingEndRequestBody != nil {
		t.Fatalf("initial source refused: %+v/%v", w, err)
	}
	initial.PendingEndRequestBody = []byte{}
	assertWorkRefused(t, initial)
	initial.PendingEndRequestBody = nil
	initial.AllocatedGeneration = 2 // retirement preserves the allocator, never source readiness
	if _, err := mapCatalogWork(initial); err != nil {
		t.Fatal(err)
	}
	r = workRow()
	*r.PendingPageCount, *r.PendingDocumentCount, *r.PendingCanonicalBytes = 0, 0, 0
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	*r.PendingMembershipSha256, *r.PendingPagesSha256 = empty, empty
	if _, err := mapCatalogWork(r); err != nil {
		t.Fatal(err)
	}
}

func TestMapCatalogWorkRefusesEveryMissingAndStrayNullableProofColumn(t *testing.T) {
	for _, name := range []string{"VerifiedGeneration", "VerifiedAt", "VerifiedPageCount", "VerifiedDocumentCount", "VerifiedMembershipSha256", "VerifiedPagesSha256", "PendingGeneration", "PendingCapturedAt", "PendingPageCount", "PendingDocumentCount", "PendingCanonicalBytes", "PendingMembershipSha256", "PendingPagesSha256", "PendingEndRequestBody"} {
		valid := workRow()
		missing := valid
		field := reflect.ValueOf(&missing).Elem().FieldByName(name)
		field.SetZero()
		assertWorkRefused(t, missing)
		stray := sqlcgen.ReadCatalogDeliveryStateRow{Epoch: valid.Epoch, AllocatedGeneration: 2}
		reflect.ValueOf(&stray).Elem().FieldByName(name).Set(reflect.ValueOf(valid).FieldByName(name))
		assertWorkRefused(t, stray)
	}
}

func TestMapCatalogWorkRefusesCorruptFencesErrorsDatesAndQuota(t *testing.T) {
	for _, mutate := range []func(*sqlcgen.ReadCatalogDeliveryStateRow){
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.Epoch = uuid.Nil },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.AllocatedGeneration = -1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.ForceFullAfterGeneration = -1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.ForceFullAfterGeneration = 3 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.VerifiedGeneration = 0 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.VerifiedGeneration = 3 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingGeneration = 1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingGeneration = 3 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.LastErrorID.Valid = false },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.LastErrorID.Bytes = uuid.Nil },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.LastErrorCode = nil },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.LastErrorCode = "" },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.LastErrorCode = "unknown" },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.ReceiverPredecessor.Bytes = r.Epoch },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.ReceiverPredecessor.Bytes = uuid.Nil },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.VerifiedAt.Time = time.Time{} },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.PendingCapturedAt.InfinityModifier = pgtype.Infinity },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingPageCount = catalog.MaxPages + 1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingDocumentCount = catalog.MaxDocuments + 1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.VerifiedDocumentCount = catalog.MaxDocuments + 1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingCanonicalBytes = catalog.MaxSnapshotBytes + 1 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.PendingCanonicalBytes = 0 },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { r.PendingEndRequestBody = []byte{} },
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) {
			r.PendingEndRequestBody = make([]byte, catalog.MaxRequestBytes+1)
		},
		func(r *sqlcgen.ReadCatalogDeliveryStateRow) { *r.VerifiedPagesSha256 = strings.Repeat("A", 64) },
	} {
		r := workRow()
		mutate(&r)
		assertWorkRefused(t, r)
	}
	for _, code := range []string{"quota_exceeded", "scalar_limit_exceeded", "capture_failed", "delivery_failed", "verification_failed", "epoch_conflict"} {
		r := workRow()
		r.LastErrorCode = &code
		if _, err := mapCatalogWork(r); err != nil {
			t.Fatal(err)
		}
	}
}

func assertWorkRefused(t *testing.T, r sqlcgen.ReadCatalogDeliveryStateRow) {
	t.Helper()
	if w, err := mapCatalogWork(r); err == nil || !reflect.DeepEqual(w, catalog.SourceWork{}) {
		t.Fatalf("corrupt/partial row manufactured work: %+v/%v", w, err)
	}
}
