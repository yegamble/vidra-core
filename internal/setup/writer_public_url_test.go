package setup

import (
	"testing"
)

func TestComputePublicBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		port     int
		protocol string
		want     string
	}{
		{
			name:     "HTTP port 80 omits port",
			domain:   "localhost",
			port:     80,
			protocol: "http",
			want:     "http://localhost",
		},
		{
			name:     "HTTPS port 443 omits port",
			domain:   "example.com",
			port:     443,
			protocol: "https",
			want:     "https://example.com",
		},
		{
			name:     "HTTP custom port includes port",
			domain:   "example.com",
			port:     8080,
			protocol: "http",
			want:     "http://example.com:8080",
		},
		{
			name:     "HTTPS custom port includes port",
			domain:   "example.com",
			port:     8443,
			protocol: "https",
			want:     "https://example.com:8443",
		},
		{
			name:     "HTTP with domain and default port",
			domain:   "videos.local",
			port:     80,
			protocol: "http",
			want:     "http://videos.local",
		},
		{
			name:     "HTTPS with subdomain and default port",
			domain:   "videos.example.com",
			port:     443,
			protocol: "https",
			want:     "https://videos.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computePublicBaseURL(tt.domain, tt.port, tt.protocol)
			if got != tt.want {
				t.Errorf("computePublicBaseURL(%q, %d, %q) = %q, want %q",
					tt.domain, tt.port, tt.protocol, got, tt.want)
			}
		})
	}
}
