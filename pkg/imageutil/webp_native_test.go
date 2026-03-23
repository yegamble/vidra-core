//go:build webp

package imageutil

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDummyPNG creates a valid PNG image at the given path
func createDummyPNG(t *testing.T, path string) {
	width, height := 100, 100
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Fill with a color to ensure it's not empty
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	err = png.Encode(f, img)
	require.NoError(t, err)
}

func TestEncodeFileToWebP_Success(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "test.png")
	dstPath := filepath.Join(tmpDir, "test.webp")

	createDummyPNG(t, srcPath)

	err := EncodeFileToWebP(srcPath, dstPath)
	require.NoError(t, err)

	// Verify file exists and has size > 0
	info, err := os.Stat(dstPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	// Verify magic bytes "RIFF" and "WEBP"
	data, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	require.Greater(t, len(data), 12)
	assert.Equal(t, "RIFF", string(data[0:4]))
	assert.Equal(t, "WEBP", string(data[8:12]))
}

func TestEncodeFileToWebPWithQuality_Success(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "test_q.png")
	dstPath := filepath.Join(tmpDir, "test_q.webp")

	createDummyPNG(t, srcPath)

	// Test with a reasonable quality value
	err := EncodeFileToWebPWithQuality(srcPath, dstPath, 75)
	require.NoError(t, err)

	info, err := os.Stat(dstPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	data, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	require.Greater(t, len(data), 12)
	assert.Equal(t, "RIFF", string(data[0:4]))
	assert.Equal(t, "WEBP", string(data[8:12]))
}

func TestEncodeFileToWebP_InvalidSource(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "invalid.png")
	dstPath := filepath.Join(tmpDir, "invalid.webp")

	// Create a file that is not an image
	err := os.WriteFile(srcPath, []byte("this is not a valid image file"), 0644)
	require.NoError(t, err)

	err = EncodeFileToWebP(srcPath, dstPath)
	assert.Error(t, err)
	// Expect decode error, not ErrWebPUnavailable
	assert.NotEqual(t, ErrWebPUnavailable, err)
}

func TestEncodeFileToWebP_MissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "missing.png")
	dstPath := filepath.Join(tmpDir, "missing.webp")

	err := EncodeFileToWebP(srcPath, dstPath)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}
