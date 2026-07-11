package instancesettings

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory instance_settings store.
type fakeRepo struct {
	rows map[string]sqlcgen.InstanceSetting
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[string]sqlcgen.InstanceSetting{}} }

func (f *fakeRepo) ListInstanceSettings(_ context.Context) ([]sqlcgen.InstanceSetting, error) {
	out := make([]sqlcgen.InstanceSetting, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *fakeRepo) UpsertInstanceSetting(_ context.Context, arg sqlcgen.UpsertInstanceSettingParams) error {
	f.rows[arg.Key] = sqlcgen.InstanceSetting{Key: arg.Key, Value: arg.Value, UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now()}
	return nil
}

func (f *fakeRepo) DeleteInstanceSetting(_ context.Context, key string) (int64, error) {
	if _, ok := f.rows[key]; ok {
		delete(f.rows, key)
		return 1, nil
	}
	return 0, nil
}

func testDefaults() Defaults {
	return Defaults{
		InstanceName:                "Vidra Default",
		InstanceDescription:         "",
		TermsURL:                    "",
		PrivacyURL:                  "",
		ContactEmail:                "",
		RegistrationEnabled:         true,
		RegistrationRequireApproval: false,
		QuarantineNewUploads:        false,
		UploadsEnabled:              true,
		ImportsEnabled:              true,
		LiveEnabled:                 true,
		CommentsEnabled:             true,

		DefaultUserQuotaBytes:          1_000_000,
		UploadMaxSizeBytes:             1 << 21, // 2 MiB
		UploadMaxActiveSessionsPerUser: 5,
		ImportMaxHeight:                1080,
	}
}

// TestIntSettings covers the KindInt operational-limit settings: default
// resolution, override round-trip, reset, Snapshot shape, validation bounds, and
// the defensive fallback when a stored value is not a parseable integer.
func TestIntSettings(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	by := uuid.New()

	// Default resolution (no override) returns the config default.
	if got := svc.Int(KeyUploadMaxActiveSessionsPerUser); got != 5 {
		t.Fatalf("default sessions = %d, want 5", got)
	}
	if got := svc.Int(KeyDefaultUserQuotaBytes); got != 1_000_000 {
		t.Fatalf("default quota = %d, want 1000000", got)
	}

	// Override round-trip (incl. the 0 = unlimited/no-cap sentinel).
	if err := svc.Apply(ctx, map[string]Update{
		KeyUploadMaxActiveSessionsPerUser: {Value: "2"},
		KeyDefaultUserQuotaBytes:          {Value: "0"},
	}, by); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if got := svc.Int(KeyUploadMaxActiveSessionsPerUser); got != 2 {
		t.Fatalf("override sessions = %d, want 2", got)
	}
	if got := svc.Int(KeyDefaultUserQuotaBytes); got != 0 {
		t.Fatalf("override quota = %d, want 0", got)
	}

	// Reset via Delete falls back to the default.
	if err := svc.Apply(ctx, map[string]Update{KeyUploadMaxActiveSessionsPerUser: {Delete: true}}, by); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := svc.Int(KeyUploadMaxActiveSessionsPerUser); got != 5 {
		t.Fatalf("reset sessions = %d, want default 5", got)
	}

	// Snapshot emits Kind:"int" with int64 Value/Default (round-trips as a JSON number).
	var found bool
	for _, e := range svc.Snapshot() {
		if e.Key != KeyImportMaxHeight {
			continue
		}
		found = true
		if e.Kind != KindInt {
			t.Errorf("import_max_height kind = %q, want int", e.Kind)
		}
		if v, ok := e.Value.(int64); !ok || v != 1080 {
			t.Errorf("import_max_height value = %v (%T), want int64 1080", e.Value, e.Value)
		}
		if d, ok := e.Default.(int64); !ok || d != 1080 {
			t.Errorf("import_max_height default = %v (%T), want int64 1080", e.Default, e.Default)
		}
	}
	if !found {
		t.Fatal("import_max_height missing from snapshot")
	}

	// Validation bounds — Apply rejects each before writing anything.
	bad := map[string]map[string]Update{
		"negative quota":     {KeyDefaultUserQuotaBytes: {Value: "-1"}},
		"upload below floor": {KeyUploadMaxSizeBytes: {Value: "1024"}}, // non-zero, < 1 MiB
		"sessions over max":  {KeyUploadMaxActiveSessionsPerUser: {Value: "10001"}},
		"height below min":   {KeyImportMaxHeight: {Value: "143"}},
		"height above max":   {KeyImportMaxHeight: {Value: "4321"}},
		"non-integer":        {KeyUploadMaxSizeBytes: {Value: "big"}},
		"over max safe int":  {KeyDefaultUserQuotaBytes: {Value: "9007199254740992"}}, // 2^53
	}
	for name, updates := range bad {
		err := svc.Apply(ctx, updates, by)
		if err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: error = %v, want *ValidationError", name, err)
		}
	}
	// Zero is accepted for the sentinel-bearing limits.
	if err := svc.Apply(ctx, map[string]Update{
		KeyUploadMaxSizeBytes: {Value: "0"}, KeyImportMaxHeight: {Value: "0"},
	}, by); err != nil {
		t.Fatalf("zero sentinel rejected: %v", err)
	}

	// Defensive fallback: a stored value that is not a valid integer (only
	// reachable via a manual DB edit — Apply validates every write) resolves to
	// the default rather than 0.
	repo.rows[KeyDefaultUserQuotaBytes] = sqlcgen.InstanceSetting{Key: KeyDefaultUserQuotaBytes, Value: "not-a-number"}
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := svc.Int(KeyDefaultUserQuotaBytes); got != 1_000_000 {
		t.Fatalf("unparseable override quota = %d, want default 1000000", got)
	}
}

