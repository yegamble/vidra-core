package instancesettings

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// terms_version / privacy_version are the labels an operator gives the
// documents behind terms_url / privacy_url; a signup records the label it
// accepted. They are short text, empty by default, and must stay printable
// because they are stored on every consent row and shown back to people.
func TestDocumentVersionSettings(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, key := range []string{KeyTermsVersion, KeyPrivacyVersion} {
		if got := svc.String(key); got != "" {
			t.Errorf("%s default = %q, want empty", key, got)
		}
		if err := svc.Apply(ctx, map[string]Update{key: {Value: "2026-11-12"}}, uuid.Nil); err != nil {
			t.Fatalf("apply %s: %v", key, err)
		}
		if got := svc.String(key); got != "2026-11-12" {
			t.Errorf("%s = %q after apply", key, got)
		}
		for name, bad := range map[string]string{
			"too long":     strings.Repeat("v", 65),
			"control char": "v1\nv2",
		} {
			if err := svc.Apply(ctx, map[string]Update{key: {Value: bad}}, uuid.Nil); err == nil {
				t.Errorf("%s accepted a %s value", key, name)
			}
		}
		if err := svc.Apply(ctx, map[string]Update{key: {Value: ""}}, uuid.Nil); err != nil {
			t.Errorf("%s could not be cleared: %v", key, err)
		}
	}
}
