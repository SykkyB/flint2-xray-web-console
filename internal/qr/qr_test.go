package qr

import (
	"bytes"
	"image/png"
	"testing"
)

func TestPNG_Decodable(t *testing.T) {
	out, err := PNG("vless://test@example.com:443?x=1", 256, Medium)
	if err != nil {
		t.Fatalf("PNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Errorf("size: got %v", img.Bounds())
	}
}

func TestPNG_ClampsTinySize(t *testing.T) {
	out, err := PNG("hello", 0, Medium)
	if err != nil {
		t.Fatalf("PNG: %v", err)
	}
	img, _ := png.Decode(bytes.NewReader(out))
	if img == nil || img.Bounds().Dx() < 64 {
		t.Errorf("expected clamped size, got %v", img)
	}
}

func TestPNG_EmptyPayload(t *testing.T) {
	if _, err := PNG("", 256, Medium); err == nil {
		t.Errorf("expected error for empty payload")
	}
}
