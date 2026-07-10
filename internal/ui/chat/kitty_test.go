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

func TestKittyVirtualTransmitSingleChunk(t *testing.T) {
	seq, err := kittyVirtualTransmit(7, solidImage(2, 2), 12, 6)
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
	// U=1 (virtual placement) is what makes this multiplexer-safe.
	for _, want := range []string{"a=T", "U=1", "f=100", "q=2", "i=7", "c=12", "r=6", "m=0"} {
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

func TestKittyVirtualTransmitChunking(t *testing.T) {
	// A large, noisy image forces a multi-chunk payload.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			img.Set(x, y, color.RGBA{R: uint8(x * y), G: uint8(x + y), B: uint8(x ^ y), A: 255})
		}
	}

	seq, err := kittyVirtualTransmit(1, img, 40, 20)
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

func TestKittyIDColor(t *testing.T) {
	cases := []struct {
		id            uint32
		wr, wg, wb    int32
	}{
		{1, 0, 0, 1},
		{0x010203, 1, 2, 3},
		{0xFFFFFF, 255, 255, 255},
	}
	for _, tc := range cases {
		r, g, b := kittyIDColor(tc.id)
		if r != tc.wr || g != tc.wg || b != tc.wb {
			t.Errorf("kittyIDColor(%#x) = (%d,%d,%d), want (%d,%d,%d)", tc.id, r, g, b, tc.wr, tc.wg, tc.wb)
		}
	}
}

func TestKittyDiacriticsTable(t *testing.T) {
	if len(kittyDiacritics) != 297 {
		t.Fatalf("expected 297 diacritics, got %d", len(kittyDiacritics))
	}
	if kittyDiacritics[0] != 0x0305 {
		t.Errorf("first diacritic = %#x, want 0x0305", kittyDiacritics[0])
	}
	if kittyDiacritics[len(kittyDiacritics)-1] != 0x1D244 {
		t.Errorf("last diacritic = %#x, want 0x1D244", kittyDiacritics[len(kittyDiacritics)-1])
	}
	seen := make(map[rune]bool, len(kittyDiacritics))
	for i, r := range kittyDiacritics {
		if seen[r] {
			t.Errorf("duplicate diacritic %#x at index %d", r, i)
		}
		seen[r] = true
	}
}

func TestKittyDelete(t *testing.T) {
	if got, want := string(kittyDelete(42)), "\x1b_Ga=d,d=I,i=42\x1b\\"; got != want {
		t.Errorf("kittyDelete = %q, want %q", got, want)
	}
}

func TestFitCells(t *testing.T) {
	cases := []struct {
		name                             string
		imgW, imgH, maxCols, maxRows     int
		wantCols, wantRows               int
	}{
		// Square image in a wide/short box is limited by rows (×2 for aspect).
		{"square limited by rows", 100, 100, 80, 10, 20, 10},
		// Wide image limited by cols (aspect 4:1, cell aspect ~1:2 → 5 rows).
		{"wide limited by cols", 200, 50, 40, 40, 40, 5},
		// Never exceed the bounds.
		{"clamped to bounds", 1000, 1000, 30, 15, 30, 15},
		// Degenerate inputs fall back to the box.
		{"zero image", 0, 0, 12, 6, 12, 6},
		// Always at least 1 cell.
		{"tiny", 1, 1000, 40, 20, 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := fitCells(tc.imgW, tc.imgH, tc.maxCols, tc.maxRows)
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("fitCells(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					tc.imgW, tc.imgH, tc.maxCols, tc.maxRows, cols, rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}
