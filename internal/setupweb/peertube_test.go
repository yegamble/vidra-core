package setupweb

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The wizard's half of the PeerTube-source block.
//
// Two properties matter here and neither is about the form's layout: every
// answer is validated by THE ENGINE (internal/config's own rule, reached through
// internal/setup), and neither of the block's two secrets ever travels outwards
// — not in a seed, not in a review, and not in a validation message, which is
// the one place a naive implementation would put a DSN.

const ptSourceDSN = "postgres://readonly:SENTINEL-PT-deadbeef@10.0.0.5:5432/peertube_prod?sslmode=require"

func peerTubeForm() *PeerTubeForm {
	return &PeerTubeForm{
		Enabled:     true,
		SourceURL:   ptSourceDSN,
		Storage:     "s3",
		S3:          S3Form{Endpoint: "s3.example.net", Region: "eu-central-003", Bucket: "peertube-source-media", AccessKey: "003SOURCEACCESSKEY", SecretKey: "SENTINEL-PTS3-deadbeef"},
		MediaMode:   "copy",
		ConflictPol: "rename",
	}
}

func TestValidateRunsThePeerTubeAnswersThroughTheEngine(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	for _, tc := range []struct {
		field, value string
		ok           bool
	}{
		{"peertube_source_url", ptSourceDSN, true},
		{"peertube_source_url", "peertube-db.internal", false},
		{"peertube_source_storage", "s3", true},
		{"peertube_source_storage", "gcs", false},
		{"peertube_source_s3_endpoint", "s3.example.net", true},
		{"peertube_source_s3_endpoint", "https://s3.example.net", false},
		{"peertube_media_mode", "reference", true},
		{"peertube_media_mode", "move", false},
		{"peertube_conflict_policy", "merge", true},
		{"peertube_conflict_policy", "overwrite", false},
	} {
		var out ValidateResponse
		if code := w.callJSON(t, "POST", "/api/validate", ValidateRequest{Field: tc.field, Value: tc.value}, &out); code != http.StatusOK {
			t.Fatalf("%s=%q: status %d", tc.field, tc.value, code)
		}
		if out.OK != tc.ok {
			t.Errorf("%s=%q: ok = %v, want %v (error %q)", tc.field, tc.value, out.OK, tc.ok, out.Error)
		}
		if !tc.ok && strings.HasPrefix(out.Error, "config: ") {
			t.Errorf("%s: the package prefix was not stripped: %q", tc.field, out.Error)
		}
	}
}

