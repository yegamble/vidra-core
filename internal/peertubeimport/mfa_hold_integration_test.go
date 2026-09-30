//go:build integration

package peertubeimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestPeerTubeImportMFARequiresNativeProtection(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	for _, repair := range []bool{false, true} {
		name := "initial"
		if repair {
			name = "legacy repair"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			src, _ := newScratchDB(t, ctx, base)
			dest, _ := newScratchDB(t, ctx, base)
			applyMigrations(t, ctx, dest)
			hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
			seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
			mustExec(t, ctx, src, `UPDATE "user" SET blocked=false`)
			svc := auth.NewService(sqlcgen.New(dest), auth.NewTokenIssuer("fixture-signing-secret", "vidra", "vidra", time.Hour), time.Hour)
			var priorClaims *auth.Claims
			run := func() *Report {
				t.Helper()
				r, err := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, MediaMode: MediaModeNone, SourceAuthoritative: repair}).Run(ctx, 800, nil)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			if repair {
				run()
				login, err := svc.Login(ctx, auth.LoginInput{Identifier: "alice", Password: testPassword}, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				priorClaims, err = svc.Parse(login.Tokens.AccessToken)
				if err != nil {
					t.Fatal(err)
				}
				mustExec(t, ctx, dest, `UPDATE peertube_import_ledger SET created_by_import=false WHERE entity_kind='user' AND source_id='2'`)
			}
			mustExec(t, ctx, src, `ALTER TABLE "user" ADD COLUMN "otpSecret" text; UPDATE "user" SET "otpSecret"='fixture-encrypted-secret-never-export'`)
			source := NewSourceFromPool(src)
			users, err := source.Users(ctx)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(users)
			if strings.Contains(string(encoded), "fixture-encrypted-secret") {
				t.Fatal("source OTP ciphertext left SQL")
			}
			plan, err := NewImporter(dest, source, Options{Policy: PolicyFail, MediaMode: MediaModeNone}).Plan(ctx, 800)
			if err != nil {
				t.Fatal(err)
			}
			if c := plan.Entities["user_mfa_hold"]; c == nil || c.Planned != 2 {
				t.Errorf("MFA plan=%+v", c)
			}
			if repair {
				// Gap filling does not repair accounts imported by older versions.
				gap, err := NewImporter(dest, source, Options{Policy: PolicyFail, MediaMode: MediaModeNone}).Run(ctx, 800, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.Join(gap.Deferred, " "), "ordinary reruns do not repair existing accounts") {
					t.Error("ordinary rerun omitted the required authoritative-repair warning")
				}
				if !readUserActive(t, ctx, dest, "alice") {
					t.Fatal("ordinary rerun changed an existing account")
				}
				if _, err := svc.AuthenticateAccessToken(ctx, priorClaims); err != nil {
					t.Fatalf("session invalid before repair: %v", err)
				}
			}
			r := run()
			if readUserActive(t, ctx, dest, "alice") {
				t.Error("source MFA account arrived/remained active")
			}
			if repair && !readUserActive(t, ctx, dest, "bob") {
				t.Error("linked native account was held")
			}
			if c := r.Entities["user_mfa_hold"]; c == nil || (repair && c.Updated != 1) || (!repair && c.Imported != 2) {
				t.Errorf("MFA hold report=%+v", c)
			}
			if repair {
				if _, err := svc.AuthenticateAccessToken(ctx, priorClaims); !errors.Is(err, auth.ErrSessionRevoked) {
					t.Errorf("held access session=%v", err)
				}
			}
			if _, err := svc.Login(ctx, auth.LoginInput{Identifier: "alice", Password: testPassword}, "fixture"); !errors.Is(err, auth.ErrAccountDisabled) {
				t.Errorf("hold login=%v", err)
			}
			if got := scanStrings(t, ctx, dest, `SELECT count(*)::text FROM sessions WHERE revoked_at IS NULL`); len(got) != 1 || got[0] != "0" {
				t.Errorf("held sessions remain=%v", got)
			}
			mustExec(t, ctx, src, `UPDATE "user" SET "otpSecret"=NULL,"emailVerified"=false WHERE id=1`)
			run()
			if readUserActive(t, ctx, dest, "alice") {
				t.Error("rerun released hold")
			}
			if repair {
				// Only confirmed native enrollment exempts an account from the hold.
				mustExec(t, ctx, src, `UPDATE "user" SET "otpSecret"='fixture-encrypted' WHERE id=1`)
				mustExec(t, ctx, dest, `UPDATE users SET is_active=true WHERE username='alice'; INSERT INTO user_mfa(user_id,totp_secret_sealed,enabled) SELECT id,'fixture-native-secret',false FROM users WHERE username='alice'`)
				run()
				if readUserActive(t, ctx, dest, "alice") {
					t.Error("pending enrollment bypassed hold")
				}
				mustExec(t, ctx, dest, `UPDATE users SET is_active=true WHERE username='alice'; UPDATE user_mfa SET enabled=true`)
				run()
				if !readUserActive(t, ctx, dest, "alice") {
					t.Error("native protected account was held")
				}
			}
		})
	}
}
