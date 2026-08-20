package main

import (
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/dbmigrate"
)

// unreachableDSN points at a closed port so any test below that accidentally
// reaches the database fails loudly (connection refused) instead of quietly
// passing against whatever Postgres the developer happens to have running.
const unreachableDSN = "postgres://vidra:vidra@127.0.0.1:1/vidra?sslmode=disable&connect_timeout=1"

// `migrate force` rewrites the ledger without running any SQL, so it must refuse
// every malformed invocation BEFORE opening a connection — a refusal has to mean
// "nothing happened".
func TestMigrateForceRefusesWithoutConfirmation(t *testing.T) {
	t.Setenv("DATABASE_URL", unreachableDSN)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no confirmation flag", []string{"force", "42"}, forceConfirmFlag},
		{"no version", []string{"force", forceConfirmFlag}, "usage: api"},
		{"two versions", []string{"force", "41", "42", forceConfirmFlag}, "usage: api"},
		{"non-numeric version", []string{"force", "latest", forceConfirmFlag}, "not a migration version"},
		{"version below -1", []string{"force", "-2", forceConfirmFlag}, "not a migration version"},
		{"unknown flag", []string{"force", "42", "--yes"}, "unknown flag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runMigrate(tc.args)
			if err == nil {
				t.Fatalf("runMigrate(%q) = nil, want a refusal", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runMigrate(%q) error = %q, want it to mention %q", tc.args, err, tc.want)
			}
			// Every case above is rejected by argument parsing, so no connection
			// should have been attempted.
			if strings.Contains(err.Error(), "connect") {
				t.Fatalf("runMigrate(%q) reached the database before refusing: %v", tc.args, err)
			}
		})
	}
}

// The usage string main.go prints has to name the recovery command, or the
// runbook's `force` is undiscoverable from the binary itself.
func TestMigrateUsageMentionsForce(t *testing.T) {
	for _, want := range []string{"up", "version", "force", forceConfirmFlag} {
		if !strings.Contains(migrateUsage, want) {
			t.Errorf("migrateUsage = %q, want it to mention %q", migrateUsage, want)
		}
	}
}

func TestFormatStatus(t *testing.T) {
	tests := []struct {
		name string
		st   dbmigrate.Status
		want string
	}{
		{"never migrated", dbmigrate.Status{}, "version=none dirty=false"},
		{"clean", dbmigrate.Status{Version: 104, Applied: true}, "version=104 dirty=false"},
		{"dirty", dbmigrate.Status{Version: 42, Dirty: true, Applied: true}, "version=42 dirty=true"},
	}
	for _, tc := range tests {
		if got := formatStatus(tc.st); got != tc.want {
			t.Errorf("%s: formatStatus() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
