// Package qr renders vless:// (or any) strings to PNG QR codes.
//
// This is a one-function wrapper over github.com/skip2/go-qrcode; it
// exists so the caller doesn't depend on that library directly and we
// can swap implementations later if needed.
package qr

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// Level is the QR error-correction level. Higher levels tolerate more
// damage but produce denser codes.
type Level int

const (
	Low     Level = iota // ~7%
	Medium               // ~15%
	High                 // ~25%
	Highest              // ~30%
)

func (l Level) toLibrary() qrcode.RecoveryLevel {
	switch l {
	case Low:
		return qrcode.Low
	case High:
		return qrcode.High
	case Highest:
		return qrcode.Highest
	default:
		return qrcode.Medium
	}
}

// PNG renders payload as a square PNG of the given pixel size with the
// given error-correction level. Size is clamped to a sensible minimum so
// a zero value still yields a scannable code.
func PNG(payload string, size int, level Level) ([]byte, error) {
	if payload == "" {
		return nil, fmt.Errorf("payload is empty")
	}
	if size < 64 {
		size = 256
	}
	b, err := qrcode.Encode(payload, level.toLibrary(), size)
	if err != nil {
		return nil, fmt.Errorf("qr encode: %w", err)
	}
	return b, nil
}
