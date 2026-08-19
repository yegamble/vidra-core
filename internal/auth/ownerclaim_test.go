package auth

import (
	"context"
	"errors"
	"testing"
)

// Owner-claim bootstrap tests (0104). The fake repo mirrors the SQL semantics:
// a single upserted row whose claimed_at-IS-NULL guard is the single-winner
// gate (the DB-level concurrency proof lives in the internal/store
// integration tests against real Postgres).

// mintOwnerClaim boots the claim flow the way cmd/api does and returns the raw
// token the operator would read off the console.
func mintOwnerClaim(t *testing.T, svc *Service) string {
	t.Helper()
	raw, minted, err := svc.EnsureOwnerClaimToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureOwnerClaimToken: %v", err)
	}
	if !minted {
		t.Fatal("EnsureOwnerClaimToken: minted = false, want a fresh token on an empty instance")
	}
	return raw
}

func claimInput(token string) ClaimOwnerInput {
	return ClaimOwnerInput{Token: token, Username: "ada", Email: "ada@example.test", Password: "supersecret"}
}

func TestEnsureOwnerClaimTokenStoresOnlyTheHash(t *testing.T) {
	repo := newFakeRepo()
	raw := mintOwnerClaim(t, newTestService(repo))
	if repo.ownerClaim == nil {
		t.Fatal("no owner-claim row minted")
	}
	if repo.ownerClaim.TokenHash == raw {
		t.Error("the raw token must never be persisted, only its hash")
	}
	if repo.ownerClaim.TokenHash != hashOwnerClaimToken(raw) {
		t.Error("stored hash does not match the returned token")
	}
}

func TestEnsureOwnerClaimTokenNoopOnceUsersExist(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test") // no token minted → registration open
	if _, minted, err := svc.EnsureOwnerClaimToken(context.Background()); err != nil || minted {
		t.Fatalf("minted=%v err=%v, want no-op on an instance with users (implicitly claimed)", minted, err)
	}
}

func TestEnsureOwnerClaimTokenRemintInvalidatesTheOld(t *testing.T) {
	svc := newTestService(newFakeRepo())
	old := mintOwnerClaim(t, svc)
	fresh := mintOwnerClaim(t, svc) // second boot while unclaimed
	if _, _, err := svc.ClaimOwner(context.Background(), claimInput(old), "ua"); !errors.Is(err, ErrOwnerClaimInvalid) {
		t.Fatalf("claim with the superseded token: err = %v, want ErrOwnerClaimInvalid", err)
	}
	if user, _, err := svc.ClaimOwner(context.Background(), claimInput(fresh), "ua"); err != nil || user.Role != "admin" {
		t.Fatalf("claim with the fresh token: role=%q err=%v, want admin/nil", user.Role, err)
	}
}

func TestSignupPathsRefuseWhileOwnerUnclaimed(t *testing.T) {
	svc := newTestService(newFakeRepo())
	mintOwnerClaim(t, svc)
	ctx := context.Background()
	in := RegisterInput{Username: "bot", Email: "bot@example.test", Password: "supersecret"}

	t.Run("register", func(t *testing.T) {
		if _, _, err := svc.Register(ctx, in, "ua"); !errors.Is(err, ErrOwnerClaimRequired) {
			t.Errorf("err = %v, want ErrOwnerClaimRequired", err)
		}
	})
	t.Run("register pending verification", func(t *testing.T) {
		if _, err := svc.RegisterPendingVerification(ctx, in); !errors.Is(err, ErrOwnerClaimRequired) {
			t.Errorf("err = %v, want ErrOwnerClaimRequired", err)
		}
	})
	t.Run("registration request", func(t *testing.T) {
		if _, err := svc.RequestRegistration(ctx, in, "please"); !errors.Is(err, ErrOwnerClaimRequired) {
			t.Errorf("err = %v, want ErrOwnerClaimRequired", err)
		}
	})
}

