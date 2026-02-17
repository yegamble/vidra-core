package setup

import (
	"testing"
)

func TestValidateIOTANodeURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid http URL",
			url:     "http://localhost:14265",
			wantErr: false,
		},
		{
			name:    "valid https URL",
			url:     "https://node.example.com:14265",
			wantErr: false,
		},
		{
			name:    "valid https URL no port",
			url:     "https://iota-node.example.com",
			wantErr: false,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
		{
			name:    "invalid scheme ftp",
			url:     "ftp://localhost:14265",
			wantErr: true,
		},
		{
			name:    "no scheme",
			url:     "localhost:14265",
			wantErr: true,
		},
		{
			name:    "shell metacharacters",
			url:     "http://localhost;rm -rf /",
			wantErr: true,
		},
		{
			name:    "shell pipe",
			url:     "http://localhost|cat",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIOTANodeURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIOTANodeURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIOTANetwork(t *testing.T) {
	tests := []struct {
		name    string
		network string
		wantErr bool
	}{
		{name: "testnet", network: "testnet", wantErr: false},
		{name: "mainnet", network: "mainnet", wantErr: false},
		{name: "empty", network: "", wantErr: true},
		{name: "invalid devnet", network: "devnet", wantErr: true},
		{name: "invalid uppercase", network: "MAINNET", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIOTANetwork(tt.network)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIOTANetwork(%q) error = %v, wantErr %v", tt.network, err, tt.wantErr)
			}
		})
	}
}
