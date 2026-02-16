package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateDatabaseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid postgres URL",
			url:     "postgres://user:pass@localhost:5432/dbname",
			wantErr: false,
		},
		{
			name:    "valid postgresql URL",
			url:     "postgresql://user:pass@localhost:5432/dbname",
			wantErr: false,
		},
		{
			name:    "invalid schema",
			url:     "mysql://user:pass@localhost:3306/dbname",
			wantErr: true,
		},
		{
			name:    "contains shell metacharacters",
			url:     "postgres://user;rm -rf /;@localhost/db",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDatabaseURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRedisURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid redis URL",
			url:     "redis://localhost:6379/0",
			wantErr: false,
		},
		{
			name:    "valid with auth",
			url:     "redis://:password@localhost:6379/0",
			wantErr: false,
		},
		{
			name:    "invalid schema",
			url:     "http://localhost:6379",
			wantErr: true,
		},
		{
			name:    "contains shell metacharacters",
			url:     "redis://localhost:6379/0&cmd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRedisURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateJWTSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{
			name:    "valid 32 char secret",
			secret:  "a8b9c0d1e2f3g4h5i6j7k8l9m0n1o2p3",
			wantErr: false,
		},
		{
			name:    "valid 64 char secret",
			secret:  "a8b9c0d1e2f3g4h5i6j7k8l9m0n1o2p3q4r5s6t7u8v9w0x1y2z3a4b5c6d7e8f9",
			wantErr: false,
		},
		{
			name:    "too short",
			secret:  "short",
			wantErr: true,
		},
		{
			name:    "weak value - secret",
			secret:  "secret12345678901234567890123456",
			wantErr: true,
		},
		{
			name:    "weak value - password",
			secret:  "password1234567890123456789012",
			wantErr: true,
		},
		{
			name:    "weak value - 12345",
			secret:  "12345678901234567890123456789012",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJWTSecret(tt.secret)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestContainsShellMetachars(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{
			name:  "clean string",
			value: "normal-value_123",
			want:  false,
		},
		{
			name:  "semicolon",
			value: "value;command",
			want:  true,
		},
		{
			name:  "pipe",
			value: "value|other",
			want:  true,
		},
		{
			name:  "ampersand",
			value: "value&background",
			want:  true,
		},
		{
			name:  "dollar sign",
			value: "value$VAR",
			want:  true,
		},
		{
			name:  "backtick",
			value: "value`cmd`",
			want:  true,
		},
		{
			name:  "backslash",
			value: "value\\escape",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsShellMetachars(tt.value)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateDomain(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{
			name:    "valid localhost",
			domain:  "localhost",
			wantErr: false,
		},
		{
			name:    "valid hostname",
			domain:  "example.com",
			wantErr: false,
		},
		{
			name:    "valid subdomain",
			domain:  "videos.example.com",
			wantErr: false,
		},
		{
			name:    "valid IP address",
			domain:  "192.168.1.1",
			wantErr: false,
		},
		{
			name:    "empty domain",
			domain:  "",
			wantErr: true,
		},
		{
			name:    "contains shell metacharacters",
			domain:  "example.com;rm -rf /",
			wantErr: true,
		},
		{
			name:    "contains pipe",
			domain:  "example.com|whoami",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDomain(tt.domain)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{
			name:    "valid port 80",
			port:    80,
			wantErr: false,
		},
		{
			name:    "valid port 443",
			port:    443,
			wantErr: false,
		},
		{
			name:    "valid port 8080",
			port:    8080,
			wantErr: false,
		},
		{
			name:    "valid high port",
			port:    65535,
			wantErr: false,
		},
		{
			name:    "port 0",
			port:    0,
			wantErr: true,
		},
		{
			name:    "port above max",
			port:    65536,
			wantErr: true,
		},
		{
			name:    "negative port",
			port:    -1,
			wantErr: true,
		},
		{
			name:    "privileged port 22 (allowed only 80/443)",
			port:    22,
			wantErr: true,
		},
		{
			name:    "privileged port 1023 (allowed only 80/443)",
			port:    1023,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
