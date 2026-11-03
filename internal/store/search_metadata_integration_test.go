//go:build integration

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestCanonicalHydrationIncludesCurrentTags(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, channel := seedOwnerChannel(ctx, t, st, "canonical-tags")
	id := seedPublishedVideo(ctx, t, st, channel, "visible")
	q := st.Queries()
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: id, Tags: []string{"zebra", "alpha"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListPublicVideosByIDs(ctx, sqlcgen.ListPublicVideosByIDsParams{Ids: []uuid.UUID{id}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("hydrate=%v, %v", rows, err)
	}
	raw, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil || string(row["tags"]) != `["alpha","zebra"]` {
		t.Fatalf("canonical tag projection=%s, %v", row["tags"], err)
	}
	if err := q.DeleteVideoTags(ctx, id); err != nil {
		t.Fatal(err)
	}
	rows, err = q.ListPublicVideosByIDs(ctx, sqlcgen.ListPublicVideosByIDsParams{Ids: []uuid.UUID{id}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("fresh hydrate=%v, %v", rows, err)
	}
	raw, err = json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &row); err != nil || string(row["tags"]) != `[]` {
		t.Fatalf("canonical tags after deletion=%s, %v", row["tags"], err)
	}
}
