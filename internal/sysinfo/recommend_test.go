package sysinfo

import (
	"strings"
	"testing"
)

func TestRecommend(t *testing.T) {
	tests := []struct {
		name              string
		resources         *Resources
		wantEnableIPFS    bool
		wantEnableClamAV  bool
		wantEnableWhisper bool
	}{
		{
			name: "minimal system - less than 2GB RAM",
			resources: &Resources{
				CPUCores: 1,
				RAMBytes: 1 * 1024 * 1024 * 1024,
				RAMGB:    1.0,
			},
			wantEnableIPFS:    false,
			wantEnableClamAV:  false,
			wantEnableWhisper: false,
		},
		{
			name: "standard system - 4GB RAM, 2 cores",
			resources: &Resources{
				CPUCores: 2,
				RAMBytes: 4 * 1024 * 1024 * 1024,
				RAMGB:    4.0,
			},
			wantEnableIPFS:    false,
			wantEnableClamAV:  true,
			wantEnableWhisper: false,
		},
		{
			name: "full system - 16GB RAM, 8 cores",
			resources: &Resources{
				CPUCores: 8,
				RAMBytes: 16 * 1024 * 1024 * 1024,
				RAMGB:    16.0,
			},
			wantEnableIPFS:    true,
			wantEnableClamAV:  true,
			wantEnableWhisper: true,
		},
		{
			name: "edge case - exactly 2GB",
			resources: &Resources{
				CPUCores: 2,
				RAMBytes: 2 * 1024 * 1024 * 1024,
				RAMGB:    2.0,
			},
			wantEnableIPFS:    false,
			wantEnableClamAV:  true,
			wantEnableWhisper: false,
		},
		{
			name: "edge case - exactly 8GB",
			resources: &Resources{
				CPUCores: 4,
				RAMBytes: 8 * 1024 * 1024 * 1024,
				RAMGB:    8.0,
			},
			wantEnableIPFS:    true,
			wantEnableClamAV:  true,
			wantEnableWhisper: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := Recommend(tt.resources)

			if rec.EnableIPFS != tt.wantEnableIPFS {
				t.Errorf("EnableIPFS = %v, want %v", rec.EnableIPFS, tt.wantEnableIPFS)
			}

			if rec.EnableClamAV != tt.wantEnableClamAV {
				t.Errorf("EnableClamAV = %v, want %v", rec.EnableClamAV, tt.wantEnableClamAV)
			}

			if rec.EnableWhisper != tt.wantEnableWhisper {
				t.Errorf("EnableWhisper = %v, want %v", rec.EnableWhisper, tt.wantEnableWhisper)
			}

			if rec.Explanation == "" {
				t.Error("Expected non-empty explanation")
			}
		})
	}
}

func TestRecommendation_ExplanationContent(t *testing.T) {
	minimalRes := &Resources{
		CPUCores: 1,
		RAMBytes: 1 * 1024 * 1024 * 1024,
		RAMGB:    1.0,
	}

	rec := Recommend(minimalRes)

	if !strings.Contains(rec.Explanation, "disabled") && !strings.Contains(rec.Explanation, "Minimal") {
		t.Errorf("Expected explanation to mention disabled services or minimal tier, got: %s", rec.Explanation)
	}
}

func TestRecommendation_TierDetection(t *testing.T) {
	tests := []struct {
		name     string
		ramGB    float64
		cpuCores int
		wantTier string
	}{
		{"minimal", 1.5, 1, "Minimal"},
		{"standard", 4.0, 2, "Standard"},
		{"full", 12.0, 6, "Full"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &Resources{
				CPUCores: tt.cpuCores,
				RAMBytes: int64(tt.ramGB * 1024 * 1024 * 1024),
				RAMGB:    tt.ramGB,
			}

			rec := Recommend(res)

			if !strings.Contains(rec.Explanation, tt.wantTier) {
				t.Errorf("Expected tier %s in explanation, got: %s", tt.wantTier, rec.Explanation)
			}
		})
	}
}
