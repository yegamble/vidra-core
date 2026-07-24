package instancesettings

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
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

		ChannelSyncEnabled:    true,
		ChannelSyncMaxPerUser: 5,
		TranscriptionEnabled:  false,

		TranscodingEnabled: true,
	}
}

// TestW8ToggleBatchRegistry covers the shipped-feature toggle batch
// (config-parity W8): every new key's kind, default resolution, admin-IA
// placement, and validator boundaries.
func TestW8ToggleBatchRegistry(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	snap := map[string]Effective{}
	for _, e := range svc.Snapshot() {
		snap[e.Key] = e
	}

	cases := []struct {
		key     string
		kind    Kind
		def     any // bool or int64, matching kind
		page    string
		section string
	}{
		{KeyImportHTTPEnabled, KindBool, true, PageVOD, "imports"},
		{KeyChannelSyncEnabled, KindBool, true, PageVOD, "imports"}, // testDefaults: ChannelSyncEnabled=true
		{KeyChannelSyncMaxPerUser, KindInt, int64(5), PageVOD, "imports"},
		{KeyStoryboardsEnabled, KindBool, true, PageVOD, "storyboards"},
		{KeyVideoCardPreviewsEnabled, KindBool, false, PageVOD, "playback"},
		{KeyVideoCardPreviewsDefaultEnabled, KindBool, false, PageVOD, "playback"},
		{KeyTranscriptionEnabled, KindBool, false, PageVOD, "transcription"}, // TranscriptionEnabled=false
		{KeyUserImportEnabled, KindBool, true, PageAdvanced, "user_data"},
		{KeyUserExportEnabled, KindBool, true, PageAdvanced, "user_data"},
		{KeyUserExportExpirationHours, KindInt, int64(DefaultUserExportExpirationHours), PageAdvanced, "user_data"},
		{KeyUserExportMaxQuotaBytes, KindInt, int64(0), PageAdvanced, "user_data"},
		{KeyMaxChannelsPerUser, KindInt, int64(0), PageGeneral, "channels"},
	}
	for _, tc := range cases {
		e, ok := snap[tc.key]
		if !ok {
			t.Errorf("%s missing from snapshot", tc.key)
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("%s kind = %q, want %q", tc.key, e.Kind, tc.kind)
		}
		if !reflect.DeepEqual(e.Default, tc.def) || !reflect.DeepEqual(e.Value, tc.def) {
			t.Errorf("%s default/value = %v/%v, want %v", tc.key, e.Default, e.Value, tc.def)
		}
		if e.Overridden {
			t.Errorf("%s overridden = true, want false", tc.key)
		}
		if e.Page != tc.page || e.Section != tc.section {
			t.Errorf("%s placement = %s/%s, want %s/%s", tc.key, e.Page, e.Section, tc.page, tc.section)
		}
	}

	// Validator boundaries: each rejected before anything is written.
	by := uuid.New()
	bad := map[string]map[string]Update{
		"bool not boolean":                   {KeyImportHTTPEnabled: {Value: "yes"}},
		"sync cap negative":                  {KeyChannelSyncMaxPerUser: {Value: "-1"}},
		"sync cap over max":                  {KeyChannelSyncMaxPerUser: {Value: "10001"}},
		"export ttl negative":                {KeyUserExportExpirationHours: {Value: "-1"}},
		"export ttl over a year":             {KeyUserExportExpirationHours: {Value: "8761"}},
		"export quota negative":              {KeyUserExportMaxQuotaBytes: {Value: "-5"}},
		"channel cap negative":               {KeyMaxChannelsPerUser: {Value: "-1"}},
		"channel cap over max":               {KeyMaxChannelsPerUser: {Value: "10001"}},
		"transcription not a boolean":        {KeyTranscriptionEnabled: {Value: "1"}},
		"card previews not a boolean":        {KeyVideoCardPreviewsEnabled: {Value: "enabled"}},
		"card preview default not a boolean": {KeyVideoCardPreviewsDefaultEnabled: {Value: "enabled"}},
	}
	for name, updates := range bad {
		var verr *ValidationError
		if err := svc.Apply(ctx, updates, by); !errors.As(err, &verr) {
			t.Errorf("%s: Apply err = %v, want ValidationError", name, err)
		}
	}

	// Boundary accepts: 0 sentinels and in-range values round-trip.
	if err := svc.Apply(ctx, map[string]Update{
		KeyChannelSyncMaxPerUser:           {Value: "0"}, // unlimited
		KeyUserExportExpirationHours:       {Value: "0"}, // never expires
		KeyUserExportMaxQuotaBytes:         {Value: "0"}, // no cap
		KeyMaxChannelsPerUser:              {Value: "3"},
		KeyImportHTTPEnabled:               {Value: "false"},
		KeyStoryboardsEnabled:              {Value: "false"},
		KeyVideoCardPreviewsEnabled:        {Value: "true"},
		KeyVideoCardPreviewsDefaultEnabled: {Value: "true"},
	}, by); err != nil {
		t.Fatalf("apply boundary values: %v", err)
	}
	if got := svc.Int(KeyChannelSyncMaxPerUser); got != 0 {
		t.Errorf("sync cap override = %d, want 0", got)
	}
	if got := svc.Int(KeyMaxChannelsPerUser); got != 3 {
		t.Errorf("channel cap override = %d, want 3", got)
	}
	if svc.Bool(KeyImportHTTPEnabled) || svc.Bool(KeyStoryboardsEnabled) {
		t.Error("bool overrides did not apply")
	}
	if !svc.Bool(KeyVideoCardPreviewsEnabled) || !svc.Bool(KeyVideoCardPreviewsDefaultEnabled) {
		t.Error("video-card preview gate/default overrides did not apply")
	}

	// null-PATCH (Delete) clears back to the defaults.
	if err := svc.Apply(ctx, map[string]Update{
		KeyImportHTTPEnabled:               {Delete: true},
		KeyStoryboardsEnabled:              {Delete: true},
		KeyVideoCardPreviewsEnabled:        {Delete: true},
		KeyVideoCardPreviewsDefaultEnabled: {Delete: true},
	}, by); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !svc.Bool(KeyImportHTTPEnabled) || !svc.Bool(KeyStoryboardsEnabled) {
		t.Error("delete did not restore defaults")
	}
	if svc.Bool(KeyVideoCardPreviewsEnabled) || svc.Bool(KeyVideoCardPreviewsDefaultEnabled) {
		t.Error("delete did not restore video-card preview gate/default off")
	}
}

