package testutil

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// CreateTestPNG creates a small valid PNG image (16x16 red square)
// Returns the PNG bytes that can be used in tests
func CreateTestPNG() []byte {
	// Create a 16x16 image
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))

	// Fill with red color
	red := color.RGBA{255, 0, 0, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, red)
		}
	}

	// Encode to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("failed to encode test PNG: " + err.Error())
	}

	return buf.Bytes()
}

// CreateTestJPEG creates a small valid JPEG image (16x16 blue square)
// Returns the JPEG bytes that can be used in tests
func CreateTestJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))

	blue := color.RGBA{0, 0, 255, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, blue)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic("failed to encode test JPEG: " + err.Error())
	}

	return buf.Bytes()
}
