//go:build integration

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
)

func TestIPFSGatewayRootQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cid := ipfs.RawLeafCIDv1([]byte(uuid.NewString()))
	defer func() { _, _ = st.Pool.Exec(context.Background(), "DELETE FROM media_ipfs_pins WHERE cid=$1", cid) }()
	seed := func(n int, state, network string) {
		t.Helper()
		_, err := st.Pool.Exec(ctx, `INSERT INTO media_ipfs_pins(object_key,media_class,cid,state,network)
		 VALUES($1,'video_original',$2,$3,$4)`, fmt.Sprintf("gateway-test/%s/%03d", cid, n), cid, state, network)
		if err != nil {
			t.Fatal(err)
		}
	}
	seed(0, "pinned", "public")
	seed(1, "pinned", "private")
	seed(2, "pending", "public")
	seed(3, "unpinning", "public")
	rows, err := st.Queries().ListPublicIPFSRootPins(ctx, cid)
	if err != nil || len(rows) != 1 || rows[0].Network != "public" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	// More shared references must produce the overflow sentinel, never an
	// unbounded result or a silently truncated authorization decision.
	for n := 4; n < 135; n++ {
		seed(n, "pinned", "public")
	}
	rows, err = st.Queries().ListPublicIPFSRootPins(ctx, cid)
	if err != nil || len(rows) != 129 {
		t.Fatalf("overflow rows=%d err=%v", len(rows), err)
	}
	rows, err = st.Queries().ListPublicIPFSRootPins(ctx, ipfs.RawLeafCIDv1([]byte("unknown")))
	if err != nil || len(rows) != 0 {
		t.Fatalf("unknown rows=%d err=%v", len(rows), err)
	}
}
