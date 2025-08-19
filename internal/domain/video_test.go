package domain

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetTargetResolutions(t *testing.T) {
	tests := []struct {
		name               string
		sourceResolution   string
		expectedTargets    []string
		expectedMinTargets int
	}{
		{
			name:             "4K source",
			sourceResolution: "2160p",
			expectedTargets:  []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p"},
		},
		{
			name:             "1080p source",
			sourceResolution: "1080p",
			expectedTargets:  []string{"240p", "360p", "480p", "720p", "1080p"},
		},
		{
			name:             "720p source",
			sourceResolution: "720p",
			expectedTargets:  []string{"240p", "360p", "480p", "720p"},
		},
		{
			name:             "480p source",
			sourceResolution: "480p",
			expectedTargets:  []string{"240p", "360p", "480p"},
		},
		{
			name:             "360p source",
			sourceResolution: "360p",
			expectedTargets:  []string{"240p", "360p"},
		},
		{
			name:             "240p source",
			sourceResolution: "240p",
			expectedTargets:  []string{"240p"},
		},
		{
			name:               "Invalid source resolution",
			sourceResolution:   "invalid",
			expectedTargets:    []string{"720p", "480p", "360p", "240p"},
			expectedMinTargets: 4,
		},
		{
			name:             "8K source",
			sourceResolution: "4320p",
			expectedTargets:  []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := GetTargetResolutions(tt.sourceResolution)

			if tt.expectedMinTargets > 0 {
				assert.GreaterOrEqual(t, len(targets), tt.expectedMinTargets)
			} else {
				assert.Equal(t, len(tt.expectedTargets), len(targets))
			}

			// Check that all expected targets are present
			for _, expected := range tt.expectedTargets {
				assert.Contains(t, targets, expected, "Expected resolution %s not found in targets", expected)
			}

			// Always ensure 240p is included
			assert.Contains(t, targets, "240p", "240p should always be included")

			// Verify no resolution higher than source is included
			if sourceHeight, exists := ResolutionHeights[tt.sourceResolution]; exists {
				for _, target := range targets {
					targetHeight := ResolutionHeights[target]
					assert.LessOrEqual(t, targetHeight, sourceHeight,
						"Target resolution %s (%dp) should not be higher than source %s (%dp)",
						target, targetHeight, tt.sourceResolution, sourceHeight)
				}
			}
		})
	}
}

func TestDetectResolutionFromHeight(t *testing.T) {
	tests := []struct {
		name     string
		height   int
		expected string
	}{
		{"240p exact", 240, "240p"},
		{"360p exact", 360, "360p"},
		{"480p exact", 480, "480p"},
		{"720p exact", 720, "720p"},
		{"1080p exact", 1080, "1080p"},
		{"1440p exact", 1440, "1440p"},
		{"2160p exact", 2160, "2160p"},
		{"4320p exact", 4320, "4320p"},

		// Close matches
		{"Close to 240p", 245, "240p"},
		{"Close to 360p", 355, "360p"},
		{"Close to 720p", 715, "720p"},
		{"Close to 1080p", 1085, "1080p"},

		// Edge cases
		{"Very low", 100, "240p"},
		{"Very high", 10000, "4320p"},
		{"Between 240p and 360p (closer to 240p)", 280, "240p"},
		{"Between 240p and 360p (closer to 360p)", 320, "360p"},
		{"Between 720p and 1080p", 900, "720p"},
		{"Exactly between 720p and 1080p", 900, "720p"}, // Should prefer lower resolution
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectResolutionFromHeight(tt.height)
			assert.Equal(t, tt.expected, result)

			// Verify the result is a valid resolution
			_, exists := ResolutionHeights[result]
			assert.True(t, exists, "Detected resolution %s should be valid", result)
		})
	}
}

