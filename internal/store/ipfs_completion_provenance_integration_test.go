//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestIPFSCompletionRechecksIdentityAndPlaylistProvenance(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, class := range []string{"user_avatar", "user_banner", "channel_avatar", "channel_banner", "playlist_cover", "future_unknown"} {
		scenarios := []string{"current", "replaced"}
		if strings.Contains(class, "avatar") || strings.Contains(class, "banner") {
			scenarios = append(scenarios, "unlisted", "inactive", "deleted", "missing-owner")
		}
		if class == "playlist_cover" {
			scenarios = append(scenarios, "private")
		}
		for _, scenario := range scenarios {
			t.Run(class+"/"+scenario, func(t *testing.T) {
				tx, e := st.Pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback(ctx)
				exec := func(sql string, args ...any) {
					t.Helper()
					if _, e = tx.Exec(ctx, sql, args...); e != nil {
						t.Fatal(e)
					}
				}
				owner, channel, playlist, claim := uuid.New(), uuid.New(), uuid.New(), uuid.New()
				exec("INSERT INTO users(id,username,email,password_hash,is_active) VALUES($1,$2,$3,'x',true)", owner, "copy"+owner.String(), owner.String()+"@example.test")
				exec("INSERT INTO channels(id,owner_id,handle,display_name) VALUES($1,$2,$3,'test')", channel, owner, "copy"+channel.String())
				key := "completion/" + claim.String()
				if class == "playlist_cover" {
					key = "playlist-thumbnails/" + playlist.String() + ".jpg"
					exec("INSERT INTO playlists(id,owner_id,title,visibility,thumbnail_ext) VALUES($1,$2,'test','public','jpg')", playlist, owner)
				}
				if strings.HasPrefix(class, "user_") {
					exec("INSERT INTO user_images(user_id,kind,storage_key,size_bytes) VALUES($1,$2,$3,1)", owner, strings.TrimPrefix(class, "user_"), key)
				}
				if strings.HasPrefix(class, "channel_") {
					exec("INSERT INTO channel_images(channel_id,kind,storage_key,size_bytes) VALUES($1,$2,$3,1)", channel, strings.TrimPrefix(class, "channel_"), key)
				}
				exec("INSERT INTO media_ipfs_pins(object_key,media_class,owner_user_id,claim_token,lease_until) VALUES($1,$2,$3,$4,now()+interval '1 minute')", key, class, owner, claim)
				switch scenario {
				case "unlisted":
					exec("UPDATE users SET unlisted=true WHERE id=$1", owner)
				case "inactive":
					exec("UPDATE users SET is_active=false WHERE id=$1", owner)
				case "deleted":
					exec("UPDATE users SET deleted_at=now() WHERE id=$1", owner)
				case "missing-owner":
					exec("UPDATE media_ipfs_pins SET owner_user_id=NULL WHERE object_key=$1", key)
				case "private":
					exec("UPDATE playlists SET visibility='private' WHERE id=$1", playlist)
				case "replaced":
					exec("UPDATE user_images SET storage_key=storage_key||'-new' WHERE user_id=$1", owner)
					exec("UPDATE channel_images SET storage_key=storage_key||'-new' WHERE channel_id=$1", channel)
					exec("UPDATE playlists SET thumbnail_ext='png' WHERE id=$1", playlist)
				}
				state, e := st.Queries().WithTx(tx).CompleteIPFSAdmission(ctx, sqlcgen.CompleteIPFSAdmissionParams{ObjectKey: key, ClaimToken: pgtype.UUID{Bytes: claim, Valid: true}, Cid: "returned-root", ByteSize: 1})
				want := "unpinning"
				if scenario == "current" && class != "future_unknown" {
					want = "pinned"
				}
				if e != nil || state != want {
					t.Fatalf("state=%q want=%q err=%v", state, want, e)
				}
			})
		}
	}
}
