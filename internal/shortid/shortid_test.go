package shortid

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Golden vectors: byte-identical to the TWIN suite in vidra-user
// (lib/short-id.test.ts). If a vector here ever disagrees with the one there,
// short links minted by one side stop resolving on the other — which is
// exactly the silent breakage this table exists to catch.
var goldenVectors = []struct {
	uuid string
	sid  string
}{
	{"6f2a1c3d-4b5e-4f60-8a71-9c0d2e3f4a5b", "EjArDZ8v19uX6BigXbAx5p"},
	{"0f8fad5b-d9cb-469f-a165-70867728950e", "2vTA15nAkb7x3Sp2dEi3i5"},
	// Leading zero BYTES must survive as leading '1' characters, or the id
	// stops round-tripping (the classic base58 bug).
	{"00000000-0000-4000-8000-000000000001", "1111114bZ6BZRUqUqZep"},
	{"00000000-0000-0000-0000-000000000000", "1111111111111111"},
	{"ffffffff-ffff-ffff-ffff-ffffffffffff", "YcVfxkQb6JRzqk5kF2tNLv"},
}

func TestFromUUIDGoldenVectors(t *testing.T) {
	for _, tc := range goldenVectors {
		t.Run(tc.uuid, func(t *testing.T) {
			got := FromUUID(uuid.MustParse(tc.uuid))
			if got != tc.sid {
				t.Fatalf("FromUUID(%s) = %q, want %q", tc.uuid, got, tc.sid)
			}
		})
	}
}

func TestToUUIDGoldenVectors(t *testing.T) {
	for _, tc := range goldenVectors {
		t.Run(tc.sid, func(t *testing.T) {
			got, ok := ToUUID(tc.sid)
			if !ok {
				t.Fatalf("ToUUID(%q) = _, false; want the uuid %s", tc.sid, tc.uuid)
			}
			if got != uuid.MustParse(tc.uuid) {
				t.Fatalf("ToUUID(%q) = %s, want %s", tc.sid, got, tc.uuid)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	ids := []uuid.UUID{
		uuid.MustParse("6f2a1c3d-4b5e-4f60-8a71-9c0d2e3f4a5b"),
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		uuid.Nil,
		uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		uuid.MustParse("1b4e28ba-2fa1-11d2-883f-0016d3cca427"),
	}
	// A pseudo-random spread as well: encoding is positional, so the interesting
	// failures hide in ids whose high bytes happen to be zero.
	for i := 0; i < 200; i++ {
		ids = append(ids, uuid.New())
	}
	for _, id := range ids {
		sid := FromUUID(id)
		if n := len(sid); n < 16 || n > 22 {
			t.Fatalf("FromUUID(%s) = %q: length %d outside the 16..22 bound", id, sid, n)
		}
		got, ok := ToUUID(sid)
		if !ok || got != id {
			t.Fatalf("round trip %s -> %q -> (%s, %v)", id, sid, got, ok)
		}
	}
}

func TestToUUIDRejects(t *testing.T) {
	tests := []struct {
		name string
		sid  string
	}{
		{"empty", ""},
		// 0, O, I and l are deliberately absent from the base58 alphabet.
		{"digit zero", "EjArDZ8v19uX6BigXbAx0p"},
		{"capital O", "EjArDZ8v19uX6BigXbAxOp"},
		{"capital I", "EjArDZ8v19uX6BigXbAxIp"},
		{"lowercase l", "EjArDZ8v19uX6BigXbAxlp"},
		{"punctuation", "Ej-ArDZ8v19uX6BigXbAx"},
		{"non ascii", "EjArDZ8v19uX6BigXbAx5é"},
		{"too short to be 16 bytes", "2g"},
		{"fifteen ones", "111111111111111"},
		{"decodes to more than 16 bytes", "zzzzzzzzzzzzzzzzzzzzzz"},
		{"twenty three chars", "EjArDZ8v19uX6BigXbAx5pQ"},
		// Non-canonical padding: a valid sid with an extra leading '1' decodes
		// to 17 bytes, not 16. The TWIN rejects it, so this side must too —
		// otherwise the two implementations disagree about which URLs exist.
		{"non-canonical leading one", "1" + "1111114bZ6BZRUqUqZep"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := ToUUID(tc.sid); ok {
				t.Fatalf("ToUUID(%q) = %s, true; want rejected", tc.sid, got)
			}
		})
	}
}

// Every sid ToUUID accepts must be exactly what FromUUID would have produced;
// two spellings of one video are two URLs, two cache keys and two canonicals.
func TestToUUIDAcceptsOnlyCanonicalForm(t *testing.T) {
	for _, tc := range goldenVectors {
		for _, pad := range []string{"1", "11"} {
			sid := pad + tc.sid
			if len(sid) > 22 {
				continue // the length bound already rejects these
			}
			if got, ok := ToUUID(sid); ok {
				t.Fatalf("ToUUID(%q) = %s, true; want rejected as non-canonical", sid, got)
			}
		}
	}
}

func TestFromUUIDAlphabetIsBitcoin(t *testing.T) {
	const want = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	if alphabet != want {
		t.Fatalf("alphabet = %q, want the Bitcoin base58 alphabet", alphabet)
	}
	for _, banned := range []string{"0", "O", "I", "l"} {
		if strings.Contains(alphabet, banned) {
			t.Errorf("alphabet contains the visually ambiguous %q", banned)
		}
	}
}
