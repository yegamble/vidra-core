package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelCreateRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     ChannelCreateRequest
		wantErr bool
	}{
		{
			name:    "valid request",
			req:     ChannelCreateRequest{Handle: "mychannel", DisplayName: "My Channel"},
			wantErr: false,
		},
		{
			name:    "empty handle",
			req:     ChannelCreateRequest{Handle: "", DisplayName: "My Channel"},
			wantErr: true,
		},
		{
			name:    "empty display name",
			req:     ChannelCreateRequest{Handle: "mychannel", DisplayName: ""},
			wantErr: true,
		},
		{
			name:    "both empty",
			req:     ChannelCreateRequest{Handle: "", DisplayName: ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidInput)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestChannel_PopulateFileURLs(t *testing.T) {
	t.Run("sets FileURL on avatar and banner", func(t *testing.T) {
		ch := &Channel{
			Avatar: &ChannelAvatar{URL: "https://example.com/avatar.jpg"},
			Banner: &ChannelBanner{URL: "https://example.com/banner.jpg"},
		}
		ch.PopulateFileURLs()
		assert.Equal(t, "https://example.com/avatar.jpg", ch.Avatar.FileURL)
		assert.Equal(t, "https://example.com/banner.jpg", ch.Banner.FileURL)
	})

	t.Run("nil avatar and banner does not panic", func(t *testing.T) {
		ch := &Channel{}
		assert.NotPanics(t, func() { ch.PopulateFileURLs() })
	})

	t.Run("empty URLs are not copied", func(t *testing.T) {
		ch := &Channel{
			Avatar: &ChannelAvatar{URL: ""},
			Banner: &ChannelBanner{URL: ""},
		}
		ch.PopulateFileURLs()
		assert.Empty(t, ch.Avatar.FileURL)
		assert.Empty(t, ch.Banner.FileURL)
	})

	t.Run("only avatar set", func(t *testing.T) {
		ch := &Channel{
			Avatar: &ChannelAvatar{URL: "https://example.com/avatar.jpg"},
		}
		ch.PopulateFileURLs()
		assert.Equal(t, "https://example.com/avatar.jpg", ch.Avatar.FileURL)
	})
}

func TestChannelUpdateRequest_Validate(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name    string
		req     ChannelUpdateRequest
		wantErr bool
	}{
		{
			name:    "display name only",
			req:     ChannelUpdateRequest{DisplayName: strPtr("New Name")},
			wantErr: false,
		},
		{
			name:    "description only",
			req:     ChannelUpdateRequest{Description: strPtr("New description")},
			wantErr: false,
		},
		{
			name:    "support only",
			req:     ChannelUpdateRequest{Support: strPtr("Buy me a coffee")},
			wantErr: false,
		},
		{
			name:    "all fields set",
			req:     ChannelUpdateRequest{DisplayName: strPtr("Name"), Description: strPtr("Desc"), Support: strPtr("Support")},
			wantErr: false,
		},
		{
			name:    "no fields set returns error",
			req:     ChannelUpdateRequest{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidInput)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
