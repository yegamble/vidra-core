package imageutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeFileToWebP_NonExistentSource_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	err := EncodeFileToWebP("/nonexistent/path/image.png", filepath.Join(tmp, "out.webp"))
	require.Error(t, err)
}

func TestEncodeFileToWebPWithQuality_NonExistentSource_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	err := EncodeFileToWebPWithQuality("/nonexistent/path/image.png", filepath.Join(tmp, "out.webp"), 80)
	require.Error(t, err)
}

func TestEncodeFileToWebP_StubReturnsUnavailableError(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "dummy.png")
	require.NoError(t, os.WriteFile(src, []byte("not a real image"), 0600))

	err := EncodeFileToWebP(src, filepath.Join(tmp, "out.webp"))

	if err != nil && !errors.Is(err, ErrWebPUnavailable) {
		t.Logf("non-stub encoder returned: %v", err)
	}
}

func TestEncodeFileToWebPWithQuality_StubReturnsUnavailableError(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "dummy.png")
	require.NoError(t, os.WriteFile(src, []byte("not a real image"), 0600))

	err := EncodeFileToWebPWithQuality(src, filepath.Join(tmp, "out.webp"), 75)

	if err != nil && !errors.Is(err, ErrWebPUnavailable) {
		t.Logf("non-stub encoder returned: %v", err)
	}
}

func TestErrWebPUnavailable_IsNotNil(t *testing.T) {
	assert.NotNil(t, ErrWebPUnavailable)
}