// The validation message is the one place a DSN would leak outwards, because it
// is the only response computed FROM the value the operator typed. It is also
// the one an operator sees in a browser, over plain http, on a shared screen.
func TestValidateNeverEchoesTheSourceDSN(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", nil)
	code, raw := w.call(t, "POST", "/api/validate", ValidateRequest{
		Field: "peertube_source_url",
		Value: "not-a-dsn-SENTINEL-PT-deadbeef",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if bytes.Contains(raw, []byte("SENTINEL-PT-deadbeef")) {
		t.Errorf("the validation response carries the DSN: %s", raw)
	}
}

// The seed reports both secrets as PRESENCE and echoes the four identifying
// fields, which is the split the terminal interview makes: an access key
// identifies a credential, a secret key IS one, and a connection string carries
// one inside it.
func TestStateSeedsThePeerTubeBlockWithoutItsSecrets(t *testing.T) {
	t.Parallel()
	w := newWizard(t, peerTubeReRunEnv, nil)
	st := w.state(t)
	pt := st.Seed.PeerTube
	if !pt.Enabled {
		t.Error("the gate was not seeded from PEERTUBE_IMPORT_ENABLED")
	}
	if !pt.SourceURLSet || !pt.S3SecretKeySet {
		t.Errorf("a secret on file is not reported as present: %+v", pt)
	}
	if pt.Storage != "s3" || pt.S3Bucket != "peertube-source-media" || pt.S3AccessKey != "003SOURCEACCESSKEY" {
		t.Errorf("the identifying fields were not seeded: %+v", pt)
	}
	// The enumerated answers fall back to the CODE's defaults, so a dropdown never
	// opens on an empty option nobody chose.
	w2 := newWizard(t, "", nil)
	pt2 := w2.state(t).Seed.PeerTube
	if pt2.Storage != "local" || pt2.MediaMode != "copy" || pt2.ConflictPol != "skip" {
		t.Errorf("the defaults are not the engine's: %+v", pt2)
	}

	_, raw := w.call(t, "GET", "/api/state", nil)
	for _, secret := range []string{"SENTINEL-PT-deadbeef", "SENTINEL-PTS3-deadbeef"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("GET /api/state echoed a source secret: %s", raw)
		}
	}
}

// Apply writes the source, and the response that reports it names variables and
// never values.
func TestApplyWritesThePeerTubeSource(t *testing.T) {
	t.Parallel()
	w := newWizard(t, "", withProxy)
	form := validForm()
	form.PeerTube = peerTubeForm()
	code, out := w.apply(t, ApplyRequest{Form: form})
	if code != http.StatusOK {
		t.Fatalf("apply = %d (%+v)", code, out.Issues)
	}
	b, err := os.ReadFile(w.out)
	if err != nil {
		t.Fatalf("read written env: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"PEERTUBE_IMPORT_ENABLED=true",
		"PEERTUBE_SOURCE_DATABASE_URL=" + ptSourceDSN,
		"PEERTUBE_SOURCE_STORAGE_BACKEND=s3",
		"PEERTUBE_SOURCE_S3_BUCKET=peertube-source-media",
		"PEERTUBE_IMPORT_CONFLICT_POLICY=rename",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the written env file is missing %q:\n%s", want, got)
		}
	}
	_, raw := w.call(t, "POST", "/api/review", form)
	for _, secret := range []string{"SENTINEL-PT-deadbeef", "SENTINEL-PTS3-deadbeef"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("the review response echoed a source secret: %s", raw)
		}
	}
}

// The Done step's migration handoff, and the deployments that do NOT get it.
// `vidra setup --web` cannot run an import — the importer lives in the api
// container, which does not exist while the wizard is open — so an address is
// the whole deliverable.
func TestStatusCarriesThePeerTubeImportHandoffOnlyWhenThereIsASource(t *testing.T) {
	t.Parallel()
	w := newWizard(t, peerTubeReRunEnv, func(o *Options) {
		o.Status = func(context.Context) []StatusLine { return nil }
	})
	var out StatusResponse
	if code := w.callJSON(t, "POST", "/api/status", nil, &out); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out.ImportURL != "https://video.example.org/admin/import-peertube" {
		t.Errorf("import_url = %q", out.ImportURL)
	}

	// The ordinary install is told nothing about a screen it has no use for.
	plain := newWizard(t, reRunEnv, nil)
	var out2 StatusResponse
	if code := plain.callJSON(t, "POST", "/api/status", nil, &out2); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out2.ImportURL != "" {
		t.Errorf("an install with no PeerTube source was handed %q", out2.ImportURL)
	}
}

// peerTubeReRunEnv is reRunEnv plus a configured migration source, with both of
// its secrets planted so a leak in any response is a failing test rather than a
// review comment.
const peerTubeReRunEnv = reRunEnv + `PEERTUBE_IMPORT_ENABLED=true
PEERTUBE_SOURCE_DATABASE_URL=` + ptSourceDSN + `
PEERTUBE_SOURCE_STORAGE_BACKEND=s3
PEERTUBE_SOURCE_S3_ENDPOINT=s3.example.net
PEERTUBE_SOURCE_S3_BUCKET=peertube-source-media
PEERTUBE_SOURCE_S3_ACCESS_KEY=003SOURCEACCESSKEY
PEERTUBE_SOURCE_S3_SECRET_KEY=SENTINEL-PTS3-deadbeef
PEERTUBE_IMPORT_MEDIA_MODE=copy
PEERTUBE_IMPORT_CONFLICT_POLICY=skip
`
