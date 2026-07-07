package video

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestValidateEmbedPrivacy is the pure validation matrix for the embed policy.
func TestValidateEmbedPrivacy(t *testing.T) {
	cases := []struct {
		name    string
		in      EmbedPrivacy
		wantErr bool
		want    EmbedPrivacy
	}{
		{"enabled", EmbedPrivacy{Status: EmbedEnabled}, false, EmbedPrivacy{Status: EmbedEnabled, AllowedDomains: []string{}}},
		{"disabled", EmbedPrivacy{Status: EmbedDisabled}, false, EmbedPrivacy{Status: EmbedDisabled, AllowedDomains: []string{}}},
		{"unknown status", EmbedPrivacy{Status: "nope"}, true, EmbedPrivacy{}},
		{"domains without whitelist", EmbedPrivacy{Status: EmbedEnabled, AllowedDomains: []string{"example.com"}}, true, EmbedPrivacy{}},
		{"whitelist empty", EmbedPrivacy{Status: EmbedWhitelist}, true, EmbedPrivacy{}},
		{"whitelist blank-only", EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"  "}}, true, EmbedPrivacy{}},
		{"whitelist with scheme", EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"https://example.com"}}, true, EmbedPrivacy{}},
		{"whitelist with path", EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"example.com/embed"}}, true, EmbedPrivacy{}},
		{"whitelist with port", EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"example.com:8080"}}, true, EmbedPrivacy{}},
		{
			"whitelist normalizes + dedupes",
			EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"Example.com", "  b.example.org ", "example.com", ""}},
			false,
			EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"example.com", "b.example.org"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateEmbedPrivacy(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				if _, ok := err.(*EmbedValidationError); !ok {
					t.Fatalf("err = %T, want *EmbedValidationError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != tc.want.Status || !equalStrings(got.AllowedDomains, tc.want.AllowedDomains) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestWhitelistCap proves the ≤50 domain cap.
func TestWhitelistCap(t *testing.T) {
	domains := make([]string, MaxEmbedAllowedDomains+1)
	for i := range domains {
		domains[i] = uniqueHost(i)
	}
	if _, err := validateEmbedPrivacy(EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: domains}); err == nil {
		t.Fatalf("51 domains = nil, want validation error")
	}
}

// TestSetEmbedPrivacyOwnerGate proves owner gating and that a non-whitelist status
// stores an empty domain list.
func TestSetEmbedPrivacyOwnerGate(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := seedVideo(t, svc, owner)
	ctx := context.Background()

	if _, err := svc.SetEmbedPrivacy(ctx, uuid.New(), id, EmbedPrivacy{Status: EmbedDisabled}); err != ErrForbidden {
		t.Fatalf("non-owner SetEmbedPrivacy = %v, want ErrForbidden", err)
	}
	got, err := svc.SetEmbedPrivacy(ctx, owner, id, EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: []string{"example.com"}})
	if err != nil {
		t.Fatalf("SetEmbedPrivacy whitelist: %v", err)
	}
	if got.Status != EmbedWhitelist || len(got.AllowedDomains) != 1 {
		t.Fatalf("got %+v", got)
	}
	// Read it back.
	rd, err := svc.GetEmbedPrivacy(ctx, id)
	if err != nil || rd.Status != EmbedWhitelist || len(rd.AllowedDomains) != 1 {
		t.Fatalf("GetEmbedPrivacy = %+v, %v", rd, err)
	}
	// Switching to enabled clears the domain list.
	back, err := svc.SetEmbedPrivacy(ctx, owner, id, EmbedPrivacy{Status: EmbedEnabled})
	if err != nil || back.Status != EmbedEnabled || len(back.AllowedDomains) != 0 {
		t.Fatalf("switch to enabled = %+v, %v", back, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func uniqueHost(i int) string {
	return "h" + itoaEmbed(i) + ".example.com"
}

func itoaEmbed(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