func TestIsValidResolution(t *testing.T) {
	validResolutions := []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"}
	invalidResolutions := []string{"", "invalid", "1080", "720", "4K", "HD", "480i"}

	for _, res := range validResolutions {
		t.Run("Valid_"+res, func(t *testing.T) {
			assert.True(t, IsValidResolution(res), "Resolution %s should be valid", res)
		})
	}

	for _, res := range invalidResolutions {
		t.Run("Invalid_"+res, func(t *testing.T) {
			assert.False(t, IsValidResolution(res), "Resolution %s should be invalid", res)
		})
	}
}

func TestResolutionHeights(t *testing.T) {
	expectedHeights := map[string]int{
		"240p":  240,
		"360p":  360,
		"480p":  480,
		"720p":  720,
		"1080p": 1080,
		"1440p": 1440,
		"2160p": 2160,
		"4320p": 4320,
	}

	for resolution, expectedHeight := range expectedHeights {
		t.Run(resolution, func(t *testing.T) {
			height, exists := ResolutionHeights[resolution]
			assert.True(t, exists, "Resolution %s should exist in ResolutionHeights", resolution)
			assert.Equal(t, expectedHeight, height, "Height for %s should be %d", resolution, expectedHeight)
		})
	}

	// Verify all supported resolutions have heights defined
	for _, resolution := range SupportedResolutions {
		t.Run("Supported_"+resolution, func(t *testing.T) {
			_, exists := ResolutionHeights[resolution]
			assert.True(t, exists, "Supported resolution %s should have height defined", resolution)
		})
	}
}

func TestHeightForResolution(t *testing.T) {
	tests := []struct {
		res    string
		height int
		ok     bool
	}{
		{"240p", 240, true},
		{"720p", 720, true},
		{"1080p", 1080, true},
		{"999p", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.res, func(t *testing.T) {
			h, ok := HeightForResolution(tt.res)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.height, h)
			}
		})
	}
}

func TestSupportedResolutions(t *testing.T) {
	expectedResolutions := []string{"240p", "360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"}

	assert.Equal(t, len(expectedResolutions), len(SupportedResolutions), "Number of supported resolutions should match")

	for _, expected := range expectedResolutions {
		assert.Contains(t, SupportedResolutions, expected, "Resolution %s should be in SupportedResolutions", expected)
	}
}

func TestEncodingLogicConsistency(t *testing.T) {
	// Test that encoding logic is consistent with resolution definitions
	for _, sourceRes := range SupportedResolutions {
		t.Run("Source_"+sourceRes, func(t *testing.T) {
			targets := GetTargetResolutions(sourceRes)
			sourceHeight := ResolutionHeights[sourceRes]

			// Verify all targets are valid resolutions
			for _, target := range targets {
				assert.True(t, IsValidResolution(target), "Target %s should be valid", target)
			}

			// Verify source resolution is included in targets
			assert.Contains(t, targets, sourceRes, "Source resolution %s should be included in targets", sourceRes)

			// Verify all targets are <= source resolution
			for _, target := range targets {
				targetHeight := ResolutionHeights[target]
				assert.LessOrEqual(t, targetHeight, sourceHeight,
					"Target %s (%dp) should not exceed source %s (%dp)",
					target, targetHeight, sourceRes, sourceHeight)
			}

			// Verify targets are sorted by height (ascending)
			for i := 1; i < len(targets); i++ {
				prevHeight := ResolutionHeights[targets[i-1]]
				currHeight := ResolutionHeights[targets[i]]
				assert.LessOrEqual(t, prevHeight, currHeight,
					"Targets should be sorted by height: %s (%dp) should not come after %s (%dp)",
					targets[i-1], prevHeight, targets[i], currHeight)
			}
		})
	}
}

func TestDefaultResolution(t *testing.T) {
	assert.Equal(t, "720p", DefaultResolution)
	assert.True(t, IsValidResolution(DefaultResolution), "Default resolution should be valid")

	// Verify default resolution exists in supported resolutions
	assert.Contains(t, SupportedResolutions, DefaultResolution, "Default resolution should be supported")
}

func TestAbsFunction(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{100, 100},
		{-100, 100},
		{math.MinInt, math.MaxInt},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("abs(%d)", tt.input), func(t *testing.T) {
			result := abs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
