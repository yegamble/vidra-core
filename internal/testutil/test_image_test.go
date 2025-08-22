package testutil

import (
	"bytes"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"golang.org/x/image/tiff"
	"github.com/HugoSmits86/nativewebp"
)

func TestCreateTestPNG(t *testing.T) {
	data := CreateTestPNG()
	if len(data) == 0 {
		t.Fatal("PNG data is empty")
	}
	
	// Verify it's valid PNG
	_, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Invalid PNG: %v", err)
	}
}

func TestCreateTestJPEG(t *testing.T) {
	data := CreateTestJPEG()
	if len(data) == 0 {
		t.Fatal("JPEG data is empty")
	}
	
	// Verify it's valid JPEG
	_, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Invalid JPEG: %v", err)
	}
}

func TestCreateTestWebP(t *testing.T) {
	data := CreateTestWebP()
	if len(data) == 0 {
		t.Fatal("WebP data is empty")
	}
	
	// Verify it's valid WebP
	_, err := nativewebp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Invalid WebP: %v", err)
	}
}

func TestCreateTestGIF(t *testing.T) {
	data := CreateTestGIF()
	if len(data) == 0 {
		t.Fatal("GIF data is empty")
	}
	
	// Verify it's valid GIF
	_, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Invalid GIF: %v", err)
	}
}

func TestCreateTestTIFF(t *testing.T) {
	data := CreateTestTIFF()
	if len(data) == 0 {
		t.Fatal("TIFF data is empty")
	}
	
	// Verify it's valid TIFF
	_, err := tiff.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Invalid TIFF: %v", err)
	}
}

func TestCreateTestHEIC(t *testing.T) {
	data := CreateTestHEIC()
	if len(data) == 0 {
		t.Fatal("HEIC data is empty")
	}
	
	// Just verify we have the HEIC file signature
	if len(data) < 12 {
		t.Fatal("HEIC data too short")
	}
	
	// Check for 'ftyp' box at offset 4
	if string(data[4:8]) != "ftyp" {
		t.Fatal("Missing HEIC 'ftyp' box")
	}
	
	// Check for 'heic' brand at offset 8
	if string(data[8:12]) != "heic" {
		t.Fatal("Missing HEIC 'heic' brand")
	}
}