func TestOAuthSignupRefusedWhileOwnerUnclaimed(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	mintOwnerClaim(t, s.auth)
	_, _, _, err := s.resolveIdentity(context.Background(), "fake", "sub-1", oidcClaims{
		Email: "new@example.com", EmailVerified: true, Name: "New Person",
	}, "ua")
	if !errors.Is(err, ErrOwnerClaimRequired) {
		t.Fatalf("err = %v, want ErrOwnerClaimRequired", err)
	}
	if n, _ := repo.CountUsers(context.Background()); n != 0 {
		t.Errorf("users = %d, want 0 (no account may be created while unclaimed)", n)
	}
}

func TestATProtoSignupRefusedWhileOwnerUnclaimed(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newATProtoTestService(repo, defaultFlow(), true)
	mintOwnerClaim(t, s.auth)
	ctx := context.Background()
	_, st, err := s.Begin(ctx, "alice.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Complete(ctx, st, "code", st.Issuer, "ua"); !errors.Is(err, ErrOwnerClaimRequired) {
		t.Fatalf("err = %v, want ErrOwnerClaimRequired", err)
	}
	if n, _ := repo.CountUsers(ctx); n != 0 {
		t.Errorf("users = %d, want 0 (no account may be created while unclaimed)", n)
	}
}

func TestClaimOwnerCreatesAdminAndOpensRegistration(t *testing.T) {
	svc := newTestService(newFakeRepo())
	raw := mintOwnerClaim(t, svc)
	ctx := context.Background()

	user, tokens, err := svc.ClaimOwner(ctx, claimInput(raw), "ua")
	if err != nil {
		t.Fatalf("ClaimOwner: %v", err)
	}
	if user.Role != "admin" || user.Username != "ada" {
		t.Errorf("claimed user = %q role %q, want ada/admin", user.Username, user.Role)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Error("claim must issue a full session")
	}

	// The claim opens normal registration — as a plain user.
	second, _ := register(t, svc, "bob", "bob@example.test")
	if second.Role != "user" {
		t.Errorf("post-claim registrant role = %q, want user", second.Role)
	}
}

func TestClaimOwnerWrongTokenFailsAndKeepsTheClaimPending(t *testing.T) {
	svc := newTestService(newFakeRepo())
	raw := mintOwnerClaim(t, svc)
	ctx := context.Background()

	if _, _, err := svc.ClaimOwner(ctx, claimInput("not-the-token"), "ua"); !errors.Is(err, ErrOwnerClaimInvalid) {
		t.Fatalf("err = %v, want ErrOwnerClaimInvalid", err)
	}
	// The failed guess burned nothing: registration is still refused and the
	// real token still works.
	in := RegisterInput{Username: "bot", Email: "bot@example.test", Password: "supersecret"}
	if _, _, err := svc.Register(ctx, in, "ua"); !errors.Is(err, ErrOwnerClaimRequired) {
		t.Fatalf("register after bad guess: err = %v, want ErrOwnerClaimRequired", err)
	}
	if user, _, err := svc.ClaimOwner(ctx, claimInput(raw), "ua"); err != nil || user.Role != "admin" {
		t.Fatalf("claim with the real token: role=%q err=%v, want admin/nil", user.Role, err)
	}
}

func TestClaimOwnerIsSingleUse(t *testing.T) {
	svc := newTestService(newFakeRepo())
	raw := mintOwnerClaim(t, svc)
	ctx := context.Background()
	if _, _, err := svc.ClaimOwner(ctx, claimInput(raw), "ua"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	in := ClaimOwnerInput{Token: raw, Username: "eve", Email: "eve@example.test", Password: "supersecret"}
	if _, _, err := svc.ClaimOwner(ctx, in, "ua"); !errors.Is(err, ErrOwnerClaimInvalid) {
		t.Fatalf("second claim: err = %v, want ErrOwnerClaimInvalid", err)
	}
}

func TestClaimOwnerWithoutPendingClaimIsInvalid(t *testing.T) {
	// No token was ever minted (e.g. an instance upgraded with users, or a
	// pre-mint request): the endpoint answers the same non-probing error.
	svc := newTestService(newFakeRepo())
	if _, _, err := svc.ClaimOwner(context.Background(), claimInput("anything"), "ua"); !errors.Is(err, ErrOwnerClaimInvalid) {
		t.Fatalf("err = %v, want ErrOwnerClaimInvalid", err)
	}
}
