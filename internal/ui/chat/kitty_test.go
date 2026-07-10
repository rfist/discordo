package chat

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"strings"
	"testing"
)

func solidImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	return img
}

func TestKittyPlacementSingleChunk(t *testing.T) {
	seq, err := kittyPlacement(7, solidImage(2, 2), 12, 6)
	if err != nil {
		t.Fatal(err)
	}
	s := string(seq)

	if !strings.HasPrefix(s, "\x1b_G") {
		t.Errorf("missing APC graphics prefix")
	}
	if !strings.HasSuffix(s, "\x1b\\") {
		t.Errorf("missing string terminator")
	}
	for _, want := range []string{"a=T", "f=100", "q=2", "i=7", "c=12", "r=6", "m=0"} {
		if !strings.Contains(s, want) {
			t.Errorf("control block missing %q", want)
		}
	}

	// A small image fits in one chunk, so exactly one escape sequence.
	if n := strings.Count(s, "\x1b_G"); n != 1 {
		t.Errorf("expected 1 chunk, got %d", n)
	}

	// Payload must be valid base64 of a PNG (starts with the PNG signature).
	semi := strings.IndexByte(s, ';')
	payload := strings.TrimSuffix(s[semi+1:], "\x1b\\")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload not valid base64: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("payload is not a PNG")
	}
}

func TestKittyPlacementChunking(t *testing.T) {
	// A large, noisy image forces a multi-chunk payload.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			img.Set(x, y, color.RGBA{R: uint8(x * y), G: uint8(x + y), B: uint8(x ^ y), A: 255})
		}
	}

	seq, err := kittyPlacement(1, img, 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	s := string(seq)

	chunks := strings.Count(s, "\x1b_G")
	if chunks < 2 {
		t.Fatalf("expected multiple chunks, got %d", chunks)
	}
	// Continuation chunks are marked m=1; the final chunk m=0.
	if strings.Count(s, "m=1") != chunks-1 {
		t.Errorf("expected %d continuation markers, got %d", chunks-1, strings.Count(s, "m=1"))
	}
	if strings.Count(s, "m=0") != 1 {
		t.Errorf("expected exactly one terminal marker m=0")
	}
	// Only the first chunk carries the control block.
	if strings.Count(s, "a=T") != 1 {
		t.Errorf("control block should appear once, got %d", strings.Count(s, "a=T"))
	}
}

func TestKittyDelete(t *testing.T) {
	if got, want := string(kittyDelete(42)), "\x1b_Ga=d,d=I,i=42\x1b\\"; got != want {
		t.Errorf("kittyDelete = %q, want %q", got, want)
	}
}
