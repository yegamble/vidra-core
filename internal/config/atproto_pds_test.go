package config

import "testing"

func TestDefaultATProtoPDS(t *testing.T) {
	for _, tt := range []struct{ name, site, external, want string }{
		{"local", "https://video.example", "", "https://pds.video.example"},
		{"external", "https://video.example", "https://pds.example:8443/", "https://pds.example:8443"},
		{"credentials", "https://video.example", "https://a:b@pds.example", ""},
		{"path", "https://video.example", "https://pds.example/xrpc", ""},
		{"plaintext", "https://video.example", "http://pds.example", ""},
		{"query", "https://video.example", "https://pds.example?secret=x", ""},
		{"no host", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{ATProtoEnabled: true, PublicBaseURL: tt.site, ATProtoPDSURL: tt.external}
			if got := cfg.DefaultATProtoPDS(); got != tt.want {
				t.Errorf("default PDS = %q, want %q", got, tt.want)
			}
		})
	}
}
