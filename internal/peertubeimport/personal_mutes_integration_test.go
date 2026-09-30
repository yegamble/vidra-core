//go:build integration

package peertubeimport

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func personalMuteFixture(t *testing.T) (context.Context, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	return ctx, src, dest
}

func seedPersonalMutes(t *testing.T, ctx context.Context, src *pgxpool.Pool) {
	t.Helper()
	mustExec(t, ctx, src, `
CREATE TABLE "accountBlocklist" (id integer PRIMARY KEY,"accountId" integer,"targetAccountId" integer,"createdAt" timestamptz);
CREATE TABLE server (id integer PRIMARY KEY,host text);
CREATE TABLE "serverBlocklist" (id integer PRIMARY KEY,"accountId" integer,"targetServerId" integer,"createdAt" timestamptz);
INSERT INTO account(id,name,"userId","actorId") VALUES (6,'instance',NULL,99);
INSERT INTO "accountBlocklist" VALUES (1,1,2,'2020-01-02T00:00:00Z'),(2,6,2,'2020-01-02T00:00:00Z'),(3,1,5,'2020-01-02T00:00:00Z'),(4,5,1,'2020-01-02T00:00:00Z');
INSERT INTO server VALUES (1,'EXAMPLE.ORG');
INSERT INTO "serverBlocklist" VALUES (1,1,1,'2020-02-03T00:00:00Z'),(2,6,1,'2020-02-03T00:00:00Z');`)
}

