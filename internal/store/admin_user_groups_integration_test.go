//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestAdminUserGroupCountsAndPages(t *testing.T) {
	ctx := context.Background()
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
	_, err = tx.Exec(ctx, `INSERT INTO users (id,username,email,password_hash,role,is_active,deleted_at,created_at)
		SELECT ('9a2b0000-0000-4000-8000-00000000000'||n)::uuid, 'group_fixture_'||n,
		       n||'@admin-filter.example', '', role, n<3, CASE WHEN n=5 THEN now() END, now()
		FROM (VALUES (1,'admin'), (2,'user'), (3,'moderator'), (4,'user'), (5,'moderator')) u(n,role)`)
	if err != nil {
		t.Fatal(err)
	}
	q := st.Queries().WithTx(tx)
	const search = "@ADMIN-FILTER.EXAMPLE"
	counts, err := q.CountUsersMatching(ctx, search)
	if err != nil || counts != (sqlcgen.CountUsersMatchingRow{All: 5, Staff: 3, Deactivated: 2}) {
		t.Fatalf("counts = %+v, err = %v", counts, err)
	}
	for _, tc := range []struct {
		group  string
		offset int32
		want   string
	}{
		{"", 0, "group_fixture_5"}, {"all", 4, "group_fixture_1"},
		{"staff", 1, "group_fixture_3"}, {"deactivated", 0, "group_fixture_4"},
		{"deactivated", 1, "group_fixture_3"}, {"deactivated", 2, ""},
	} {
		rows, err := q.ListUsers(ctx, sqlcgen.ListUsersParams{Query: search, UserFilter: tc.group, ResultLimit: 1, ResultOffset: tc.offset})
		if err != nil || (tc.want == "" && len(rows) != 0) || (tc.want != "" && (len(rows) != 1 || rows[0].Username != tc.want)) {
			t.Errorf("filter %q offset %d: got %d rows, want %q, err %v", tc.group, tc.offset, len(rows), tc.want, err)
		}
	}
}
