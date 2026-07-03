package donation

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- pure validation tables ----------------------------------------------

func TestValidAddressPerNetwork(t *testing.T) {
	cases := []struct {
		network string
		address string
		want    bool
	}{
		// bitcoin
		{"bitcoin", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", true},         // P2PKH
		{"bitcoin", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", true},         // P2SH
		{"bitcoin", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", true}, // bech32
		{"bitcoin", "0x52908400098527886E0F7030069857D2E4169EE7", false},
		{"bitcoin", "", false},
		{"bitcoin", "notanaddress", false},
		// ethereum
		{"ethereum", "0x52908400098527886E0F7030069857D2E4169EE7", true}, // checksummed
		{"ethereum", "0x8617e340b3d01fa5f11f306f4090fd50e238070d", true}, // lowercase
		{"ethereum", "52908400098527886E0F7030069857D2E4169EE7", false},  // missing 0x
		{"ethereum", "0x123", false},
		{"ethereum", "0xZZ908400098527886E0F7030069857D2E4169EE7", false},
		// litecoin
		{"litecoin", "Ld5vv9oCUpguKMR1Es5gTZuNJK2rSjKbYq", true},
		{"litecoin", "ltc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", true},
		{"litecoin", "0x52908400098527886E0F7030069857D2E4169EE7", false},
		// monero (95-char standard address)
		{"monero", "4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx3skxNgYeYTRj5UzqtReoS44qo9mtmXCqY45DJ852K5Jv2684Rge", true},
		{"monero", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", false},
		{"monero", "48tooShortAddress", false},
		// unknown network
		{"dogecoin", "DPo1234567890abcdefghijklmnopqrstuv", false},
	}
	for _, tc := range cases {
		if got := ValidAddress(tc.network, tc.address); got != tc.want {
			t.Errorf("ValidAddress(%q, %q) = %v, want %v", tc.network, tc.address, got, tc.want)
		}
	}
}

func TestIsKnownNetworkAndVerificationSupport(t *testing.T) {
	for _, n := range []string{"bitcoin", "ethereum", "litecoin", "monero"} {
		if !IsKnownNetwork(n) {
			t.Errorf("IsKnownNetwork(%q) = false, want true", n)
		}
	}
	if IsKnownNetwork("dogecoin") {
		t.Error("IsKnownNetwork(dogecoin) = true, want false")
	}
	if !SupportsVerification("ethereum") {
		t.Error("ethereum should support verification")
	}
	for _, n := range []string{"bitcoin", "litecoin", "monero"} {
		if SupportsVerification(n) {
			t.Errorf("SupportsVerification(%q) = true, want false (unsupported in v1)", n)
		}
	}
}

// --- service behaviour ----------------------------------------------------

func TestAddValidatesNetworkAndAddress(t *testing.T) {
	svc := NewService(newDonationFakeRepo(), "vidra.test")
	owner := uuid.New()
	if _, err := svc.Add(context.Background(), owner, AddInput{Network: "dogecoin", Address: "x"}); !errors.Is(err, ErrInvalidNetwork) {
		t.Fatalf("bad network err = %v, want ErrInvalidNetwork", err)
	}
	if _, err := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: "0x123"}); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("bad address err = %v, want ErrInvalidAddress", err)
	}
}

func TestAddDuplicateConflict(t *testing.T) {
	svc := NewService(newDonationFakeRepo(), "vidra.test")
	owner := uuid.New()
	in := AddInput{Network: "ethereum", Address: "0x52908400098527886E0F7030069857D2E4169EE7"}
	if _, err := svc.Add(context.Background(), owner, in); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := svc.Add(context.Background(), owner, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate err = %v, want ErrConflict", err)
	}
}

func TestChannelScopeOwnershipEnforced(t *testing.T) {
	repo := newDonationFakeRepo()
	owner := uuid.New()
	other := uuid.New()
	ch := sqlcgen.Channel{ID: uuid.New(), OwnerID: other}
	repo.channels[ch.ID] = ch
	svc := NewService(repo, "vidra.test")

	// Adding to a channel you do not own is forbidden.
	if _, err := svc.Add(context.Background(), owner, AddInput{
		ChannelID: &ch.ID, Network: "ethereum", Address: "0x52908400098527886E0F7030069857D2E4169EE7",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign channel err = %v, want ErrForbidden", err)
	}
	// Unknown channel → not found.
	missing := uuid.New()
	if _, err := svc.Add(context.Background(), owner, AddInput{
		ChannelID: &missing, Network: "ethereum", Address: "0x52908400098527886E0F7030069857D2E4169EE7",
	}); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("unknown channel err = %v, want ErrChannelNotFound", err)
	}
}

func TestDeleteOwnershipEnforced(t *testing.T) {
	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner, other := uuid.New(), uuid.New()
	row, err := svc.Add(context.Background(), owner, AddInput{Network: "monero", Address: "4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx3skxNgYeYTRj5UzqtReoS44qo9mtmXCqY45DJ852K5Jv2684Rge"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.Delete(context.Background(), other, row.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner delete err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), owner, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown delete err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), owner, row.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestChallengeUnsupportedNetwork(t *testing.T) {
	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	row, err := svc.Add(context.Background(), owner, AddInput{Network: "bitcoin", Address: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := svc.Challenge(context.Background(), owner, row.ID); !errors.Is(err, ErrVerificationUnsupported) {
		t.Fatalf("bitcoin challenge err = %v, want ErrVerificationUnsupported", err)
	}
	if _, err := svc.Verify(context.Background(), owner, row.ID, "0x00"); !errors.Is(err, ErrVerificationUnsupported) {
		t.Fatalf("bitcoin verify err = %v, want ErrVerificationUnsupported", err)
	}
}

func TestEthereumChallengeVerifyRoundTrip(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	addr := ethereumAddressFromPubKey(priv.PubKey())

	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	row, err := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: addr})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if row.Verified {
		t.Fatal("new address should be unverified")
	}

	message, _, err := svc.Challenge(context.Background(), owner, row.ID)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	sig := signEthPersonal(t, priv, message)

	verified, err := svc.Verify(context.Background(), owner, row.ID, sig)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verified.Verified {
		t.Fatal("address should be verified after a valid signature")
	}
	// The challenge is cleared after success.
	if verified.VerificationNonce != nil || verified.VerificationExpiresAt.Valid {
		t.Error("challenge fields should be cleared after verification")
	}
}

func TestVerifyWrongSignatureMismatch(t *testing.T) {
	priv, _ := secp256k1.GeneratePrivateKey()
	other, _ := secp256k1.GeneratePrivateKey()
	addr := ethereumAddressFromPubKey(priv.PubKey())

	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	row, _ := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: addr})
	message, _, _ := svc.Challenge(context.Background(), owner, row.ID)

	// Signed by a DIFFERENT key → recovers a different address → mismatch.
	sig := signEthPersonal(t, other, message)
	if _, err := svc.Verify(context.Background(), owner, row.ID, sig); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("wrong-signer err = %v, want ErrSignatureMismatch", err)
	}

	// Malformed signature → bad signature.
	if _, err := svc.Verify(context.Background(), owner, row.ID, "0xdeadbeef"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("malformed err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyNonceMismatch(t *testing.T) {
	priv, _ := secp256k1.GeneratePrivateKey()
	addr := ethereumAddressFromPubKey(priv.PubKey())
	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	row, _ := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: addr})

	// Sign the FIRST challenge, then re-issue a new one (rotating the nonce).
	stale, _, _ := svc.Challenge(context.Background(), owner, row.ID)
	sig := signEthPersonal(t, priv, stale)
	if _, _, err := svc.Challenge(context.Background(), owner, row.ID); err != nil {
		t.Fatalf("re-challenge: %v", err)
	}
	// The stored nonce is now the newer one, so the stale signature verifies
	// against a different message and fails to recover the owner's address.
	if _, err := svc.Verify(context.Background(), owner, row.ID, sig); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("stale-nonce err = %v, want ErrSignatureMismatch", err)
	}
}