func runPersonalMuteImport(t *testing.T, ctx context.Context, src, dest *pgxpool.Pool, opts Options) *Report {
	t.Helper()
	opts.Policy, opts.MediaMode = PolicyFail, MediaModeNone
	r, err := NewImporter(dest, NewSourceFromPool(src), opts).Run(ctx, 800, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPeerTubeImportPersonalMutes(t *testing.T) {
	ctx, src, dest := personalMuteFixture(t)
	seedPersonalMutes(t, ctx, src)
	plan, err := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, MediaMode: MediaModeNone}).Plan(ctx, 800)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"account_mute", "instance_mute"} {
		if c := plan.Entities[kind]; c == nil || c.Planned != 1 {
			t.Errorf("plan %s=%+v", kind, c)
		}
	}
	if got := scanStrings(t, ctx, dest, `SELECT count(*)::text FROM peertube_import_ledger`); !reflect.DeepEqual(got, []string{"0"}) {
		t.Errorf("plan wrote ledger=%v", got)
	}
	r := runPersonalMuteImport(t, ctx, src, dest, Options{})
	for _, kind := range []string{"account_mute", "instance_mute"} {
		if c := r.Entities[kind]; c == nil || c.Imported != 1 {
			t.Errorf("run %s=%+v", kind, c)
		}
	}
	if got := scanStrings(t, ctx, dest, `SELECT u.username||':'||v.username||':'||to_char(m.created_at AT TIME ZONE 'UTC','YYYY-MM-DD') FROM muted_accounts m JOIN users u ON u.id=m.muter_id JOIN users v ON v.id=m.muted_id`); !reflect.DeepEqual(got, []string{"alice:bob:2020-01-02"}) {
		t.Errorf("account mute=%v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT u.username||':'||m.domain||':'||to_char(m.created_at AT TIME ZONE 'UTC','YYYY-MM-DD') FROM muted_instances m JOIN users u ON u.id=m.muter_id`); !reflect.DeepEqual(got, []string{"alice:example.org:2020-02-03"}) {
		t.Errorf("instance mute=%v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT ((SELECT count(*) FROM user_blocks)+(SELECT count(*) FROM blocked_instances))::text`); !reflect.DeepEqual(got, []string{"0"}) {
		t.Errorf("personal mute became native block=%v", got)
	}
	// A user's later unmute survives even source-authoritative metadata refreshes.
	mustExec(t, ctx, dest, `DELETE FROM muted_accounts;DELETE FROM muted_instances`)
	runPersonalMuteImport(t, ctx, src, dest, Options{SourceAuthoritative: true})
	if got := scanStrings(t, ctx, dest, `SELECT ((SELECT count(*) FROM muted_accounts)+(SELECT count(*) FROM muted_instances))::text`); !reflect.DeepEqual(got, []string{"0"}) {
		t.Errorf("rerun resurrected unmute=%v", got)
	}
}

func TestPeerTubeImportPersonalMutesRetryMissingParent(t *testing.T) {
	ctx, src, dest := personalMuteFixture(t)
	seedPersonalMutes(t, ctx, src)
	r := runPersonalMuteImport(t, ctx, src, dest, Options{SealKey: func(pem string) (string, error) {
		if strings.Contains(pem, "BOB") {
			return "", errors.New("fixture key unavailable")
		}
		return pem, nil
	}})
	if c := r.Entities["account_mute"]; c == nil || c.waiting != 1 {
		t.Errorf("missing parent=%+v", c)
	}
	if got := scanStrings(t, ctx, dest, `SELECT count(*)::text FROM peertube_import_ledger WHERE entity_kind='account_mute'`); !reflect.DeepEqual(got, []string{"0"}) {
		t.Errorf("missing parent made terminal child=%v", got)
	}
	r = runPersonalMuteImport(t, ctx, src, dest, Options{})
	if c := r.Entities["account_mute"]; c == nil || c.Imported != 1 {
		t.Errorf("parent retry=%+v", c)
	}
}

func TestPeerTubeImportPersonalMutesPreserveExisting(t *testing.T) {
	ctx, src, dest := personalMuteFixture(t)
	runPersonalMuteImport(t, ctx, src, dest, Options{})
	seedPersonalMutes(t, ctx, src)
	mustExec(t, ctx, dest, `INSERT INTO muted_accounts SELECT a.id,b.id,'2010-01-01T00:00:00Z' FROM users a,users b WHERE a.username='alice' AND b.username='bob';INSERT INTO muted_instances SELECT id,'example.org','2010-01-01T00:00:00Z' FROM users WHERE username='alice'`)
	r := runPersonalMuteImport(t, ctx, src, dest, Options{})
	for _, kind := range []string{"account_mute", "instance_mute"} {
		if c := r.Entities[kind]; c == nil || c.Skipped != 1 || c.Imported != 0 {
			t.Errorf("preexisting %s=%+v", kind, c)
		}
	}
	if got := scanStrings(t, ctx, dest, `SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD') FROM muted_accounts UNION ALL SELECT to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD') FROM muted_instances`); !reflect.DeepEqual(got, []string{"2010-01-01", "2010-01-01"}) {
		t.Errorf("existing timestamp overwritten=%v", got)
	}
}

func TestPeerTubeImportPersonalMutesInvalidAndAbsent(t *testing.T) {
	t.Run("invalid preferences are isolated and retryable", func(t *testing.T) {
		ctx, src, dest := personalMuteFixture(t)
		seedPersonalMutes(t, ctx, src)
		mustExec(t, ctx, src, `INSERT INTO "accountBlocklist" VALUES(9,1,1,now());UPDATE server SET host='https://private.example/secret'`)
		var logs bytes.Buffer
		r := runPersonalMuteImport(t, ctx, src, dest, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
		if c := r.Entities["account_mute"]; c == nil || c.Unsupported != 1 || c.Imported != 1 {
			t.Errorf("self mute=%+v", c)
		}
		if c := r.Entities["instance_mute"]; c == nil || c.Failed != 1 {
			t.Errorf("invalid domain=%+v", c)
		}
		if strings.Contains(logs.String(), "private.example") || strings.Contains(logs.String(), "secret") {
			t.Errorf("private mute data leaked to logs")
		}
		if got := scanStrings(t, ctx, dest, `SELECT status||':'||note FROM peertube_import_ledger WHERE entity_kind='instance_mute'`); len(got) != 1 || strings.Contains(got[0], "private.example") {
			t.Errorf("failure ledger=%v", got)
		}
		mustExec(t, ctx, src, `UPDATE server SET host='example.org'`)
		r = runPersonalMuteImport(t, ctx, src, dest, Options{})
		if c := r.Entities["instance_mute"]; c == nil || c.Imported != 1 {
			t.Errorf("invalid domain retry=%+v", c)
		}
	})
	t.Run("older source lacks optional tables", func(t *testing.T) {
		ctx, src, dest := personalMuteFixture(t)
		runPersonalMuteImport(t, ctx, src, dest, Options{})
	})
}
