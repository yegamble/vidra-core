package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginRecord_Validate(t *testing.T) {
	validPlugin := func() *PluginRecord {
		return &PluginRecord{
			Name:        "my-plugin",
			Version:     "1.0.0",
			Author:      "author",
			Description: "A test plugin",
			InstallPath: "/plugins/my-plugin",
			Status:      PluginStatusInstalled,
		}
	}

	tests := []struct {
		name    string
		modify  func(p *PluginRecord)
		wantErr bool
		errIs   error
	}{
		{
			name:    "valid plugin record",
			modify:  func(p *PluginRecord) {},
			wantErr: false,
		},
		{
			name:    "empty name",
			modify:  func(p *PluginRecord) { p.Name = "" },
			wantErr: true,
			errIs:   ErrPluginInvalidName,
		},
		{
			name:    "empty version",
			modify:  func(p *PluginRecord) { p.Version = "" },
			wantErr: true,
			errIs:   ErrPluginInvalidVersion,
		},
		{
			name:    "empty author",
			modify:  func(p *PluginRecord) { p.Author = "" },
			wantErr: true,
		},
		{
			name:    "empty description",
			modify:  func(p *PluginRecord) { p.Description = "" },
			wantErr: true,
		},
		{
			name:    "empty install path",
			modify:  func(p *PluginRecord) { p.InstallPath = "" },
			wantErr: true,
		},
		{
			name:    "invalid status",
			modify:  func(p *PluginRecord) { p.Status = "bogus" },
			wantErr: true,
		},
		{
			name:    "status enabled is valid",
			modify:  func(p *PluginRecord) { p.Status = PluginStatusEnabled },
			wantErr: false,
		},
		{
			name:    "status disabled is valid",
			modify:  func(p *PluginRecord) { p.Status = PluginStatusDisabled },
			wantErr: false,
		},
		{
			name:    "status failed is valid",
			modify:  func(p *PluginRecord) { p.Status = PluginStatusFailed },
			wantErr: false,
		},
		{
			name:    "status updating is valid",
			modify:  func(p *PluginRecord) { p.Status = PluginStatusUpdating },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlugin()
			tt.modify(p)
			err := p.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPluginRecord_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		status PluginStatus
		want   bool
	}{
		{"enabled", PluginStatusEnabled, true},
		{"disabled", PluginStatusDisabled, false},
		{"installed", PluginStatusInstalled, false},
		{"failed", PluginStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PluginRecord{Status: tt.status}
			assert.Equal(t, tt.want, p.IsEnabled())
		})
	}
}

func TestPluginRecord_IsDisabled(t *testing.T) {
	tests := []struct {
		name   string
		status PluginStatus
		want   bool
	}{
		{"disabled", PluginStatusDisabled, true},
		{"enabled", PluginStatusEnabled, false},
		{"installed", PluginStatusInstalled, false},
		{"failed", PluginStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PluginRecord{Status: tt.status}
			assert.Equal(t, tt.want, p.IsDisabled())
		})
	}
}

func TestPluginRecord_IsFailed(t *testing.T) {
	tests := []struct {
		name   string
		status PluginStatus
		want   bool
	}{
		{"failed", PluginStatusFailed, true},
		{"enabled", PluginStatusEnabled, false},
		{"disabled", PluginStatusDisabled, false},
		{"installed", PluginStatusInstalled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PluginRecord{Status: tt.status}
			assert.Equal(t, tt.want, p.IsFailed())
		})
	}
}

func TestPluginRecord_Enable(t *testing.T) {
	t.Run("enable from installed", func(t *testing.T) {
		p := &PluginRecord{Status: PluginStatusInstalled}
		err := p.Enable()
		require.NoError(t, err)
		assert.Equal(t, PluginStatusEnabled, p.Status)
		assert.NotNil(t, p.EnabledAt)
		assert.Nil(t, p.DisabledAt)
		assert.Empty(t, p.LastError)
	})

	t.Run("enable from disabled", func(t *testing.T) {
		disabledAt := time.Now().Add(-1 * time.Hour)
		p := &PluginRecord{
			Status:     PluginStatusDisabled,
			DisabledAt: &disabledAt,
			LastError:  "previous error",
		}
		err := p.Enable()
		require.NoError(t, err)
		assert.Equal(t, PluginStatusEnabled, p.Status)
		assert.NotNil(t, p.EnabledAt)
		assert.Nil(t, p.DisabledAt)
		assert.Empty(t, p.LastError)
	})

	t.Run("enable already enabled returns error", func(t *testing.T) {
		p := &PluginRecord{Status: PluginStatusEnabled}
		err := p.Enable()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPluginAlreadyEnabled)
	})
}