// TestOverlayResolution covers the three states per key: config default (no
// override), DB override, and reset-to-default (override cleared).
func TestOverlayResolution(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Default state: config values, nothing overridden.
	if got := svc.String(KeyInstanceName); got != "Vidra Default" {
		t.Errorf("default instance_name = %q, want Vidra Default", got)
	}
	if !svc.Bool(KeyUploadsEnabled) {
		t.Error("default uploads_enabled = false, want true")
	}
	if !svc.Bool(KeyDownloadsEnabled) {
		t.Error("default downloads_enabled = false, want true")
	}
	for _, e := range svc.Snapshot() {
		if e.Overridden {
			t.Errorf("key %q unexpectedly overridden before any write", e.Key)
		}
	}

	// Override a string and a bool.
	admin := uuid.New()
	if err := svc.Apply(ctx, map[string]Update{
		KeyInstanceName:   {Value: "Custom Name"},
		KeyUploadsEnabled: {Value: "false"},
	}, admin); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if got := svc.String(KeyInstanceName); got != "Custom Name" {
		t.Errorf("override instance_name = %q, want Custom Name", got)
	}
	if svc.Bool(KeyUploadsEnabled) {
		t.Error("override uploads_enabled = true, want false")
	}
	overridden := overriddenKeys(svc)
	if !overridden[KeyInstanceName] || !overridden[KeyUploadsEnabled] {
		t.Errorf("expected instance_name + uploads_enabled overridden, got %v", overridden)
	}
	// The snapshot reports the config default alongside the effective value.
	for _, e := range svc.Snapshot() {
		if e.Key == KeyUploadsEnabled {
			if e.Value != false || e.Default != true || !e.Overridden {
				t.Errorf("uploads_enabled snapshot = %+v, want value=false default=true overridden=true", e)
			}
		}
	}

	// Clear the instance_name override → back to the config default.
	if err := svc.Apply(ctx, map[string]Update{KeyInstanceName: {Delete: true}}, admin); err != nil {
		t.Fatalf("apply delete: %v", err)
	}
	if got := svc.String(KeyInstanceName); got != "Vidra Default" {
		t.Errorf("after clear instance_name = %q, want Vidra Default", got)
	}
	if overriddenKeys(svc)[KeyInstanceName] {
		t.Error("instance_name still overridden after clear")
	}
	// uploads_enabled override persists independently.
	if svc.Bool(KeyUploadsEnabled) {
		t.Error("uploads_enabled override lost after clearing a different key")
	}

	// downloads_enabled is a hardcoded-default runtime toggle (no config/env
	// backing): false overrides it and clearing the row restores true.
	if err := svc.Apply(ctx, map[string]Update{KeyDownloadsEnabled: {Value: "false"}}, admin); err != nil {
		t.Fatalf("apply downloads override: %v", err)
	}
	if svc.Bool(KeyDownloadsEnabled) {
		t.Error("downloads_enabled override = true, want false")
	}
	if err := svc.Apply(ctx, map[string]Update{KeyDownloadsEnabled: {Delete: true}}, admin); err != nil {
		t.Fatalf("clear downloads override: %v", err)
	}
	if !svc.Bool(KeyDownloadsEnabled) {
		t.Error("downloads_enabled after clear = false, want hardcoded default true")
	}
}

