//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type legacyReceiptNode struct {
	ipfs.Client
	after func() error
}

func (n legacyReceiptNode) AddDirectory(ctx context.Context, entries []ipfs.DirEntry) (ipfs.AddResult, error) {
	result, err := n.Client.AddDirectory(ctx, entries)
	if err == nil {
		err = n.after()
	}
	return result, err
}

func TestLegacyIPFSHLSRecordsOnlyCopiedCurrentGeneration(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, scenario := range []string{"current", "generation changed during Add", "policy adopted during Add", "master absent from copy"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := st.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			id, owner, channel := uuid.New(), uuid.New(), uuid.New()
			key, master := media.HLSKeyPrefix(id)+"/", "streaming-playlists/imported/"+id.String()+"/hash-master.m3u8"
			exec("INSERT INTO users(id,username,email,password_hash,is_active) VALUES($1,$2,$3,'x',true)", owner, "receipt"+owner.String(), owner.String()+"@example.test")
			exec("INSERT INTO channels(id,owner_id,handle,display_name) VALUES($1,$2,$3,'receipt')", channel, owner, "receipt"+channel.String())
			exec("INSERT INTO videos(id,channel_id,title,privacy,state) VALUES($1,$2,'receipt','public','published')", id, channel)
			exec("INSERT INTO streaming_playlists(video_id,master_key,state) VALUES($1,$2,'ready')", id, master)
			// A retry must not carry a previous copy's receipt into a new CID.
			exec("INSERT INTO media_ipfs_pins(object_key,media_class,video_id,committed_generation) VALUES($1,'hls',$2,$3)", key, id, master)
			exec("INSERT INTO ipfs_control_config(config,policy_active) VALUES('{}',false) ON CONFLICT(singleton) DO UPDATE SET policy_active=false")
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			copiedKey := master
			if scenario == "master absent from copy" {
				copiedKey += "-other"
			}
			if _, err = blobs.Put(ctx, copiedKey, strings.NewReader("#EXTM3U\n")); err != nil {
				t.Fatal(err)
			}
			q := st.Queries().WithTx(tx)
			node := legacyReceiptNode{Client: ipfs.NewFakeIPFSClient(), after: func() error {
				if scenario == "generation changed during Add" {
					_, err := tx.Exec(ctx, "UPDATE streaming_playlists SET master_key=$2 WHERE video_id=$1", id, master+"-new")
					return err
				}
				if scenario == "policy adopted during Add" {
					_, err := tx.Exec(ctx, "UPDATE ipfs_control_config SET policy_active=true WHERE singleton")
					return err
				}
				return nil
			}}
			mirror := ipfsmirror.New(q, ipfsmirror.NewSQLLookups(q), blobs, node, ipfsmirror.Config{Enabled: true, GatewayURL: "https://gateway.test"})
			if n, err := mirror.DrainDue(ctx, 1); err != nil || n != 1 {
				t.Fatalf("copy: %d %v", n, err)
			}
			row, err := q.GetIPFSPinByObjectKey(ctx, key)
			if err != nil || row.State != "pinned" || row.Cid == "" || row.CarRoot != row.Cid {
				t.Fatalf("copy not pinned: %+v %v", row, err)
			}
			want := ""
			if scenario == "current" {
				want = master
			}
			if row.CommittedGeneration != want {
				t.Fatalf("generation=%q want=%q", row.CommittedGeneration, want)
			}
			url, allowed, err := mirror.PublicPlaybackHLS(ctx, id, master)
			if err != nil || allowed != (scenario == "current") || (allowed && !strings.HasSuffix(url, "/hash-master.m3u8")) {
				t.Fatalf("session eligibility: %q %v %v", url, allowed, err)
			}
			if scenario == "current" {
				for name, update := range map[string]string{
					"cid":      "UPDATE media_ipfs_pins SET cid='different' WHERE object_key=$1",
					"root":     "UPDATE media_ipfs_pins SET car_root='different' WHERE object_key=$1",
					"network":  "UPDATE media_ipfs_pins SET network='private' WHERE object_key=$1",
					"state":    "UPDATE media_ipfs_pins SET state='pending' WHERE object_key=$1",
					"class":    "UPDATE media_ipfs_pins SET media_class='original' WHERE object_key=$1",
					"policy":   "UPDATE media_ipfs_pins SET policy_reason='demand' WHERE object_key=$1",
					"claim":    "UPDATE media_ipfs_pins SET claim_token=gen_random_uuid() WHERE object_key=$1",
					"notready": "UPDATE streaming_playlists SET state='pending' WHERE video_id=(SELECT video_id FROM media_ipfs_pins WHERE object_key=$1)",
				} {
					exec("SAVEPOINT receipt_guard")
					exec(update, key)
					changed, err := q.RecordLegacyIPFSGeneration(ctx, sqlcgen.RecordLegacyIPFSGenerationParams{ObjectKey: key, Cid: row.Cid, Generation: master})
					if err != nil || changed != 0 {
						t.Fatalf("%s certified changed copy: %d %v", name, changed, err)
					}
					exec("ROLLBACK TO SAVEPOINT receipt_guard")
				}
			}
		})
	}
}
