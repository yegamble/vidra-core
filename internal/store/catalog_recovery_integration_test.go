//go:build integration

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogRecoveryFencePreservesProofsAndRequiresCurrentEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := New(ctx, dsn(t), WithMaxConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	epoch, token := uuid.New(), uuid.New()
	defer func() {
		for _, table := range []string{"search_catalog_state", "search_catalog_epochs"} {
			if _, err := s.Pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE epoch=$1`, epoch); err != nil {
				t.Error(err)
			}
		}
	}()
	limits := catalog.Limits{MaxRequestBytes: catalog.MaxRequestBytes, MaxSnapshotBytes: catalog.MaxSnapshotBytes, MaxDocuments: catalog.MaxDocuments, MaxPages: catalog.MaxPages}
	var manifest catalog.Manifest
	err = s.WithCatalogControl(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_epochs(scope,epoch) VALUES ('local',$1)`, epoch); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO search_catalog_state(scope,epoch,allocated_generation,force_full,last_error_code,last_error_id)
			VALUES ('local',$1,2,false,'delivery_failed',$2)`, epoch, token); err != nil {
			return err
		}
		for _, generation := range []int64{1, 2} {
			builder, err := catalog.NewFullBuilder(catalog.Identity{Scope: "local", Epoch: epoch, Generation: catalog.Generation(generation)}, time.Now().UTC(), limits, func(catalog.FrozenPage) error { return nil })
			if err != nil {
				return err
			}
			end, err := builder.Finish()
			if err != nil {
				return err
			}
			manifest = end.Manifest
			if err := sqlcgen.New(tx).InsertCatalogCaptureSnapshot(ctx, sqlcgen.InsertCatalogCaptureSnapshotParams{Epoch: epoch, Generation: generation,
				MembershipSha256: manifest.MembershipSHA256, PagesSha256: manifest.PagesSHA256, EndEventID: end.EventID, EndRequestBody: end.RequestBody}); err != nil {
				return err
			}
			if generation == 1 {
				for _, statement := range []string{`UPDATE search_catalog_snapshots SET pending=false WHERE epoch=$1`,
					`UPDATE search_catalog_state SET verified_generation=1,verified_at=now() WHERE epoch=$1`} {
					if _, err := tx.Exec(ctx, statement, epoch); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	load := func() catalog.SourceWork {
		t.Helper()
		w, err := s.LoadCatalogWork(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := load()
	receiver := catalog.Status{ProtocolVersion: 2, Scope: "local", Epoch: &epoch, SupportedScopes: []string{"local"},
		SupportedEventTypes: []string{catalog.TypeSnapshotPage, catalog.TypeSnapshotEnd}, Limits: limits,
		HighestObservedGeneration: "0", CompletedGeneration: "0"}
	healthy, at := receiver, time.Now().UTC()
	healthy.Ready, healthy.HighestObservedGeneration, healthy.CompletedGeneration, healthy.CompletedAt, healthy.CompletedManifest = true, "1", "1", &at, &manifest
	if changed, err := s.RequireCatalogRecoveryFull(ctx, w, healthy); err == nil || changed {
		t.Fatalf("healthy receiver forced recovery: %v/%v", changed, err)
	}
	// A conflict committed after HTTP observation must also stop this control.
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_code='epoch_conflict' WHERE epoch=$1`, epoch); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.RequireCatalogRecoveryFull(ctx, w, receiver); !errors.Is(err, catalog.ErrEpochConflict) || changed {
		t.Fatalf("current conflict bypassed: %v/%v", changed, err)
	}
	newToken := uuid.New()
	if _, err := s.Pool.Exec(ctx, `UPDATE search_catalog_state SET last_error_code='delivery_failed',last_error_id=$2 WHERE epoch=$1`, epoch, newToken); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.RequireCatalogRecoveryFull(ctx, w, receiver); err != nil || changed {
		t.Fatalf("stale repeated-code evidence accepted: %v/%v", changed, err)
	}
	w = load()
	if changed, err := s.RequireCatalogRecoveryFull(ctx, w, receiver); err != nil || !changed {
		t.Fatalf("restore barrier not committed: %v/%v", changed, err)
	}
	want := w
	want.ForceFull, want.ForceFullAfterGeneration = true, 2
	if got := load(); !reflect.DeepEqual(got, want) {
		t.Fatalf("barrier changed payload/verified/error evidence: got%+v want%+v", got, want)
	}
	if changed, err := s.RequireCatalogRecoveryFull(ctx, w, receiver); err != nil || changed {
		t.Fatalf("stale force accepted: %v/%v", changed, err)
	}
	if changed, err := s.RequireCatalogRecoveryFull(ctx, want, receiver); err != nil || !changed {
		t.Fatalf("durable barrier failed exact retry: %v/%v", changed, err)
	}
}