// TestFeaturedBannerRegistry covers the admin featured-banner keys
// (home-featured-banner): kind, hardcoded default, admin-IA placement,
// validator boundaries (empty-or-UUID video id, length caps, enum label), the
// override round-trip, and reset-to-default.
func TestFeaturedBannerRegistry(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	snap := map[string]Effective{}
	for _, e := range svc.Snapshot() {
		snap[e.Key] = e
	}

	cases := []struct {
		key     string
		kind    Kind
		def     any // bool or string, matching kind
		page    string
		section string
	}{
		{KeyFeaturedEnabled, KindBool, false, PageHomepage, "featured"},
		{KeyFeaturedVideoID, KindString, "", PageHomepage, "featured"},
		{KeyFeaturedTitle, KindString, "", PageHomepage, "featured"},
		{KeyFeaturedDescription, KindString, "", PageHomepage, "featured"},
		{KeyFeaturedCTALabel, KindString, "", PageHomepage, "featured"},
		{KeyFeaturedLabel, KindEnum, DefaultFeaturedLabel, PageHomepage, "featured"},
	}
	for _, tc := range cases {
		e, ok := snap[tc.key]
		if !ok {
			t.Errorf("%s missing from snapshot", tc.key)
			continue
		}
		if e.Kind != tc.kind {
			t.Errorf("%s kind = %q, want %q", tc.key, e.Kind, tc.kind)
		}
		if !reflect.DeepEqual(e.Default, tc.def) || !reflect.DeepEqual(e.Value, tc.def) {
			t.Errorf("%s default/value = %v/%v, want %v", tc.key, e.Default, e.Value, tc.def)
		}
		if e.Overridden {
			t.Errorf("%s overridden = true, want false", tc.key)
		}
		if e.Page != tc.page || e.Section != tc.section {
			t.Errorf("%s placement = %s/%s, want %s/%s", tc.key, e.Page, e.Section, tc.page, tc.section)
		}
	}
	// The label enum exposes its options for the admin UI + OpenAPI.
	if got := snap[KeyFeaturedLabel].Options; !reflect.DeepEqual(got, FeaturedLabelOptions) {
		t.Errorf("featured_label options = %v, want %v", got, FeaturedLabelOptions)
	}

	// Validator boundaries: each rejected before anything is written.
	by := uuid.New()
	longTitle := strings.Repeat("x", 121)
	longDesc := strings.Repeat("x", 501)
	longCTA := strings.Repeat("x", 41)
	bad := map[string]map[string]Update{
		"enabled not boolean":  {KeyFeaturedEnabled: {Value: "yes"}},
		"video id not a uuid":  {KeyFeaturedVideoID: {Value: "not-a-uuid"}},
		"title too long":       {KeyFeaturedTitle: {Value: longTitle}},
		"description too long": {KeyFeaturedDescription: {Value: longDesc}},
		"cta too long":         {KeyFeaturedCTALabel: {Value: longCTA}},
		"label not an option":  {KeyFeaturedLabel: {Value: "promoted"}},
	}
	for name, updates := range bad {
		var verr *ValidationError
		if err := svc.Apply(ctx, updates, by); !errors.As(err, &verr) {
			t.Errorf("%s: Apply err = %v, want ValidationError", name, err)
		}
	}

	// Accepts: an empty video id (no pick), a well-formed UUID, at-cap strings,
	// and the sponsored label all round-trip.
	vid := uuid.New().String()
	if err := svc.Apply(ctx, map[string]Update{
		KeyFeaturedEnabled:     {Value: "true"},
		KeyFeaturedVideoID:     {Value: vid},
		KeyFeaturedTitle:       {Value: strings.Repeat("x", 120)},
		KeyFeaturedDescription: {Value: strings.Repeat("x", 500)},
		KeyFeaturedCTALabel:    {Value: strings.Repeat("x", 40)},
		KeyFeaturedLabel:       {Value: "sponsored"},
	}, by); err != nil {
		t.Fatalf("apply valid featured settings: %v", err)
	}
	if !svc.Bool(KeyFeaturedEnabled) {
		t.Error("featured_enabled override did not apply")
	}
	if got := svc.String(KeyFeaturedVideoID); got != vid {
		t.Errorf("featured_video_id = %q, want %q", got, vid)
	}
	if got := svc.String(KeyFeaturedLabel); got != "sponsored" {
		t.Errorf("featured_label = %q, want sponsored", got)
	}

	// Empty video id clears the pick (valid).
	if err := svc.Apply(ctx, map[string]Update{KeyFeaturedVideoID: {Value: ""}}, by); err != nil {
		t.Fatalf("clear video id: %v", err)
	}
	if got := svc.String(KeyFeaturedVideoID); got != "" {
		t.Errorf("featured_video_id after clear = %q, want empty", got)
	}

	// null-PATCH (Delete) restores the hardcoded defaults.
	if err := svc.Apply(ctx, map[string]Update{
		KeyFeaturedEnabled: {Delete: true},
		KeyFeaturedLabel:   {Delete: true},
	}, by); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if svc.Bool(KeyFeaturedEnabled) {
		t.Error("delete did not restore featured_enabled=false")
	}
	if got := svc.String(KeyFeaturedLabel); got != DefaultFeaturedLabel {
		t.Errorf("featured_label after delete = %q, want %q", got, DefaultFeaturedLabel)
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

// TestW9SearchServiceEnabledRegistry covers the smart-search routing toggle
// (search-service W9): its kind/default/placement, its validator, the
// null-PATCH-clears-override behaviour, and — critically — that it is NOT a
// config-pushed search key (its change must never emit search.config_updated).
func TestW9SearchServiceEnabledRegistry(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Kind + default (true: use the service whenever it is wired).
	if k, ok := KindOf(KeySearchServiceEnabled); !ok || k != KindBool {
		t.Errorf("KindOf(%s) = %v,%v; want bool,true", KeySearchServiceEnabled, k, ok)
	}
	if !svc.Bool(KeySearchServiceEnabled) {
		t.Error("search_service_enabled default = false, want true")
	}

	// Placement: advanced page, same 'search' section as the W4 keys.
	if e := snapshotByKey(t, svc, KeySearchServiceEnabled); e.Kind != KindBool || e.Page != PageAdvanced || e.Section != "search" {
		t.Errorf("%s = kind %s at %s/%s, want bool at advanced/search", KeySearchServiceEnabled, e.Kind, e.Page, e.Section)
	}

	// Routing-policy toggle, NOT search-service config: a change must not be
	// pushed on search.config_updated.
	if IsSearchSettingKey(KeySearchServiceEnabled) {
		t.Error("search_service_enabled must NOT be a config-pushed search key (contract stays stable)")
	}

	admin := uuid.New()
	// Validator: accepts booleans, rejects anything else.
	if err := svc.Apply(ctx, map[string]Update{KeySearchServiceEnabled: {Value: "false"}}, admin); err != nil {
		t.Errorf("Apply(false) = %v, want ok", err)
	}
	if svc.Bool(KeySearchServiceEnabled) {
		t.Error("after Apply(false), value = true")
	}
	err := svc.Apply(ctx, map[string]Update{KeySearchServiceEnabled: {Value: "maybe"}}, admin)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Key != KeySearchServiceEnabled {
		t.Errorf("Apply(maybe) = %v, want ValidationError on the key", err)
	}

	// null-PATCH clears the override back to the true default.
	if err := svc.Apply(ctx, map[string]Update{KeySearchServiceEnabled: {Delete: true}}, admin); err != nil {
		t.Fatalf("Apply(delete): %v", err)
	}
	if !svc.Bool(KeySearchServiceEnabled) {
		t.Error("after delete, search_service_enabled = false, want the true default")
	}
}

// TestW10TranscodingKnobsRegistry covers the VOD transcoding runtime knobs &
// worker pools batch (config-parity W10): every key's kind, default
// resolution (transcoding_enabled from config, the ladder from the shipped
// media default, everything else hardcoded), admin-IA placement, and
// validator boundaries.
func TestW10TranscodingKnobsRegistry(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Defaults + kinds + placement.
	boolKeys := []struct {
		key  string
		def  bool
		page string
		sect string
	}{
		{KeyTranscodingEnabled, true, PageVOD, "transcoding"}, // testDefaults: TranscodingEnabled=true
		{KeyTranscodingOriginalResolution, false, PageVOD, "transcoding"},
		{KeyUploadAdditionalExtensionsEnabled, true, PageVOD, "uploads"}, // deviation: default ON (shipped allow-list already extended)
	}
	for _, tc := range boolKeys {
		if got := svc.Bool(tc.key); got != tc.def {
			t.Errorf("%s default = %v, want %v", tc.key, got, tc.def)
		}
		e := snapshotByKey(t, svc, tc.key)
		if e.Kind != KindBool || e.Page != tc.page || e.Section != tc.sect {
			t.Errorf("%s = kind %s at %s/%s, want bool at %s/%s", tc.key, e.Kind, e.Page, e.Section, tc.page, tc.sect)
		}
	}
	intKeys := []struct {
		key  string
		def  int64
		sect string
	}{
		{KeyTranscodingMaxFPS, 0, "transcoding"},
		{KeyTranscodingThreads, 0, "transcoding"},
		{KeyTranscodingConcurrency, 1, "transcoding"},
		{KeyImportJobsConcurrency, 1, "imports"},
	}
	for _, tc := range intKeys {
		if got := svc.Int(tc.key); got != tc.def {
			t.Errorf("%s default = %d, want %d", tc.key, got, tc.def)
		}
		e := snapshotByKey(t, svc, tc.key)
		if e.Kind != KindInt || e.Page != PageVOD || e.Section != tc.sect {
			t.Errorf("%s = kind %s at %s/%s, want int at vod/%s", tc.key, e.Kind, e.Page, e.Section, tc.sect)
		}
	}

	// The ladder default mirrors the shipped hardcoded ladder exactly.
	if got := svc.Strings(KeyTranscodingResolutions); FormatList(got) != `["1080","720","480","360"]` {
		t.Errorf("transcoding_resolutions default = %v, want the shipped 1080/720/480/360 ladder", got)
	}
	if e := snapshotByKey(t, svc, KeyTranscodingResolutions); e.Kind != KindList || e.Page != PageVOD || e.Section != "transcoding" {
		t.Errorf("transcoding_resolutions = kind %s at %s/%s, want list at vod/transcoding", e.Kind, e.Page, e.Section)
	}

	// The transcoding_enabled default follows config (Defaults.TranscodingEnabled).
	off := NewService(newFakeRepo(), Defaults{TranscodingEnabled: false})
	_ = off.Load(ctx)
	if off.Bool(KeyTranscodingEnabled) {
		t.Error("transcoding_enabled must default to the config value (false)")
	}

	// Validator boundaries (table-driven).
	admin := uuid.New()
	valid := map[string]string{
		KeyTranscodingEnabled:                "true",
		KeyTranscodingMaxFPS:                 "0",
		KeyTranscodingThreads:                "0",
		KeyTranscodingConcurrency:            "16",
		KeyImportJobsConcurrency:             "1",
		KeyTranscodingOriginalResolution:     "true",
		KeyUploadAdditionalExtensionsEnabled: "false",
		KeyTranscodingResolutions:            `["2160","144"]`,
	}
	for key, val := range valid {
		if err := svc.Apply(ctx, map[string]Update{key: {Value: val}}, admin); err != nil {
			t.Errorf("Apply(%s=%s) = %v, want ok", key, val, err)
		}
	}
	for _, tc := range []struct{ key, val string }{
		{KeyTranscodingMaxFPS, "23"},  // below the 24 floor
		{KeyTranscodingMaxFPS, "241"}, // above the 240 cap
		{KeyTranscodingMaxFPS, "-1"},
		{KeyTranscodingThreads, "65"},
		{KeyTranscodingThreads, "-1"},
		{KeyTranscodingConcurrency, "0"}, // 0 is NOT unlimited here: 1..16
		{KeyTranscodingConcurrency, "17"},
		{KeyImportJobsConcurrency, "0"},
		{KeyImportJobsConcurrency, "17"},
		{KeyTranscodingResolutions, `[]`},            // empty ladder refused
		{KeyTranscodingResolutions, `["999"]`},       // non-canonical rung
		{KeyTranscodingResolutions, `["0"]`},         // no audio-only 0p rung in v1
		{KeyTranscodingResolutions, `["720","720"]`}, // duplicates refused
		{KeyTranscodingResolutions, `["720p"]`},      // strings must be bare heights
		{KeyTranscodingResolutions, `"1080"`},        // must be an array
	} {
		err := svc.Apply(ctx, map[string]Update{tc.key: {Value: tc.val}}, admin)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Key != tc.key {
			t.Errorf("Apply(%s=%s) = %v, want ValidationError on the key", tc.key, tc.val, err)
		}
	}

	// Boundary accepts.
	for _, tc := range []struct{ key, val string }{
		{KeyTranscodingMaxFPS, "24"},
		{KeyTranscodingMaxFPS, "240"},
		{KeyTranscodingThreads, "1"},
		{KeyTranscodingThreads, "64"},
		{KeyTranscodingConcurrency, "1"},
	} {
		if err := svc.Apply(ctx, map[string]Update{tc.key: {Value: tc.val}}, admin); err != nil {
			t.Errorf("Apply(%s=%s) = %v, want ok (boundary value)", tc.key, tc.val, err)
		}
	}

	// null-PATCH-clears-override convention: a delete falls back to the default.
	if err := svc.Apply(ctx, map[string]Update{KeyTranscodingResolutions: {Delete: true}}, admin); err != nil {
		t.Fatalf("Apply(delete transcoding_resolutions): %v", err)
	}
	if got := svc.Strings(KeyTranscodingResolutions); FormatList(got) != `["1080","720","480","360"]` {
		t.Errorf("after delete, transcoding_resolutions = %v, want the default ladder", got)
	}

	// ParseRungHeights maps stored list items back to ints for the encode seam.
	if got := ParseRungHeights([]string{"1080", " 720", "junk"}); len(got) != 2 || got[0] != 1080 || got[1] != 720 {
		t.Errorf("ParseRungHeights = %v, want [1080 720]", got)
	}
}

// TestW11LiveKnobsRegistry covers the live streaming enforcement knobs
// (config-parity W11): kinds, hardcoded defaults (replay allowed, save-replay
// default off, every limit 0 = unlimited/no limit), admin-IA placement on the
// live page, validator boundaries, and the null-PATCH reset convention. The
// deferred live-transcoding cluster must NOT be registered (architecture
// note 7 — no dormant keys).
func TestW11LiveKnobsRegistry(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	if err := svc.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Defaults + kinds + placement.
	if !svc.Bool(KeyLiveAllowReplay) {
		t.Error("live_allow_replay default = false, want true (the shipped behaviour)")
	}
	if svc.Bool(KeyLiveDefaultSaveReplay) {
		t.Error("live_default_save_replay default = true, want false")
	}
	for _, key := range []string{KeyLiveMaxInstanceLives, KeyLiveMaxUserLives, KeyLiveMaxDurationSecs} {
		if got := svc.Int(key); got != 0 {
			t.Errorf("%s default = %d, want 0 (unlimited/no limit)", key, got)
		}
	}
	for key, sect := range map[string]string{
		KeyLiveAllowReplay:       "replay",
		KeyLiveDefaultSaveReplay: "replay",
	} {
		if e := snapshotByKey(t, svc, key); e.Kind != KindBool || e.Page != PageLive || e.Section != sect {
			t.Errorf("%s = kind %s at %s/%s, want bool at live/%s", key, e.Kind, e.Page, e.Section, sect)
		}
	}
	for _, key := range []string{KeyLiveMaxInstanceLives, KeyLiveMaxUserLives, KeyLiveMaxDurationSecs} {
		if e := snapshotByKey(t, svc, key); e.Kind != KindInt || e.Page != PageLive || e.Section != "limits" {
			t.Errorf("%s = kind %s at %s/%s, want int at live/limits", key, e.Kind, e.Page, e.Section)
		}
	}

	// Validator boundaries.
	admin := uuid.New()
	for _, tc := range []struct{ key, val string }{
		{KeyLiveAllowReplay, "false"},
		{KeyLiveDefaultSaveReplay, "true"},
		{KeyLiveMaxInstanceLives, "0"},
		{KeyLiveMaxInstanceLives, "10000"},
		{KeyLiveMaxUserLives, "1"},
		{KeyLiveMaxDurationSecs, "0"},       // no limit
		{KeyLiveMaxDurationSecs, "60"},      // floor
		{KeyLiveMaxDurationSecs, "2592000"}, // 30-day ceiling
	} {
		if err := svc.Apply(ctx, map[string]Update{tc.key: {Value: tc.val}}, admin); err != nil {
			t.Errorf("Apply(%s=%s) = %v, want ok", tc.key, tc.val, err)
		}
	}
	for _, tc := range []struct{ key, val string }{
		{KeyLiveMaxInstanceLives, "-1"}, // 0 = unlimited, never PT's -1
		{KeyLiveMaxInstanceLives, "10001"},
		{KeyLiveMaxUserLives, "-1"},
		{KeyLiveMaxDurationSecs, "59"}, // sub-minute limits are watchdog noise
		{KeyLiveMaxDurationSecs, "2592001"},
		{KeyLiveMaxDurationSecs, "-1"},
		{KeyLiveAllowReplay, "yes"},
	} {
		err := svc.Apply(ctx, map[string]Update{tc.key: {Value: tc.val}}, admin)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Key != tc.key {
			t.Errorf("Apply(%s=%s) = %v, want ValidationError on the key", tc.key, tc.val, err)
		}
	}

	// null-PATCH-clears-override: allow_replay returns to its true default.
	if err := svc.Apply(ctx, map[string]Update{KeyLiveAllowReplay: {Delete: true}}, admin); err != nil {
		t.Fatalf("Apply(delete live_allow_replay): %v", err)
	}
	if !svc.Bool(KeyLiveAllowReplay) {
		t.Error("after delete, live_allow_replay = false, want the true default")
	}

	// Architecture note 7: the deferred live-transcoding cluster stays
	// unregistered — no dormant keys without apply points.
	for _, key := range []string{"live_transcoding_enabled", "live_transcoding_resolutions", "live_dvr_max_window_mins", "live_latency_mode"} {
		if _, known := KindOf(key); known {
			t.Errorf("deferred key %q is registered; architecture note 7 forbids dormant live-transcoding keys", key)
		}
	}
}
