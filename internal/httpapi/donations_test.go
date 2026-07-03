package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/sha3"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/donation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// donationServer wires real auth + channel + donation services over in-memory
// fakes. The donation fake resolves channel ownership through the same channel
// fake the channel service uses, so a channel created via the API is visible to
// both. buf, when non-nil, captures structured logs for audit assertions.
func donationServer(t *testing.T, buf *bytes.Buffer) *Server {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chanRepo := newChannelFakeRepo()
	chansvc := channel.NewService(chanRepo)
	donsvc := donation.NewService(&donationHTTPFakeRepo{rows: map[uuid.UUID]sqlcgen.DonationAddress{}, chans: chanRepo}, "vidra.test")
	opts := []Option{
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(chansvc),
		WithDonationService(donsvc),
	}
	if buf != nil {
		opts = append(opts, WithLogger(slog.New(slog.NewJSONHandler(buf, nil))))
	}
	return New(testConfig(), nil, nil, opts...)
}

func TestAddDonationAddressRequiresAuth(t *testing.T) {
	srv := donationServer(t, nil)
	rec := postTo(srv, "/api/v1/me/donation-addresses", `{"network":"bitcoin","address":"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAddDonationAddressValidation(t *testing.T) {
	srv := donationServer(t, nil)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	// Unsupported network.
	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"dogecoin","address":"x"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad network status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Malformed address for a valid network.
	rec = postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"ethereum","address":"0x123"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad address status = %d, want 422", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) == 0 {
		t.Errorf("expected field errors, got %+v", er.Error)
	}
}

func TestDonationAddressCRUDAndPublicRead(t *testing.T) {
	srv := donationServer(t, nil)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Add an account-level address.
	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"bitcoin","address":"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq","label":"Tips"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created donationAddressView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Verified {
		t.Error("new address should be unverified")
	}
	if created.ChannelID != nil {
		t.Error("account-level address should have no channel_id")
	}

	// List own.
	list := getWithAuth(srv, "/api/v1/me/donation-addresses", tok)
	var owned donationAddressListResponse
	_ = json.Unmarshal(list.Body.Bytes(), &owned)
	if len(owned.Addresses) != 1 {
		t.Fatalf("list own = %d, want 1", len(owned.Addresses))
	}
	ownerID := owned.Addresses[0].OwnerID

	// Public read by user id (no auth) returns the same account-level address.
	pub := doGet(srv, "/api/v1/users/"+ownerID+"/donation-addresses")
	if pub.Code != http.StatusOK {
		t.Fatalf("public user read = %d, want 200", pub.Code)
	}
	var pubList donationAddressListResponse
	_ = json.Unmarshal(pub.Body.Bytes(), &pubList)
	if len(pubList.Addresses) != 1 || pubList.Addresses[0].Address != "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq" {
		t.Fatalf("public read unexpected: %+v", pubList.Addresses)
	}

	// Delete it; then it's gone.
	del := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/donation-addresses/"+created.ID, "", tok)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", del.Code)
	}
	after := getWithAuth(srv, "/api/v1/me/donation-addresses", tok)
	var empty donationAddressListResponse
	_ = json.Unmarshal(after.Body.Bytes(), &empty)
	if len(empty.Addresses) != 0 {
		t.Fatalf("after delete = %d, want 0", len(empty.Addresses))
	}
}

func TestDonationAddressChannelScopeAndPublicChannelRead(t *testing.T) {
	srv := donationServer(t, nil)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Create a channel the caller owns.
	chRec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, tok)
	var ch channelView
	_ = json.Unmarshal(chRec.Body.Bytes(), &ch)

	// Add a channel-scoped donation address.
	body := `{"channel_id":"` + ch.ID + `","network":"litecoin","address":"ltc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"}`
	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", body, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("channel add = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// It shows on the channel's public read but NOT on the user's account read.
	chPub := doGet(srv, "/api/v1/channels/ada_makes/donation-addresses")
	var chList donationAddressListResponse
	_ = json.Unmarshal(chPub.Body.Bytes(), &chList)
	if len(chList.Addresses) != 1 || chList.Addresses[0].ChannelID == nil {
		t.Fatalf("channel public read unexpected: %+v", chList.Addresses)
	}

	userPub := doGet(srv, "/api/v1/users/"+ch.OwnerID+"/donation-addresses")
	var userList donationAddressListResponse
	_ = json.Unmarshal(userPub.Body.Bytes(), &userList)
	if len(userList.Addresses) != 0 {
		t.Fatalf("channel-scoped address must not appear on the account read, got %d", len(userList.Addresses))
	}

	// Unknown channel handle → 404.
	if miss := doGet(srv, "/api/v1/channels/ghost/donation-addresses"); miss.Code != http.StatusNotFound {
		t.Fatalf("unknown channel read = %d, want 404", miss.Code)
	}
}

func TestDonationAddressDeleteNonOwner(t *testing.T) {
	srv := donationServer(t, nil)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"ethereum","address":"0x52908400098527886E0F7030069857D2E4169EE7"}`, ownerTok)
	var created donationAddressView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/donation-addresses/"+created.ID, "", otherTok); bad.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete = %d, want 403", bad.Code)
	}
}