// TestReloadPicksUpExternalRows proves Load rebuilds the cache from the store
// (an override written out of band appears after Load).
func TestReloadPicksUpExternalRows(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !svc.Bool(KeyCommentsEnabled) {
		t.Fatal("comments_enabled should default true")
	}
	// Write a row directly into the store, bypassing the service. Until Load, the
	// cache still returns the default (the external row is not yet seen).
	_ = repo.UpsertInstanceSetting(ctx, sqlcgen.UpsertInstanceSettingParams{Key: KeyCommentsEnabled, Value: "false"})
	if !svc.Bool(KeyCommentsEnabled) {
		t.Error("external row leaked into the cache before reload")
	}
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if svc.Bool(KeyCommentsEnabled) {
		t.Error("comments_enabled still true after reload of an external false override")
	}
	// Unknown/legacy rows are ignored on load.
	_ = repo.UpsertInstanceSetting(ctx, sqlcgen.UpsertInstanceSettingParams{Key: "legacy_unknown_key", Value: "x"})
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("reload with legacy row: %v", err)
	}
}

func TestApplyValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := []struct {
		name    string
		updates map[string]Update
		wantKey string
	}{
		{"unknown key", map[string]Update{"nope": {Value: "x"}}, "nope"},
		{"non-bool value", map[string]Update{KeyUploadsEnabled: {Value: "maybe"}}, KeyUploadsEnabled},
		{"empty instance_name", map[string]Update{KeyInstanceName: {Value: "  "}}, KeyInstanceName},
		{"bad url", map[string]Update{KeyTermsURL: {Value: "not-a-url"}}, KeyTermsURL},
		{"bad email", map[string]Update{KeyContactEmail: {Value: "nope"}}, KeyContactEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Apply(ctx, tc.updates, uuid.New())
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Key != tc.wantKey {
				t.Errorf("ValidationError.Key = %q, want %q", ve.Key, tc.wantKey)
			}
		})
	}

	// A batch with one invalid key writes NOTHING (all-or-nothing validation).
	err := svc.Apply(ctx, map[string]Update{
		KeyInstanceName:   {Value: "Fine"},
		KeyUploadsEnabled: {Value: "banana"},
	}, uuid.New())
	if err == nil {
		t.Fatal("mixed batch should fail validation")
	}
	if got := svc.String(KeyInstanceName); got != "Vidra Default" {
		t.Errorf("instance_name persisted despite failed batch: %q", got)
	}

	// Valid edge cases: empty URL/email clears the link/contact; a valid email/URL is accepted.
	if err := svc.Apply(ctx, map[string]Update{
		KeyTermsURL:     {Value: "https://example.test/terms"},
		KeyContactEmail: {Value: ""},
		KeyPrivacyURL:   {Value: ""},
	}, uuid.New()); err != nil {
		t.Fatalf("valid url + empty clears: %v", err)
	}
	if got := svc.String(KeyTermsURL); got != "https://example.test/terms" {
		t.Errorf("terms_url = %q", got)
	}
}