func TestPluginRecord_Disable(t *testing.T) {
	t.Run("disable from enabled", func(t *testing.T) {
		p := &PluginRecord{Status: PluginStatusEnabled}
		err := p.Disable()
		require.NoError(t, err)
		assert.Equal(t, PluginStatusDisabled, p.Status)
		assert.NotNil(t, p.DisabledAt)
	})

	t.Run("disable from installed", func(t *testing.T) {
		p := &PluginRecord{Status: PluginStatusInstalled}
		err := p.Disable()
		require.NoError(t, err)
		assert.Equal(t, PluginStatusDisabled, p.Status)
		assert.NotNil(t, p.DisabledAt)
	})

	t.Run("disable already disabled returns error", func(t *testing.T) {
		p := &PluginRecord{Status: PluginStatusDisabled}
		err := p.Disable()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPluginAlreadyDisabled)
	})
}

func TestPluginRecord_MarkFailed(t *testing.T) {
	p := &PluginRecord{Status: PluginStatusEnabled}
	testErr := errors.New("crash on hook execution")
	p.MarkFailed(testErr)

	assert.Equal(t, PluginStatusFailed, p.Status)
	assert.Equal(t, "crash on hook execution", p.LastError)
	assert.False(t, p.UpdatedAt.IsZero())
}

func TestPluginRecord_ClearError(t *testing.T) {
	p := &PluginRecord{LastError: "some previous error"}
	p.ClearError()

	assert.Empty(t, p.LastError)
	assert.False(t, p.UpdatedAt.IsZero())
}

func TestPluginRecord_UpdateConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		p := &PluginRecord{}
		cfg := map[string]any{"key": "value", "count": 42}
		err := p.UpdateConfig(cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, p.Config)
		assert.False(t, p.UpdatedAt.IsZero())
	})

	t.Run("nil config returns error", func(t *testing.T) {
		p := &PluginRecord{}
		err := p.UpdateConfig(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPluginInvalidConfig)
	})
}

func TestPluginRecord_HasPermission(t *testing.T) {
	p := &PluginRecord{Permissions: []string{"read", "write", "admin"}}

	tests := []struct {
		name       string
		permission string
		want       bool
	}{
		{"has read", "read", true},
		{"has write", "write", true},
		{"has admin", "admin", true},
		{"missing delete", "delete", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, p.HasPermission(tt.permission))
		})
	}

	t.Run("nil permissions slice", func(t *testing.T) {
		p2 := &PluginRecord{Permissions: nil}
		assert.False(t, p2.HasPermission("read"))
	})
}

func TestPluginRecord_HasHook(t *testing.T) {
	p := &PluginRecord{Hooks: []string{"before_upload", "after_transcode"}}

	tests := []struct {
		name string
		hook string
		want bool
	}{
		{"has before_upload", "before_upload", true},
		{"has after_transcode", "after_transcode", true},
		{"missing on_delete", "on_delete", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, p.HasHook(tt.hook))
		})
	}

	t.Run("nil hooks slice", func(t *testing.T) {
		p2 := &PluginRecord{Hooks: nil}
		assert.False(t, p2.HasHook("before_upload"))
	})
}

func TestPluginStatistics_SuccessRate(t *testing.T) {
	tests := []struct {
		name  string
		stats PluginStatistics
		want  float64
	}{
		{
			name:  "zero executions returns zero",
			stats: PluginStatistics{TotalExecutions: 0, SuccessCount: 0},
			want:  0,
		},
		{
			name:  "all successful",
			stats: PluginStatistics{TotalExecutions: 100, SuccessCount: 100},
			want:  100,
		},
		{
			name:  "half successful",
			stats: PluginStatistics{TotalExecutions: 200, SuccessCount: 100},
			want:  50,
		},
		{
			name:  "none successful",
			stats: PluginStatistics{TotalExecutions: 50, SuccessCount: 0},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, tt.stats.SuccessRate(), 0.01)
		})
	}
}

func TestPluginStatistics_FailureRate(t *testing.T) {
	tests := []struct {
		name  string
		stats PluginStatistics
		want  float64
	}{
		{
			name:  "zero executions returns zero",
			stats: PluginStatistics{TotalExecutions: 0, FailureCount: 0},
			want:  0,
		},
		{
			name:  "all failed",
			stats: PluginStatistics{TotalExecutions: 100, FailureCount: 100},
			want:  100,
		},
		{
			name:  "25 percent failure",
			stats: PluginStatistics{TotalExecutions: 200, FailureCount: 50},
			want:  25,
		},
		{
			name:  "none failed",
			stats: PluginStatistics{TotalExecutions: 50, FailureCount: 0},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, tt.stats.FailureRate(), 0.01)
		})
	}
}