func TestEthereumChallengeVerifyOverHTTP(t *testing.T) {
	var buf bytes.Buffer
	srv := donationServer(t, &buf)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	addr := ethAddressForTest(priv.PubKey())

	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"ethereum","address":"`+addr+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created donationAddressView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Request a challenge.
	chRec := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/challenge", "", tok)
	if chRec.Code != http.StatusCreated {
		t.Fatalf("challenge = %d, want 201; body=%s", chRec.Code, chRec.Body.String())
	}
	var ch donationChallengeResponse
	_ = json.Unmarshal(chRec.Body.Bytes(), &ch)
	if ch.Message == "" {
		t.Fatal("challenge message is empty")
	}

	// Sign and verify.
	sig := signEthForTest(t, priv, ch.Message)
	vRec := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/verify", `{"signature":"`+sig+`"}`, tok)
	if vRec.Code != http.StatusOK {
		t.Fatalf("verify = %d, want 200; body=%s", vRec.Code, vRec.Body.String())
	}
	var verified donationAddressView
	_ = json.Unmarshal(vRec.Body.Bytes(), &verified)
	if !verified.Verified {
		t.Fatal("address should be verified after signing")
	}

	// No-secrets audit: the verify event is present, carries no denylisted key,
	// and the raw signature never appears anywhere in the logs.
	events := auditEvents(t, &buf)
	if findAudit(events, "content.donation.verify", "success") == nil {
		t.Error("expected a content.donation.verify success audit event")
	}
	assertNoDenylistedKeys(t, &buf)
	if strings.Contains(buf.String(), strings.TrimPrefix(sig, "0x")) {
		t.Error("the signature leaked into the logs")
	}

	// A wrong signature is rejected (re-challenge first since the last succeeded).
	chRec2 := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/challenge", "", tok)
	var ch2 donationChallengeResponse
	_ = json.Unmarshal(chRec2.Body.Bytes(), &ch2)
	other, _ := secp256k1.GeneratePrivateKey()
	badSig := signEthForTest(t, other, ch2.Message)
	if bad := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/verify", `{"signature":"`+badSig+`"}`, tok); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong signature = %d, want 422", bad.Code)
	}
}

