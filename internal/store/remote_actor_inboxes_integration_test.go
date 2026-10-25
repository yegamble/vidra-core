//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Migration 0183 clears remote-actor inboxes cached before the fetch learned
// to drop cross-host ones: an inbox on another host would have this instance
// sign and POST activities to a server that never federated with it.
func TestClearCrossHostRemoteInboxes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	tag := uuid.NewString()[:8]
	own := "https://own-" + tag + ".example"
	type row struct {
		actor, inbox string
		shared       *string
	}
	str := func(s string) *string { return &s }
	rows := map[string]row{
		"both foreign": {own + "/a/1", "https://victim.example/inbox", str("https://victim.example/shared")},
		"shared only":  {own + "/a/2", own + "/a/2/inbox", str("https://victim.example/shared")},
		"own host":     {own + "/a/3", own + "/a/3/inbox", str(own + "/inbox")},
		"no inbox":     {own + "/a/4", "", nil},
		"host case":    {own + "/a/5", "https://OWN-" + tag + ".example/a/5/inbox", nil},
	}
	for _, r := range rows {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, inbox_url, shared_inbox_url, public_key_pem)
			 VALUES ($1, 'Person', 'x', $2, $3, $4, 'pem')`,
			r.actor, "own-"+tag+".example", r.inbox, r.shared); err != nil {
			t.Fatalf("seed %s: %v", r.actor, err)
		}
		defer func(a string) {
			_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, a)
		}(r.actor)
	}

	if _, err := st.Pool.Exec(ctx, `SELECT clear_cross_host_remote_inboxes()`); err != nil {
		t.Fatalf("clear: %v", err)
	}

	want := map[string]struct {
		inbox  string
		shared *string
	}{
		"both foreign": {"", nil},
		"shared only":  {own + "/a/2/inbox", nil},
		"own host":     {own + "/a/3/inbox", str(own + "/inbox")},
		"no inbox":     {"", nil},
		"host case":    {"https://OWN-" + tag + ".example/a/5/inbox", nil},
	}
	for name, r := range rows {
		var inbox string
		var shared *string
		if err := st.Pool.QueryRow(ctx,
			`SELECT inbox_url, shared_inbox_url FROM remote_actors WHERE actor_url = $1`, r.actor).Scan(&inbox, &shared); err != nil {
			t.Fatalf("%s: read: %v", name, err)
		}
		w := want[name]
		if inbox != w.inbox {
			t.Errorf("%s: inbox = %q, want %q", name, inbox, w.inbox)
		}
		if (shared == nil) != (w.shared == nil) || (shared != nil && *shared != *w.shared) {
			t.Errorf("%s: shared inbox = %v, want %v", name, shared, w.shared)
		}
	}
}