func TestPlatformInfoEnumAndListSettings(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	if got := svc.String(KeyDefaultLanguage); got != DefaultDefaultLanguage {
		t.Errorf("default_language default = %q, want %q", got, DefaultDefaultLanguage)
	}
	if got := svc.String(KeySensitiveContentPolicy); got != DefaultSensitiveContentPolicy {
		t.Errorf("sensitive_content_policy default = %q, want %q", got, DefaultSensitiveContentPolicy)
	}
	if got := svc.Strings(KeyInstanceCategories); len(got) != 0 {
		t.Errorf("instance_categories default = %#v, want []", got)
	}

	admin := uuid.New()
	if err := svc.Apply(ctx, map[string]Update{
		KeySensitiveContentPolicy: {Value: SensitiveContentPolicyBlur},
		KeyDefaultLanguage:        {Value: "fr"},
		KeyInstanceCategories:     {Value: FormatList([]string{"1", "7"})},
		KeyModeratorLanguages:     {Value: FormatList([]string{"en", "es"})},
	}, admin); err != nil {
		t.Fatalf("apply platform info settings: %v", err)
	}
	if got := svc.String(KeySensitiveContentPolicy); got != SensitiveContentPolicyBlur {
		t.Errorf("sensitive_content_policy = %q, want blur", got)
	}
	if got := svc.String(KeyDefaultLanguage); got != "fr" {
		t.Errorf("default_language = %q, want fr", got)
	}
	if got := svc.Strings(KeyInstanceCategories); !reflect.DeepEqual(got, []string{"1", "7"}) {
		t.Errorf("instance_categories = %#v, want [1 7]", got)
	}
	if got := svc.Strings(KeyModeratorLanguages); !reflect.DeepEqual(got, []string{"en", "es"}) {
		t.Errorf("moderator_languages = %#v, want [en es]", got)
	}

	policy := snapshotByKey(t, svc, KeySensitiveContentPolicy)
	if policy.Kind != KindEnum || !reflect.DeepEqual(policy.Options, SensitiveContentPolicyOptions) {
		t.Errorf("policy snapshot = %+v, want enum with options %v", policy, SensitiveContentPolicyOptions)
	}
	cats := snapshotByKey(t, svc, KeyInstanceCategories)
	if cats.Kind != KindList || !reflect.DeepEqual(cats.Value, []string{"1", "7"}) || !reflect.DeepEqual(cats.Default, []string{}) {
		t.Errorf("categories snapshot = %+v, want list value/default", cats)
	}

	badCases := []struct {
		name    string
		updates map[string]Update
		wantKey string
	}{
		{"bad enum", map[string]Update{KeySensitiveContentPolicy: {Value: "surprise"}}, KeySensitiveContentPolicy},
		{"bad language", map[string]Update{KeyDefaultLanguage: {Value: "zz-nope"}}, KeyDefaultLanguage},
		{"bad category list json", map[string]Update{KeyInstanceCategories: {Value: `"1"`}}, KeyInstanceCategories},
		{"bad category id", map[string]Update{KeyInstanceCategories: {Value: FormatList([]string{"999"})}}, KeyInstanceCategories},
		{"bad moderator language", map[string]Update{KeyModeratorLanguages: {Value: FormatList([]string{"zz-nope"})}}, KeyModeratorLanguages},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.Apply(ctx, tc.updates, uuid.New())
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Key != tc.wantKey {
				t.Errorf("ValidationError.Key = %q, want %q", ve.Key, tc.wantKey)
			}
		})
	}
}

func TestApplyEmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	_ = svc.Load(ctx)
	if err := svc.Apply(ctx, nil, uuid.New()); err != nil {
		t.Errorf("empty apply = %v, want nil", err)
	}
}

func overriddenKeys(svc *Service) map[string]bool {
	m := map[string]bool{}
	for _, e := range svc.Snapshot() {
		m[e.Key] = e.Overridden
	}
	return m
}

func snapshotByKey(t *testing.T, svc *Service, key string) Effective {
	t.Helper()
	for _, e := range svc.Snapshot() {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("snapshot missing key %q", key)
	return Effective{}
}
