package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions []string
		wantErr     bool
	}{
		{
			name:        "valid permissions",
			permissions: []string{"read_videos", "write_videos"},
			wantErr:     false,
		},
		{
			name:        "empty permissions",
			permissions: []string{},
			wantErr:     false,
		},
		{
			name:        "invalid permission",
			permissions: []string{"invalid_permission"},
			wantErr:     true,
		},
		{
			name:        "mix of valid and invalid",
			permissions: []string{"read_videos", "invalid_permission"},
			wantErr:     true,
		},
		{
			name: "all valid permissions",
			permissions: []string{
				"read_videos", "write_videos", "delete_videos",
				"read_users", "write_users", "delete_users",
				"read_channels", "write_channels", "delete_channels",
				"read_storage", "write_storage", "delete_storage",
				"read_analytics", "write_analytics",
				"moderate_content", "admin_access", "register_api_routes",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePermissions(tt.permissions)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPluginInfo_HasPermission(t *testing.T) {
	info := &PluginInfo{
		Name:        "test-plugin",
		Permissions: []string{"read_videos", "write_videos"},
	}

	tests := []struct {
		name       string
		permission Permission
		want       bool
	}{
		{
			name:       "has permission",
			permission: PermissionReadVideos,
			want:       true,
		},
		{
			name:       "has another permission",
			permission: PermissionWriteVideos,
			want:       true,
		},
		{
			name:       "does not have permission",
			permission: PermissionDeleteVideos,
			want:       false,
		},
		{
			name:       "does not have admin permission",
			permission: PermissionAdminAccess,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := info.HasPermission(tt.permission)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestPluginInfo_HasPermission_EmptyPermissions(t *testing.T) {
	info := &PluginInfo{
		Name:        "test-plugin",
		Permissions: []string{},
	}

	result := info.HasPermission(PermissionReadVideos)
	assert.False(t, result)
}

func TestPluginInfo_RequirePermission(t *testing.T) {
	info := &PluginInfo{
		Name:        "test-plugin",
		Permissions: []string{"read_videos", "write_videos"},
	}

	t.Run("has required permission", func(t *testing.T) {
		err := info.RequirePermission(PermissionReadVideos)
		assert.NoError(t, err)
	})

	t.Run("missing required permission", func(t *testing.T) {
		err := info.RequirePermission(PermissionDeleteVideos)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test-plugin")
		assert.Contains(t, err.Error(), "delete_videos")
	})

	t.Run("missing admin permission", func(t *testing.T) {
		err := info.RequirePermission(PermissionAdminAccess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "admin_access")
	})
}

func TestValidPermissions_AllConstantsDefined(t *testing.T) {
	// Test that all permission constants are in the ValidPermissions map
	permissions := []Permission{
		PermissionReadVideos,
		PermissionWriteVideos,
		PermissionDeleteVideos,
		PermissionReadUsers,
		PermissionWriteUsers,
		PermissionDeleteUsers,
		PermissionReadChannels,
		PermissionWriteChannels,
		PermissionDeleteChannels,
		PermissionReadStorage,
		PermissionWriteStorage,
		PermissionDeleteStorage,
		PermissionReadAnalytics,
		PermissionWriteAnalytics,
		PermissionModerateContent,
		PermissionAdminAccess,
		PermissionRegisterAPIRoutes,
	}

	for _, perm := range permissions {
		t.Run(string(perm), func(t *testing.T) {
			valid, exists := ValidPermissions[perm]
			assert.True(t, exists, "Permission %s not in ValidPermissions map", perm)
			assert.True(t, valid, "Permission %s is false in ValidPermissions map", perm)
		})
	}

	// Verify count
	assert.Equal(t, len(permissions), len(ValidPermissions), "ValidPermissions map should contain all permission constants")
}

func TestPluginInfo_RequirePermission_MultiplePermissions(t *testing.T) {
	info := &PluginInfo{
		Name: "multi-perm-plugin",
		Permissions: []string{
			"read_videos",
			"write_videos",
			"read_users",
			"admin_access",
		},
	}

	requiredPermissions := []Permission{
		PermissionReadVideos,
		PermissionWriteVideos,
		PermissionReadUsers,
		PermissionAdminAccess,
	}

	for _, perm := range requiredPermissions {
		t.Run(string(perm), func(t *testing.T) {
			err := info.RequirePermission(perm)
			assert.NoError(t, err)
		})
	}

	// Test a permission not in the list
	err := info.RequirePermission(PermissionDeleteVideos)
	assert.Error(t, err)
}
