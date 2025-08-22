package testutil

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/tiff"
	"github.com/HugoSmits86/nativewebp"
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

// CreateTestWebP creates a small valid WebP image (16x16 green square)
// Returns the WebP bytes that can be used in tests
func CreateTestWebP() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))

	green := color.RGBA{0, 255, 0, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, green)
		}
	}

	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, img, &nativewebp.Options{}); err != nil {
		panic("failed to encode test WebP: " + err.Error())
	}

	return buf.Bytes()
}

// CreateTestGIF creates a small valid GIF image (16x16 yellow square)
// Returns the GIF bytes that can be used in tests
func CreateTestGIF() []byte {
	img := image.NewPaletted(image.Rect(0, 0, 16, 16), color.Palette{
		color.RGBA{255, 255, 0, 255}, // yellow
		color.RGBA{0, 0, 0, 255},     // black
	})

	// Fill with yellow (palette index 0)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetColorIndex(x, y, 0)
		}
	}

	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		panic("failed to encode test GIF: " + err.Error())
	}

	return buf.Bytes()
}

// CreateTestTIFF creates a small valid TIFF image (16x16 cyan square)
// Returns the TIFF bytes that can be used in tests
func CreateTestTIFF() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))

	cyan := color.RGBA{0, 255, 255, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, cyan)
		}
	}

	var buf bytes.Buffer
	if err := tiff.Encode(&buf, img, nil); err != nil {
		panic("failed to encode test TIFF: " + err.Error())
	}

	return buf.Bytes()
}

// CreateTestHEIC creates mock HEIC data (since Go doesn't have native HEIC encoding)
// For testing purposes, this returns a minimal HEIC file structure
// In real scenarios, HEIC would be handled by external libraries
func CreateTestHEIC() []byte {
	// HEIC file signature and minimal structure for testing
	// This is a simplified mock - real HEIC files are much more complex
	heicHeader := []byte{
		0x00, 0x00, 0x00, 0x20, // box size (32 bytes)
		0x66, 0x74, 0x79, 0x70, // 'ftyp' box
		0x68, 0x65, 0x69, 0x63, // 'heic' brand
		0x00, 0x00, 0x00, 0x00, // minor version
		0x6D, 0x69, 0x66, 0x31, // compatible brands: 'mif1'
		0x68, 0x65, 0x69, 0x63, // 'heic'
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // padding
	}
	
	return heicHeader
}