func TestBitcoinVerificationUnsupported501(t *testing.T) {
	srv := donationServer(t, nil)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/me/donation-addresses", `{"network":"bitcoin","address":"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}`, tok)
	var created donationAddressView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Bitcoin has no verification path in v1: both challenge and verify are 501.
	if ch := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/challenge", "", tok); ch.Code != http.StatusNotImplemented {
		t.Fatalf("btc challenge = %d, want 501", ch.Code)
	}
	v := postJSONAuth(srv, "/api/v1/me/donation-addresses/"+created.ID+"/verify", `{"signature":"0xdeadbeef"}`, tok)
	if v.Code != http.StatusNotImplemented {
		t.Fatalf("btc verify = %d, want 501", v.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(v.Body.Bytes(), &er)
	if er.Error.Code != "not_implemented" {
		t.Errorf("501 error code = %q, want not_implemented", er.Error.Code)
	}
}

// --- test crypto helpers (exercise the real HTTP verify path) -------------

func ethAddressForTest(pub *secp256k1.PublicKey) string {
	uncompressed := pub.SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	sum := h.Sum(nil)
	return "0x" + hex.EncodeToString(sum[12:])
}

func signEthForTest(t *testing.T, priv *secp256k1.PrivateKey, message string) string {
	t.Helper()
	prefix := "\x19Ethereum Signed Message:\n" + strconv.Itoa(len(message))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(prefix))
	h.Write([]byte(message))
	compact := ecdsa.SignCompact(priv, h.Sum(nil), false)
	eth := make([]byte, 65)
	copy(eth[0:64], compact[1:65])
	eth[64] = compact[0]
	return "0x" + hex.EncodeToString(eth)
}

func doGet(srv *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func assertNoDenylistedKeys(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		for k := range rec {
			if isDenylistedLogKey(k) {
				t.Errorf("log line contains denylisted key %q: %s", k, line)
			}
		}
	}
}

// isDenylistedLogKey mirrors observability.IsSensitiveKey for a handful of keys
// this slice could conceivably (but must not) log.
func isDenylistedLogKey(k string) bool {
	switch strings.ToLower(k) {
	case "signature", "private_key", "secret", "token", "password":
		return true
	}
	return false
}

// --- in-memory donation.Repository for HTTP handler tests -----------------

type donationHTTPFakeRepo struct {
	rows  map[uuid.UUID]sqlcgen.DonationAddress
	chans *channelFakeRepo
}

func (f *donationHTTPFakeRepo) CreateDonationAddress(_ context.Context, a sqlcgen.CreateDonationAddressParams) (sqlcgen.DonationAddress, error) {
	for _, r := range f.rows {
		if r.OwnerID == a.OwnerID && r.Network == a.Network && r.Address == a.Address && scopeEqual(r.ChannelID, a.ChannelID) {
			return sqlcgen.DonationAddress{}, &pgconn.PgError{Code: "23505"}
		}
	}
	row := sqlcgen.DonationAddress{
		ID: uuid.New(), OwnerID: a.OwnerID, ChannelID: a.ChannelID,
		Network: a.Network, Address: a.Address, Label: a.Label, CreatedAt: time.Now(),
	}
	f.rows[row.ID] = row
	return row, nil
}

func (f *donationHTTPFakeRepo) GetDonationAddress(_ context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error) {
	row, ok := f.rows[id]
	if !ok {
		return sqlcgen.DonationAddress{}, errors.New("not found")
	}
	return row, nil
}

func (f *donationHTTPFakeRepo) ListDonationAddressesByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if r.OwnerID == ownerID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationHTTPFakeRepo) ListAccountDonationAddresses(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if r.OwnerID == ownerID && !r.ChannelID.Valid {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationHTTPFakeRepo) ListChannelDonationAddresses(_ context.Context, channelID pgtype.UUID) ([]sqlcgen.DonationAddress, error) {
	var out []sqlcgen.DonationAddress
	for _, r := range f.rows {
		if channelID.Valid && scopeEqual(r.ChannelID, channelID) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *donationHTTPFakeRepo) DeleteDonationAddress(_ context.Context, id uuid.UUID) error {
	delete(f.rows, id)
	return nil
}

func (f *donationHTTPFakeRepo) SetDonationAddressChallenge(_ context.Context, a sqlcgen.SetDonationAddressChallengeParams) (sqlcgen.DonationAddress, error) {
	row, ok := f.rows[a.ID]
	if !ok {
		return sqlcgen.DonationAddress{}, errors.New("not found")
	}
	row.VerificationNonce = a.VerificationNonce
	row.VerificationExpiresAt = a.VerificationExpiresAt
	f.rows[a.ID] = row
	return row, nil
}

func (f *donationHTTPFakeRepo) MarkDonationAddressVerified(_ context.Context, id uuid.UUID) (sqlcgen.DonationAddress, error) {
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

func (f *donationHTTPFakeRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	for _, ch := range f.chans.byHandle {
		if ch.ID == id {
			return ch, nil
		}
	}
	return sqlcgen.Channel{}, errors.New("not found")
}

func scopeEqual(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	return a.Bytes == b.Bytes
}
