package setup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateDatabaseIfNotExists(t *testing.T) {
	t.Skip("Integration test - requires actual PostgreSQL server")

	tests := []struct {
		name        string
		databaseURL string
		wantErr     bool
	}{
		{
			name:        "valid postgres connection",
			databaseURL: "postgres://user:pass@localhost:5432/athena",
			wantErr:     false,
		},
		{
			name:        "invalid connection string",
			databaseURL: "invalid",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := CreateDatabaseIfNotExists(ctx, tt.databaseURL)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateAdminUser(t *testing.T) {
	t.Skip("Integration test - requires actual database")

	tests := []struct {
		name     string
		username string
		email    string
		password string
		wantErr  bool
	}{
		{
			name:     "valid admin user",
			username: "admin",
			email:    "admin@athena.local",
			password: "securepassword123",
			wantErr:  false,
		},
		{
			name:     "empty username",
			username: "",
			email:    "admin@athena.local",
			password: "securepassword123",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			databaseURL := "postgres://user:pass@localhost:5432/athena"

			err := CreateAdminUser(ctx, databaseURL, tt.username, tt.email, tt.password)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractDatabaseName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "standard postgres URL",
			url:  "postgres://user:pass@localhost:5432/athena",
			want: "athena",
		},
		{
			name: "with query parameters",
			url:  "postgres://user:pass@localhost:5432/mydb?sslmode=disable",
			want: "mydb",
		},
		{
			name: "invalid URL",
			url:  "invalid",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDatabaseName(tt.url)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReplaceDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		newName string
		want    string
	}{
		{
			name:    "replace database name",
			url:     "postgres://user:pass@localhost:5432/athena",
			newName: "postgres",
			want:    "postgres://user:pass@localhost:5432/postgres",
		},
		{
			name:    "invalid URL unchanged",
			url:     "invalid",
			newName: "postgres",
			want:    "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceDatabaseName(tt.url, tt.newName)
			assert.Equal(t, tt.want, got)
		})
	}
}
