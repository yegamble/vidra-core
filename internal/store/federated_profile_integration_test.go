//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestFederatedProfileVisibility(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	owner, channel := seedOwnerChannel(ctx, t, st, "approfile")
	video := seedPublishedVideo(ctx, t, st, channel, "Public channel film")
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	u, err := q.GetUserByID(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := q.GetChannelByID(ctx, channel)
	if err != nil {
		t.Fatal(err)
	}
	var note uuid.UUID
	if err := tx.QueryRow(ctx, "INSERT INTO comments(video_id,user_id,body) VALUES($1,$2,'Fixture reply') RETURNING id", video, owner).Scan(&note); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, change  string
		person, group bool
	}{
		{"private", "", false, true},
		{"public", "UPDATE users SET profile_public=true WHERE id=$1", true, true},
		{"inactive", "UPDATE users SET profile_public=true,is_active=false WHERE id=$1", false, false},
		{"deleted", "UPDATE users SET profile_public=true,deleted_at=now() WHERE id=$1", false, false},
		{"banned", "WITH opted_in AS (UPDATE users SET profile_public=true WHERE id=$1 RETURNING id) INSERT INTO user_bans(user_id,reason) SELECT id,'fixture' FROM opted_in", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer sp.Rollback(ctx)
			pending, err := sqlcgen.New(sp).InsertRemoteFollowPending(ctx, sqlcgen.InsertRemoteFollowPendingParams{
				ChannelID: channel, RemoteActorUrl: "https://peer.example/accounts/earlier", FollowActivityUrl: "https://peer.example/follow/earlier",
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.change != "" {
				if _, err := sp.Exec(ctx, tc.change, owner); err != nil {
					t.Fatal(err)
				}
			}
			svc := federation.NewService(sqlcgen.New(sp), federation.WithBaseURL("https://vidra.example"))
			check := func(surface string, err error, visible bool) {
				t.Helper()
				if visible && err != nil || !visible && !errors.Is(err, federation.ErrNotFound) {
					t.Errorf("%s: visible=%t, err=%v", surface, visible, err)
				}
			}
			_, err = svc.AccountActor(ctx, u.Username)
			check("Person", err, tc.person)
			_, err = svc.WebFinger(ctx, "acct:"+u.Username+"@vidra.example")
			check("account discovery", err, tc.person)
			for _, kind := range []string{"followers", "following", "outbox"} {
				_, err = svc.AccountCollection(ctx, u.Username, kind)
				check(kind, err, tc.person)
			}
			_, err = svc.NoteObject(ctx, note)
			check("Note", err, tc.person)
			_, err = svc.WebFinger(ctx, "acct:"+ch.Handle+"@vidra.example")
			check("channel discovery", err, tc.group)
			const follower = "https://peer.example/accounts/viewer"
			local := "https://vidra.example/video-channels/" + ch.Handle
			follow := []byte(fmt.Sprintf(`{"id":"%s/follow/%s","type":"Follow","actor":%q,"object":%q}`, follower, uuid.New(), follower, local))
			if err := svc.HandleInbox(ctx, follower, follow); err != nil {
				t.Fatal(err)
			}
			n, err := sqlcgen.New(sp).CountRemoteFollowers(ctx, channel)
			if err != nil || (n == 1) != tc.group {
				t.Errorf("accepted follows=%d, err=%v", n, err)
			}
			_, err = sqlcgen.New(sp).AcceptPendingRemoteFollowByID(ctx, pending)
			if tc.group && err != nil || !tc.group && !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("pending follow approval: eligible=%t, err=%v", tc.group, err)
			}
			group, err := svc.ChannelActor(ctx, ch.Handle)
			check("Group", err, tc.group)
			if group != nil && (len(group.AttributedTo) > 0) != tc.person {
				t.Error("unexpected channel owner attribution")
			}
			object, err := svc.VideoObject(ctx, video)
			check("Video", err, tc.group)
			if object != nil && (len(object["attributedTo"].([]map[string]any)) > 1) != tc.person {
				t.Error("unexpected video owner attribution")
			}
			page, err := svc.ChannelOutboxPage(ctx, ch.Handle, 1)
			check("channel outbox", err, tc.group)
			if page != nil {
				if len(page.OrderedItems) != 1 {
					t.Fatalf("outbox has %d entries", len(page.OrderedItems))
				}
				obj := page.OrderedItems[0]["object"].(map[string]any)
				if (len(obj["attributedTo"].([]map[string]any)) > 1) != tc.person {
					t.Error("unexpected outbox owner attribution")
				}
			}
		})
	}
}
