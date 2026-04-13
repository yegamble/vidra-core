package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name   string
		status MigrationStatus
		want   bool
	}{
		{"completed is terminal", MigrationStatusCompleted, true},
		{"cancelled is terminal", MigrationStatusCancelled, true},
		{"pending is not terminal", MigrationStatusPending, false},
		{"running is not terminal", MigrationStatusRunning, false},
		{"failed is not terminal", MigrationStatusFailed, false},
		{"dry_run is not terminal", MigrationStatusDryRun, false},
		{"validating is not terminal", MigrationStatusValidating, false},
		{"resuming is not terminal", MigrationStatusResuming, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.IsTerminal())
		})
	}
}

func TestMigrationRequest_Validate(t *testing.T) {
	validReq := func() MigrationRequest {
		return MigrationRequest{
			SourceHost:       "peertube.example.com",
			SourceDBHost:     "db.example.com",
			SourceDBPort:     5432,
			SourceDBName:     "peertube_prod",
			SourceDBUser:     "peertube",
			SourceDBPassword: "secret",
		}
	}

	tests := []struct {
		name    string
		modify  func(r *MigrationRequest)
		wantErr string
	}{
		{"valid request", func(r *MigrationRequest) {}, ""},
		{"missing source_host", func(r *MigrationRequest) { r.SourceHost = "" }, "source_host is required"},
		{"missing source_db_host", func(r *MigrationRequest) { r.SourceDBHost = "" }, "source_db_host is required"},
		{"missing source_db_name", func(r *MigrationRequest) { r.SourceDBName = "" }, "source_db_name is required"},
		{"missing source_db_user", func(r *MigrationRequest) { r.SourceDBUser = "" }, "source_db_user is required"},
		{"missing source_db_password", func(r *MigrationRequest) { r.SourceDBPassword = "" }, "source_db_password is required"},
		{"zero port defaults to 5432", func(r *MigrationRequest) { r.SourceDBPort = 0 }, ""},
		{"negative port defaults to 5432", func(r *MigrationRequest) { r.SourceDBPort = -1 }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq()
			tt.modify(&req)
			err := req.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.True(t, req.SourceDBPort > 0, "port should be set to default")
			}
		})
	}
}

func TestMigrationJob_GetStats_SetStats(t *testing.T) {
	t.Run("empty stats JSON returns empty struct", func(t *testing.T) {
		job := &MigrationJob{}
		stats, err := job.GetStats()
		require.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, 0, stats.Users.Total)
	})

	t.Run("round-trip stats through JSON", func(t *testing.T) {
		job := &MigrationJob{}
		input := &MigrationStats{
			Users:    EntityStats{Total: 100, Migrated: 80, Skipped: 10, Failed: 10},
			Videos:   EntityStats{Total: 500, Migrated: 450, Failed: 50},
			Channels: EntityStats{Total: 20, Migrated: 20},
		}

		require.NoError(t, job.SetStats(input))
		assert.NotEmpty(t, job.StatsJSON)

		output, err := job.GetStats()
		require.NoError(t, err)
		assert.Equal(t, input.Users.Total, output.Users.Total)
		assert.Equal(t, input.Users.Migrated, output.Users.Migrated)
		assert.Equal(t, input.Videos.Total, output.Videos.Total)
		assert.Equal(t, input.Videos.Failed, output.Videos.Failed)
		assert.Equal(t, input.Channels.Migrated, output.Channels.Migrated)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		job := &MigrationJob{StatsJSON: json.RawMessage(`{invalid}`)}
		_, err := job.GetStats()
		assert.Error(t, err)
	})
}

func TestMigrationJob_CanTransition(t *testing.T) {
	tests := []struct {
		name      string
		from      MigrationStatus
		to        MigrationStatus
		canChange bool
	}{
		// Valid transitions from pending
		{"pending → running", MigrationStatusPending, MigrationStatusRunning, true},
		{"pending → cancelled", MigrationStatusPending, MigrationStatusCancelled, true},
		{"pending → failed", MigrationStatusPending, MigrationStatusFailed, true},
		{"pending → dry_run", MigrationStatusPending, MigrationStatusDryRun, true},
		{"pending → validating", MigrationStatusPending, MigrationStatusValidating, true},
		{"pending → completed invalid", MigrationStatusPending, MigrationStatusCompleted, false},

		// Valid transitions from running
		{"running → completed", MigrationStatusRunning, MigrationStatusCompleted, true},
		{"running → failed", MigrationStatusRunning, MigrationStatusFailed, true},
		{"running → cancelled", MigrationStatusRunning, MigrationStatusCancelled, true},
		{"running → pending invalid", MigrationStatusRunning, MigrationStatusPending, false},

		// Valid transitions from failed (resume support)
		{"failed → resuming", MigrationStatusFailed, MigrationStatusResuming, true},
		{"failed → running invalid", MigrationStatusFailed, MigrationStatusRunning, false},

		// Valid transitions from resuming
		{"resuming → running", MigrationStatusResuming, MigrationStatusRunning, true},
		{"resuming → failed", MigrationStatusResuming, MigrationStatusFailed, true},

		// Valid transitions from validating
		{"validating → running", MigrationStatusValidating, MigrationStatusRunning, true},
		{"validating → failed", MigrationStatusValidating, MigrationStatusFailed, true},
		{"validating → cancelled", MigrationStatusValidating, MigrationStatusCancelled, true},

		// Valid transitions from dry_run
		{"dry_run → completed", MigrationStatusDryRun, MigrationStatusCompleted, true},
		{"dry_run → failed", MigrationStatusDryRun, MigrationStatusFailed, true},
		{"dry_run → running invalid", MigrationStatusDryRun, MigrationStatusRunning, false},

		// Terminal states cannot transition
		{"completed → running blocked", MigrationStatusCompleted, MigrationStatusRunning, false},
		{"cancelled → running blocked", MigrationStatusCancelled, MigrationStatusRunning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &MigrationJob{Status: tt.from}
			assert.Equal(t, tt.canChange, job.CanTransition(tt.to))
		})
	}
}
