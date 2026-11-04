//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCatalogSourceNullableCursorPreservesInvalidZeroForPreflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, hiddenOwner, channel, hiddenChannel := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{owner, hiddenOwner} {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,is_active,unlisted)
			VALUES ($1,$2,$3,'x',false,$4)`, id, id.String(), id.String()+"@example.test", i == 1); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []uuid.UUID{channel, hiddenChannel} {
		if _, err := tx.Exec(ctx, `INSERT INTO channels(id,owner_id,handle,display_name)
			VALUES ($1,$2,$3,'channel')`, id, []uuid.UUID{owner, hiddenOwner}[i], id.String()); err != nil {
			t.Fatal(err)
		}
	}
	visible, hidden, private, draft, blocked := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{uuid.Nil, visible, hidden, private, draft, blocked} {
		ch, privacy, state := channel, "public", "published"
		if i == 2 {
			ch = hiddenChannel
		}
		if i == 3 {
			privacy = "private"
		}
		if i == 4 {
			state = "draft"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO videos(id,channel_id,title,description,privacy,state)
			VALUES ($1,$2,'title <&>','source description',$3,$4)`, id, ch, privacy, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO video_blocks(video_id) VALUES ($1)`, blocked); err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(tx)
	first, err := q.ListCatalogSourcePage(ctx, sqlcgen.ListCatalogSourcePageParams{PageSize: 1})
	if err != nil || len(first) != 1 || first[0].ID != uuid.Nil {
		t.Fatalf("first page silently omitted invalid zero ID: %+v, %v", first, err)
	}
	next, err := q.ListCatalogSourcePage(ctx, sqlcgen.ListCatalogSourcePageParams{After: pgtype.UUID{Bytes: uuid.Nil, Valid: true}, PageSize: 100000})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range next {
		if row.ID == uuid.Nil || row.ID == hidden || row.ID == private || row.ID == draft || row.ID == blocked {
			t.Fatalf("ineligible/cursor-excluded fixture returned: %s", row.ID)
		}
		if row.ID != visible {
			continue // Other integration fixtures may also be eligible.
		}
		found = true
		doc := searchevents.CatalogVideoDoc(row)
		if doc.OwnerID != owner || doc.ChannelID != channel || doc.Title != "title <&>" || doc.Description != "source description" || doc.Tags == nil {
			t.Fatalf("canonical projection changed source metadata: %+v", doc)
		}
	}
	if !found {
		t.Fatal("eligible fixture with inactive, listed owner was omitted")
	}
	last, err := q.ListCatalogSourcePage(ctx, sqlcgen.ListCatalogSourcePageParams{After: pgtype.UUID{Bytes: visible, Valid: true}, PageSize: 100000})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range last {
		if row.ID == visible || row.ID == hidden || row.ID == private || row.ID == draft || row.ID == blocked || row.ID == uuid.Nil {
			t.Fatalf("cursor replayed or skipped eligibility: %s", row.ID)
		}
	}
}
