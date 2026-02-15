package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiresSetup(t *testing.T) {
	tests := []struct {
		name          string
		envFileExists bool
		envContent    string
		wantSetup     bool
		wantReason    string
	}{
		{
			name:          "missing .env file",
			envFileExists: false,
			wantSetup:     true,
			wantReason:    "missing .env file",
		},
		{
			name:          "empty .env file",
			envFileExists: true,
			envContent:    "",
			wantSetup:     true,
			wantReason:    "missing SETUP_COMPLETED flag",
		},
		{
			name:          "setup not completed",
			envFileExists: true,
			envContent:    "DATABASE_URL=postgres://localhost/test\nREDIS_URL=redis://localhost:6379\n",
			wantSetup:     true,
			wantReason:    "missing SETUP_COMPLETED flag",
		},
		{
			name:          "setup completed",
			envFileExists: true,
			envContent:    "DATABASE_URL=postgres://localhost/test\nREDIS_URL=redis://localhost:6379\nSETUP_COMPLETED=true\n",
			wantSetup:     false,
			wantReason:    "",
		},
		{
			name:          "setup completed but explicitly false",
			envFileExists: true,
			envContent:    "SETUP_COMPLETED=false\n",
			wantSetup:     true,
			wantReason:    "setup explicitly not completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			envPath := filepath.Join(tmpDir, ".env")

			if tt.envFileExists {
				err := os.WriteFile(envPath, []byte(tt.envContent), 0644)
				require.NoError(t, err)
			}

			needsSetup, reason := RequiresSetup(envPath)

			assert.Equal(t, tt.wantSetup, needsSetup, "RequiresSetup() needsSetup")
			if tt.wantSetup {
				assert.Contains(t, reason, tt.wantReason, "RequiresSetup() reason")
			}
		})
	}
}

func TestIsSetupCompleted(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"True", "True", true},
		{"1", "1", true},
		{"false", "false", false},
		{"FALSE", "FALSE", false},
		{"0", "0", false},
		{"empty", "", false},
		{"random", "random", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("SETUP_COMPLETED", tt.envValue)
			}

			got := IsSetupCompleted()
			assert.Equal(t, tt.want, got)
		})
	}
}
