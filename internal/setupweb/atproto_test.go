package setupweb

import (
	"encoding/json"
	"testing"

	"github.com/vidra/vidra-core/internal/setup"
)

func TestWebPDSSelection(t *testing.T) {
	template, err := setup.ParseEnvFile([]byte("ATPROTO_ENABLED=false\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		want *setup.ATProtoAnswers
	}{
		{`{}`, nil},
		{`{"atproto":{"enabled":true,"provider":"local"}}`, &setup.ATProtoAnswers{Enabled: true, Provider: "local"}},
		{`{"atproto":{"enabled":true,"provider":"https://pds.example.org"}}`, &setup.ATProtoAnswers{Enabled: true, Provider: "https://pds.example.org"}},
		{`{"atproto":{"enabled":false}}`, &setup.ATProtoAnswers{}},
	} {
		var f Form
		if err := json.Unmarshal([]byte(tc.body), &f); err != nil {
			t.Fatal(err)
		}
		got := BuildAnswers(f, template, nil).ATProto
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Fatalf("unexpected setup selection for %s", tc.body)
		}
	}
}
