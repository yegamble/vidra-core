//go:build integration

package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestSitePagesRetainConcurrentEdits(t *testing.T) {
	ctx := t.Context()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlcgen.New(tx)
	slug := "test-" + uuid.NewString()
	first, err := q.CreateSitePage(ctx, sqlcgen.CreateSitePageParams{Slug: slug, Title: "Privacy", Body: "One", InMenu: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateSitePage(ctx, sqlcgen.CreateSitePageParams{Slug: slug, Title: "Duplicate", Body: "Two"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("duplicate page: %v", err)
	}
	edit := sqlcgen.UpdateSitePageParams{Slug: slug, Revision: first.Revision, Title: "Privacy", Body: "Two", InMenu: false}
	updated, err := q.UpdateSitePage(ctx, edit)
	if err != nil || updated.Revision == first.Revision {
		t.Fatalf("update: %+v, %v", updated, err)
	}
	if _, err = q.UpdateSitePage(ctx, edit); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale update: %v", err)
	}
	if n, err := q.DeleteSitePage(ctx, sqlcgen.DeleteSitePageParams{Slug: slug, Revision: first.Revision}); err != nil || n != 0 {
		t.Fatalf("stale delete: %d, %v", n, err)
	}
	page, err := q.GetSitePage(ctx, slug)
	if err != nil || page.Body != "Two" || page.InMenu {
		t.Fatalf("saved page: %+v, %v", page, err)
	}
	for _, menuOnly := range []bool{true, false} {
		rows, err := q.ListSitePages(ctx, menuOnly)
		if err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, row := range rows {
			if row.Slug == slug {
				listed = true
			}
		}
		if listed == menuOnly {
			t.Fatalf("hidden page listed=%v with menuOnly=%v", listed, menuOnly)
		}
	}
	if n, err := q.DeleteSitePage(ctx, sqlcgen.DeleteSitePageParams{Slug: slug, Revision: updated.Revision}); err != nil || n != 1 {
		t.Fatalf("delete: %d, %v", n, err)
	}
}
