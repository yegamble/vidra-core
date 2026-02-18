package config

import (
	"reflect"
	"testing"
)

func TestGetEnvOrDefault(t *testing.T) {
	const key = "TEST_ENV_VAR"
	const defaultValue = "default"

	// Case 1: Variable not set
	t.Setenv(key, "")
	if got := getEnvOrDefault(key, defaultValue); got != defaultValue {
		t.Errorf("getEnvOrDefault(%q, %q) = %q, want %q", key, defaultValue, got, defaultValue)
	}

	// Case 2: Variable set
	const val = "actual"
	t.Setenv(key, val)
	if got := getEnvOrDefault(key, defaultValue); got != val {
		t.Errorf("getEnvOrDefault(%q, %q) = %q, want %q", key, defaultValue, got, val)
	}
}

func TestGetBoolEnv(t *testing.T) {
	const key = "TEST_BOOL_VAR"

	tests := []struct {
		envVal   string
		defVal   bool
		expected bool
	}{
		{"", true, true},
		{"", false, false},
		{"true", false, true},
		{"TRUE", false, true},
		{"1", false, true},
		{"false", true, false},
		{"0", true, false},
		{"anything", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv(key, tt.envVal)
			if got := getBoolEnv(key, tt.defVal); got != tt.expected {
				t.Errorf("getBoolEnv(%q, %v) with ENV=%q = %v, want %v", key, tt.defVal, tt.envVal, got, tt.expected)
			}
		})
	}
}

func TestGetIntEnv(t *testing.T) {
	const key = "TEST_INT_VAR"

	tests := []struct {
		envVal   string
		defVal   int
		expected int
	}{
		{"", 42, 42},
		{"123", 42, 123},
		{"invalid", 42, 42},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv(key, tt.envVal)
			if got := getIntEnv(key, tt.defVal); got != tt.expected {
				t.Errorf("getIntEnv(%q, %v) with ENV=%q = %v, want %v", key, tt.defVal, tt.envVal, got, tt.expected)
			}
		})
	}
}

func TestGetInt64Env(t *testing.T) {
	const key = "TEST_INT64_VAR"

	tests := []struct {
		envVal   string
		defVal   int64
		expected int64
	}{
		{"", 42, 42},
		{"1234567890", 42, 1234567890},
		{"invalid", 42, 42},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv(key, tt.envVal)
			if got := getInt64Env(key, tt.defVal); got != tt.expected {
				t.Errorf("getInt64Env(%q, %v) with ENV=%q = %v, want %v", key, tt.defVal, tt.envVal, got, tt.expected)
			}
		})
	}
}

func TestGetFloat64Env(t *testing.T) {
	const key = "TEST_FLOAT64_VAR"

	tests := []struct {
		envVal   string
		defVal   float64
		expected float64
	}{
		{"", 3.14, 3.14},
		{"2.718", 3.14, 2.718},
		{"invalid", 3.14, 3.14},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv(key, tt.envVal)
			if got := getFloat64Env(key, tt.defVal); got != tt.expected {
				t.Errorf("getFloat64Env(%q, %v) with ENV=%q = %v, want %v", key, tt.defVal, tt.envVal, got, tt.expected)
			}
		})
	}
}

func TestGetStringSliceEnv(t *testing.T) {
	const key = "TEST_SLICE_VAR"
	defaultVal := []string{"a", "b"}

	tests := []struct {
		envVal   string
		expected []string
	}{
		{"", defaultVal},
		{"x,y,z", []string{"x", "y", "z"}},
		{"one", []string{"one"}},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv(key, tt.envVal)
			got := getStringSliceEnv(key, defaultVal)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getStringSliceEnv(%q, %v) with ENV=%q = %v, want %v", key, defaultVal, tt.envVal, got, tt.expected)
			}
		})
	}
}
