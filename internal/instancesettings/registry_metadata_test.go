package instancesettings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestRegistryPageSectionMetadata proves every registered setting carries a
// valid admin-IA placement (config-parity W1): a page from the canonical page
// set and a non-empty section, both surfaced through Snapshot so the
// metadata-driven admin UI can auto-place keys.
func TestRegistryPageSectionMetadata(t *testing.T) {
	validPages := map[string]bool{}
	for _, p := range Pages {
		validPages[p] = true
	}
	svc := NewService(newFakeRepo(), testDefaults())
	_ = svc.Load(context.Background())
	snap := svc.Snapshot()
	if len(snap) == 0 {
		t.Fatal("empty snapshot")
	}
	for _, e := range snap {
		if !validPages[e.Page] {
			t.Errorf("%s: page = %q, want one of %v", e.Key, e.Page, Pages)
		}
		if e.Section == "" {
			t.Errorf("%s: section is empty", e.Key)
		}
	}

	// Spot-check the IA decisions the waves plan pins down.
	placements := map[string][2]string{
		KeyInstanceName:              {PageGeneral, "identity"},
		KeyTerms:                     {PageGeneral, "about"},
		KeyQuarantineNewUploads:      {PageGeneral, "moderation"},
		KeySensitiveContentPolicy:    {PageGeneral, "moderation"},
		KeyBroadcastEnabled:          {PageGeneral, "broadcast"},
		KeyDefaultFeedSort:           {PageGeneral, "browse"},
		KeyUploadsEnabled:            {PageVOD, "uploads"},
		KeyUploadMaxSizeBytes:        {PageVOD, "uploads"},
		KeyImportMaxHeight:           {PageVOD, "imports"},
		KeyDefaultVideoPrivacy:       {PageVOD, "publish_defaults"},
		KeyLiveEnabled:               {PageLive, "streaming"},
		KeyDefaultTheme:              {PageCustomization, "theme"},
		KeyEmailSubjectPrefix:        {PageCustomization, "email"},
		KeySocialMetaTwitterUsername: {PageGeneral, "social"},
	}
	for key, want := range placements {
		e := snapshotByKey(t, svc, key)
		if e.Page != want[0] || e.Section != want[1] {
			t.Errorf("%s placed at %s/%s, want %s/%s", key, e.Page, e.Section, want[0], want[1])
		}
	}
}

// TestCoreConfigSurfaceDefaults proves the W1-registered keys resolve their
// hardcoded defaults with no override rows.
func TestCoreConfigSurfaceDefaults(t *testing.T) {
	svc := NewService(newFakeRepo(), testDefaults())
	_ = svc.Load(context.Background())

	if svc.Bool(KeyBroadcastEnabled) || svc.Bool(KeyBroadcastDismissable) {
		t.Error("broadcast bools default true, want false")
	}
	if got := svc.String(KeyBroadcastLevel); got != "info" {
		t.Errorf("broadcast_level default = %q, want info", got)
	}
	if got := svc.String(KeyDefaultFeedSort); got != "recent" {
		t.Errorf("default_feed_sort default = %q, want recent", got)
	}
	if got := svc.String(KeyDefaultFeedScope); got != "local" {
		t.Errorf("default_feed_scope default = %q, want local", got)
	}
	if got := svc.String(KeyDefaultLandingPage); got != "home-recent" {
		t.Errorf("default_landing_page default = %q, want home-recent", got)
	}
	if got := svc.String(KeyDefaultTheme); got != "system" {
		t.Errorf("default_theme default = %q, want system", got)
	}
	if !svc.Bool(KeyDefaultPlayerAutoplay) {
		t.Error("default_player_autoplay default = false, want true")
	}
	if svc.Bool(KeyMiniaturePreferAuthorDisplayName) {
		t.Error("miniature_prefer_author_display_name default = true, want false")
	}
	if got := svc.String(KeyDefaultVideoPrivacy); got != "public" {
		t.Errorf("default_video_privacy default = %q, want public", got)
	}
	if got := svc.Int(KeyDefaultVideoLicence); got != 0 {
		t.Errorf("default_video_licence default = %d, want 0 (no default)", got)
	}
	if got := svc.String(KeyDefaultCommentPolicy); got != "enabled" {
		t.Errorf("default_comment_policy default = %q, want enabled", got)
	}
	if svc.Bool(KeyDefaultDownloadEnabled) {
		t.Error("default_download_enabled default = true, want false")
	}
	for _, key := range []string{KeyThemePrimaryColor, KeySocialMetaTwitterUsername, KeyEmailSubjectPrefix, KeyEmailBodySignature, KeyBroadcastMessage} {
		if got := svc.String(key); got != "" {
			t.Errorf("%s default = %q, want empty", key, got)
		}
	}
	if svc.Bool(KeyHeaderHideInstanceName) {
		t.Error("header_hide_instance_name default = true, want false")
	}
}

// TestCoreConfigSurfaceValidators covers the new validators: hex color,
// twitter username, licence id, and the enum option sets.
func TestCoreConfigSurfaceValidators(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo(), testDefaults())
	_ = svc.Load(ctx)

	valid := map[string]Update{
		KeyThemePrimaryColor:         {Value: "#0F62Fe"},
		KeySocialMetaTwitterUsername: {Value: "@vidra_video"},
		KeyDefaultVideoLicence:       {Value: "4"},
		KeyBroadcastLevel:            {Value: "warning"},
		KeyDefaultLandingPage:        {Value: "trending"},
		KeyDefaultVideoPrivacy:       {Value: "unlisted"},
		KeyDefaultCommentPolicy:      {Value: "disabled"},
	}
	if err := svc.Apply(ctx, valid, uuid.New()); err != nil {
		t.Fatalf("valid batch rejected: %v", err)
	}
	if got := svc.String(KeyThemePrimaryColor); got != "#0F62Fe" {
		t.Errorf("theme_primary_color = %q after apply", got)
	}
	if got := svc.Int(KeyDefaultVideoLicence); got != 4 {
		t.Errorf("default_video_licence = %d, want 4", got)
	}

	invalid := []struct {
		name string
		key  string
		val  string
	}{
		{"short hex", KeyThemePrimaryColor, "#fff"},
		{"no hash hex", KeyThemePrimaryColor, "0f62fe00"},
		{"long handle", KeySocialMetaTwitterUsername, "@a_very_long_username"},
		{"handle with space", KeySocialMetaTwitterUsername, "@vid ra"},
		{"unknown licence", KeyDefaultVideoLicence, "999"},
		{"negative licence", KeyDefaultVideoLicence, "-1"},
		{"bad broadcast level", KeyBroadcastLevel, "fatal"},
		{"bad feed sort", KeyDefaultFeedSort, "hot"},
		{"bad theme", KeyDefaultTheme, "solarized"},
		{"password privacy default", KeyDefaultVideoPrivacy, "password"},
	}
	for _, tc := range invalid {
		err := svc.Apply(ctx, map[string]Update{tc.key: {Value: tc.val}}, uuid.New())
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Key != tc.key {
			t.Errorf("%s: err = %v, want *ValidationError for %s", tc.name, err, tc.key)
		}
	}
}