func TestPluginManifest_Validate(t *testing.T) {
	validManifest := func() *PluginManifest {
		return &PluginManifest{
			Name:        "test-plugin",
			Version:     "1.0.0",
			Author:      "author",
			Description: "A test plugin",
			Main:        "index.js",
		}
	}

	tests := []struct {
		name    string
		modify  func(m *PluginManifest)
		wantErr bool
		errIs   error
	}{
		{
			name:    "valid manifest",
			modify:  func(m *PluginManifest) {},
			wantErr: false,
		},
		{
			name:    "empty name",
			modify:  func(m *PluginManifest) { m.Name = "" },
			wantErr: true,
			errIs:   ErrPluginInvalidName,
		},
		{
			name:    "empty version",
			modify:  func(m *PluginManifest) { m.Version = "" },
			wantErr: true,
			errIs:   ErrPluginInvalidVersion,
		},
		{
			name:    "empty author",
			modify:  func(m *PluginManifest) { m.Author = "" },
			wantErr: true,
		},
		{
			name:    "empty description",
			modify:  func(m *PluginManifest) { m.Description = "" },
			wantErr: true,
		},
		{
			name:    "empty main",
			modify:  func(m *PluginManifest) { m.Main = "" },
			wantErr: true,
		},
		{
			name:    "invalid semver starting with letter",
			modify:  func(m *PluginManifest) { m.Version = "v1.0.0" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.modify(m)
			err := m.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPluginManifest_ToPluginRecord(t *testing.T) {
	manifest := &PluginManifest{
		Name:        "my-plugin",
		Version:     "2.1.0",
		Author:      "dev",
		Description: "Test desc",
		Main:        "index.js",
		Permissions: []string{"read", "write"},
		Hooks:       []string{"before_upload"},
		Config:      map[string]any{"debug": true},
	}

	record := manifest.ToPluginRecord("/opt/plugins/my-plugin", "abc123sha256")

	assert.NotEmpty(t, record.ID.String())
	assert.Equal(t, "my-plugin", record.Name)
	assert.Equal(t, "2.1.0", record.Version)
	assert.Equal(t, "dev", record.Author)
	assert.Equal(t, "Test desc", record.Description)
	assert.Equal(t, PluginStatusInstalled, record.Status)
	assert.Equal(t, "/opt/plugins/my-plugin", record.InstallPath)
	assert.Equal(t, "abc123sha256", record.Checksum)
	assert.Equal(t, []string{"read", "write"}, record.Permissions)
	assert.Equal(t, []string{"before_upload"}, record.Hooks)
	assert.Equal(t, map[string]any{"debug": true}, record.Config)
	assert.False(t, record.InstalledAt.IsZero())
	assert.False(t, record.UpdatedAt.IsZero())
}

func TestValidatePluginName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid lowercase", "my-plugin", false},
		{"valid with underscore", "my_plugin", false},
		{"valid alphanumeric", "plugin123", false},
		{"valid uppercase", "MyPlugin", false},
		{"valid exact 3 chars", "abc", false},
		{"empty string", "", true},
		{"too short 2 chars", "ab", true},
		{"too long 101 chars", string(make([]byte, 101)), true},
		{"contains space", "my plugin", true},
		{"contains dot", "my.plugin", true},
		{"contains at sign", "my@plugin", true},
		{"contains slash", "my/plugin", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For the "too long" case, generate a valid-character string
			input := tt.input
			if tt.name == "too long 101 chars" {
				b := make([]byte, 101)
				for i := range b {
					b[i] = 'a'
				}
				input = string(b)
			}
			err := ValidatePluginName(input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePluginVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{"valid semver", "1.0.0", false},
		{"valid with prerelease", "1.0.0-alpha", false},
		{"valid with build", "1.0.0+build", false},
		{"empty string", "", true},
		{"starts with letter", "v1.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePluginVersion(tt.version)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsValidSemanticVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{"standard semver", "1.0.0", true},
		{"with prerelease", "2.3.4-beta", true},
		{"empty string", "", false},
		{"starts with v", "v1.0.0", false},
		{"starts with letter", "abc", false},
		{"single digit", "1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidSemanticVersion(tt.version))
		})
	}
}
