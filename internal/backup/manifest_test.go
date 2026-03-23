package backup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestCreation(t *testing.T) {
	manifest := NewManifest("1.0.0", 61)

	assert.Equal(t, 1, manifest.Version)
	assert.Equal(t, "1.0.0", manifest.AppVersion)
	assert.Equal(t, int64(61), manifest.SchemaVersion)
	assert.NotZero(t, manifest.CreatedAt)
	assert.NotEmpty(t, manifest.Contents)
}

func TestManifestJSON(t *testing.T) {
	manifest := &Manifest{
		Version:         1,
		AppVersion:      "1.0.0",
		SchemaVersion:   61,
		GooseVersion:    "v3.x.x",
		CreatedAt:       time.Now().UTC(),
		Contents:        []string{"database.sql", "redis.rdb", "storage/"},
		PostgresVersion: "15.x",
		Checksum:        "sha256:abc123",
	}

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	var decoded Manifest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, manifest.Version, decoded.Version)
	assert.Equal(t, manifest.AppVersion, decoded.AppVersion)
	assert.Equal(t, manifest.SchemaVersion, decoded.SchemaVersion)
	assert.Equal(t, len(manifest.Contents), len(decoded.Contents))
}

func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Manifest)
		wantErr bool
	}{
		{
			name:    "valid manifest",
			modify:  func(m *Manifest) {},
			wantErr: false,
		},
		{
			name: "missing app version",
			modify: func(m *Manifest) {
				m.AppVersion = ""
			},
			wantErr: true,
		},
		{
			name: "invalid schema version",
			modify: func(m *Manifest) {
				m.SchemaVersion = -1
			},
			wantErr: true,
		},
		{
			name: "empty contents",
			modify: func(m *Manifest) {
				m.Contents = []string{}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := NewManifest("1.0.0", 61)
			tt.modify(manifest)

			err := manifest.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
