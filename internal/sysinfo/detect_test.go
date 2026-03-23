package sysinfo

import (
	"testing"
)

func TestDetectResources(t *testing.T) {
	resources := DetectResources()

	if resources.CPUCores <= 0 {
		t.Errorf("Expected positive CPU cores, got %d", resources.CPUCores)
	}

	if resources.RAMBytes <= 0 {
		t.Errorf("Expected positive RAM bytes, got %d", resources.RAMBytes)
	}

	if resources.RAMGB < 0.1 {
		t.Errorf("Expected at least 0.1 GB RAM, got %.2f", resources.RAMGB)
	}
}

func TestDetectCPU(t *testing.T) {
	cores := DetectCPU()

	if cores <= 0 {
		t.Errorf("Expected positive CPU cores, got %d", cores)
	}

	if cores < 1 {
		t.Errorf("Expected at least 1 CPU core, got %d", cores)
	}
}

func TestDetectRAM(t *testing.T) {
	ramBytes := DetectRAM()

	if ramBytes <= 0 {
		t.Errorf("Expected positive RAM bytes, got %d", ramBytes)
	}

	minRAM := int64(100 * 1024 * 1024)
	if ramBytes < minRAM {
		t.Errorf("Expected at least %d bytes RAM, got %d", minRAM, ramBytes)
	}
}

func TestResources_RAMGB(t *testing.T) {
	tests := []struct {
		name       string
		ramBytes   int64
		expectedGB float64
	}{
		{
			name:       "1 GB",
			ramBytes:   1024 * 1024 * 1024,
			expectedGB: 1.0,
		},
		{
			name:       "2 GB",
			ramBytes:   2 * 1024 * 1024 * 1024,
			expectedGB: 2.0,
		},
		{
			name:       "512 MB",
			ramBytes:   512 * 1024 * 1024,
			expectedGB: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := &Resources{
				CPUCores: 1,
				RAMBytes: tt.ramBytes,
			}
			resources.RAMGB = float64(resources.RAMBytes) / (1024 * 1024 * 1024)

			diff := resources.RAMGB - tt.expectedGB
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.01 {
				t.Errorf("Expected %.2f GB, got %.2f GB", tt.expectedGB, resources.RAMGB)
			}
		})
	}
}