func TestVerifyChallengeExpiry(t *testing.T) {
	priv, _ := secp256k1.GeneratePrivateKey()
	addr := ethereumAddressFromPubKey(priv.PubKey())
	repo := newDonationFakeRepo()
	now := time.Now()
	svc := NewService(repo, "vidra.test", WithClock(func() time.Time { return now }))
	owner := uuid.New()
	row, _ := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: addr})

	message, _, err := svc.Challenge(context.Background(), owner, row.ID)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	sig := signEthPersonal(t, priv, message)

	// Advance the clock past the 10-minute expiry.
	now = now.Add(11 * time.Minute)
	if _, err := svc.Verify(context.Background(), owner, row.ID, sig); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired err = %v, want ErrChallengeExpired", err)
	}
}

func TestVerifyNoActiveChallenge(t *testing.T) {
	priv, _ := secp256k1.GeneratePrivateKey()
	addr := ethereumAddressFromPubKey(priv.PubKey())
	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	row, _ := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: addr})
	if _, err := svc.Verify(context.Background(), owner, row.ID, "0x"+hex.EncodeToString(make([]byte, 65))); !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("no-challenge err = %v, want ErrNoChallenge", err)
	}
}

func TestListSeparatesAccountAndChannel(t *testing.T) {
	repo := newDonationFakeRepo()
	svc := NewService(repo, "vidra.test")
	owner := uuid.New()
	ch := sqlcgen.Channel{ID: uuid.New(), OwnerID: owner}
	repo.channels[ch.ID] = ch

	if _, err := svc.Add(context.Background(), owner, AddInput{Network: "ethereum", Address: "0x52908400098527886E0F7030069857D2E4169EE7"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(context.Background(), owner, AddInput{ChannelID: &ch.ID, Network: "bitcoin", Address: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}); err != nil {
		t.Fatal(err)
	}

	own, _ := svc.ListOwn(context.Background(), owner)
	if len(own) != 2 {
		t.Fatalf("ListOwn = %d, want 2", len(own))
	}
	acct, _ := svc.ListForUser(context.Background(), owner)
	if len(acct) != 1 || acct[0].ChannelID.Valid {
		t.Fatalf("ListForUser should return the 1 account-level address, got %d", len(acct))
	}
	chAddrs, _ := svc.ListForChannel(context.Background(), ch.ID)
	if len(chAddrs) != 1 || chAddrs[0].Network != "bitcoin" {
		t.Fatalf("ListForChannel should return the 1 channel-scoped address, got %d", len(chAddrs))
	}
}

// signEthPersonal signs message as an EIP-191 personal_sign and returns the
// 0x-prefixed 65-byte r||s||v hex signature Vidra's verifier accepts.
func signEthPersonal(t *testing.T, priv *secp256k1.PrivateKey, message string) string {
	t.Helper()
	compact := ecdsa.SignCompact(priv, ethPersonalHash(message), false)
	// decred compact layout is [27+recid] || r || s; Ethereum wants r || s || v.
	eth := make([]byte, 65)
	copy(eth[0:64], compact[1:65])
	eth[64] = compact[0]
	return "0x" + hex.EncodeToString(eth)
}

// --- in-memory fake repository -------------------------------------------

type donationFakeRepo struct {
	rows     map[uuid.UUID]sqlcgen.DonationAddress
	channels map[uuid.UUID]sqlcgen.Channel
}

func newDonationFakeRepo() *donationFakeRepo {
	return &donationFakeRepo{
		rows:     map[uuid.UUID]sqlcgen.DonationAddress{},
		channels: map[uuid.UUID]sqlcgen.Channel{},
	}
}

func (f *donationFakeRepo) CreateDonationAddress(_ context.Context, a sqlcgen.CreateDonationAddressParams) (sqlcgen.DonationAddress, error) {
	for _, r := range f.rows {
		if r.OwnerID == a.OwnerID && r.Network == a.Network && r.Address == a.Address && sameScope(r.ChannelID, a.ChannelID) {
			return sqlcgen.DonationAddress{}, &pgconn.PgError{Code: "23505"}
		}
	}
	row := sqlcgen.DonationAddress{
		ID: uuid.New(), OwnerID: a.OwnerID, ChannelID: a.ChannelID,
		Network: a.Network, Address: a.Address, Label: a.Label,
		CreatedAt: time.Now(),
	}
	f.rows[row.ID] = row
	return row, nil
}

func (f *donationFakeRepo) GetDonationAddress(_ context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error) {
	row, ok := f.rows[id]
	if !ok {
		return sqlcgen.DonationAddress{}, errors.New("not found")
	}
	return row, nil
}

func (f *donationFakeRepo) ListDonationAddressesByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if r.OwnerID == ownerID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationFakeRepo) ListAccountDonationAddresses(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if r.OwnerID == ownerID && !r.ChannelID.Valid {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationFakeRepo) ListChannelDonationAddresses(_ context.Context, channelID pgtype.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if sameScope(r.ChannelID, channelID) && channelID.Valid {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationFakeRepo) DeleteDonationAddress(_ context.Context, id uuid.UUID) error {
	delete(f.rows, id)
	return nil
}

func (f *donationFakeRepo) SetDonationAddressChallenge(_ context.Context, a sqlcgen.SetDonationAddressChallengeParams) (sqlcgen.DonationAddress, error) {
	row, ok := f.rows[a.ID]
	if !ok {
		return sqlcgen.DonationAddress{}, errors.New("not found")
	}
	row.VerificationNonce = a.VerificationNonce
	row.VerificationExpiresAt = a.VerificationExpiresAt
	f.rows[a.ID] = row
	return row, nil
}

func (f *donationFakeRepo) MarkDonationAddressVerified(_ context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error) {
	row, ok := f.rows[id]
	if !ok {
		return sqlcgen.DonationAddress{}, errors.New("not found")
	}
	row.Verified = true
	row.VerificationNonce = nil
	row.VerificationExpiresAt = pgtype.Timestamptz{}
	f.rows[id] = row
	return row, nil
}

func (f *donationFakeRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	ch, ok := f.channels[id]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func sameScope(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	return a.Bytes == b.Bytes
}
