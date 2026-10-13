//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Per-flag sensitive content (0173): the browse query's hide predicate is
// vidra_sensitive_hidden(), which resolves each flag to the viewer's override,
// else the base policy, and hides when ANY flag resolves to hide.
func TestSensitiveFlagsGridHiding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	domain := "flags-" + suffix + ".test"
	actor := "https://" + domain + "/video-channels/c"
	if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{
		ActorUrl: actor, ActorType: "Group", PreferredUsername: "c", Domain: domain,
		InboxUrl: actor + "/inbox", PublicKeyPem: "pem", FollowersUrl: actor + "/followers",
	}); err != nil {
		t.Fatalf("UpsertRemoteActor: %v", err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actor)
	}()
	mk := func(name string, sensitive bool, flags []string) uuid.UUID {
		row, err := q.UpsertRemoteVideo(ctx, sqlcgen.UpsertRemoteVideoParams{
			ObjectUrl: "https://" + domain + "/videos/" + name, RemoteActorUrl: actor, Title: name + suffix,
			WatchUrl: "https://" + domain + "/w/" + name, IsSensitive: sensitive, SensitiveFlags: flags,
		})
		if err != nil {
			t.Fatalf("UpsertRemoteVideo %s: %v", name, err)
		}
		return row.ID
	}
	plain := mk("plain", false, nil)
	unflagged := mk("unflagged", true, nil) // sensitive, no kind: follows the base policy
	violent := mk("violent", true, []string{"violent"})
	both := mk("both", true, []string{"violent", "sex"})

	var viewer uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, 'x') RETURNING id`, "flags"+suffix, "flags"+suffix+"@example.test").Scan(&viewer); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, viewer) }()

	shown := func(baseHide bool, asViewer bool) map[uuid.UUID]bool {
		t.Helper()
		arg := sqlcgen.ListPublicVideosSortedParams{HideSensitive: baseHide, IncludeRemote: true, Sort: "recent", ResultLimit: 10000}
		if asViewer {
			arg.ViewerID = pgtype.UUID{Bytes: viewer, Valid: true}
		}
		rows, err := q.ListPublicVideosSorted(ctx, arg)
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		out := map[uuid.UUID]bool{}
		for _, r := range rows {
			out[r.ID] = true
		}
		return out
	}
	check := func(label string, got map[uuid.UUID]bool, want map[uuid.UUID]bool) {
		t.Helper()
		for id, w := range want {
			if got[id] != w {
				t.Errorf("%s: video %s shown=%v, want %v", label, id, got[id], w)
			}
		}
	}

	// No overrides: base hide hides every sensitive video, base display none.
	check("base hide", shown(true, true), map[uuid.UUID]bool{plain: true, unflagged: false, violent: false, both: false})
	check("base display", shown(false, true), map[uuid.UUID]bool{plain: true, unflagged: true, violent: true, both: true})

	save := func(policies string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, `INSERT INTO user_content_prefs (user_id, sensitive_flag_policies) VALUES ($1, $2::jsonb)
			ON CONFLICT (user_id) DO UPDATE SET sensitive_flag_policies = EXCLUDED.sensitive_flag_policies`, viewer, policies); err != nil {
			t.Fatalf("save prefs: %v", err)
		}
	}
	// Base display, but this viewer hides violence: the violent video and the
	// one carrying violence among others go; the unflagged one stays.
	save(`{"violent":"hide"}`)
	check("display + hide violent", shown(false, true), map[uuid.UUID]bool{plain: true, unflagged: true, violent: false, both: false})
	// Base hide, but this viewer displays violence: only a video whose flags
	// ALL resolve away from hide is shown; "both" still hides via sex.
	save(`{"violent":"display"}`)
	check("hide + display violent", shown(true, true), map[uuid.UUID]bool{plain: true, unflagged: false, violent: true, both: false})
	save(`{"violent":"display","sex":"blur"}`)
	check("hide + display violent + blur sex", shown(true, true), map[uuid.UUID]bool{plain: true, unflagged: false, violent: true, both: true})
	// An anonymous request ignores any viewer's overrides.
	check("anonymous", shown(true, false), map[uuid.UUID]bool{plain: true, unflagged: false, violent: false, both: false})
}

// The browse feed's ?language is = ANY(): a viewer's several preferred
// languages select videos in any of them, and an unset filter selects all.
func TestFeedLanguageFilterMatchesAny(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	count := func(langs []string) int64 {
		n, err := q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{Language: langs})
		if err != nil {
			t.Fatalf("CountPublicVideosSorted: %v", err)
		}
		return n
	}
	en0, fr0, enfr0, de0 := count([]string{"en"}), count([]string{"fr"}), count([]string{"en", "fr"}), count([]string{"de"})

	suf := uuid.NewString()[:8]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: "lang_" + suf, Email: "lang_" + suf + "@example.test", PasswordHash: "x", Role: "user"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID) }()
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{OwnerID: u.ID, Handle: "lang" + suf, DisplayName: "L"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	for _, l := range []string{"en", "fr", "de"} {
		lang := l
		v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{ChannelID: ch.ID, Title: "v " + l, Privacy: "public", CommentsPolicy: "enabled", Language: &lang})
		if err != nil {
			t.Fatalf("CreateVideo: %v", err)
		}
		if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "published"}); err != nil {
			t.Fatalf("SetVideoState: %v", err)
		}
	}
	for name, d := range map[string][2]int64{
		"en":    {count([]string{"en"}) - en0, 1},
		"fr":    {count([]string{"fr"}) - fr0, 1},
		"de":    {count([]string{"de"}) - de0, 1},
		"en+fr": {count([]string{"en", "fr"}) - enfr0, 2},
	} {
		if d[0] != d[1] {
			t.Errorf("language %s: count moved by %d, want %d", name, d[0], d[1])
		}
	}
}
