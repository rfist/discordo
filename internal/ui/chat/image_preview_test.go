package chat

import (
	"image"
	"image/color"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func TestHalfBlockLines(t *testing.T) {
	t.Run("2x2 image maps to one line of two cells", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 255, A: 255}) // top-left red
		img.Set(1, 0, color.RGBA{G: 255, A: 255}) // top-right green
		img.Set(0, 1, color.RGBA{B: 255, A: 255}) // bottom-left blue
		img.Set(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})

		lines := halfBlockLines(img, 2, 1)
		if len(lines) != 1 {
			t.Fatalf("expected 1 line, got %d", len(lines))
		}
		if len(lines[0]) != 2 {
			t.Fatalf("expected 2 segments, got %d", len(lines[0]))
		}

		for _, segment := range lines[0] {
			if segment.Text != "▀" {
				t.Errorf("expected half-block rune, got %q", segment.Text)
			}
		}

		// tcell/v3's Style has no Decompose(); Style is comparable, so assert
		// against the exact fg/bg the renderer should produce (top pixel red,
		// bottom pixel blue).
		wantStyle := tcell.StyleDefault.
			Foreground(tcell.NewRGBColor(255, 0, 0)).
			Background(tcell.NewRGBColor(0, 0, 255))
		if got := lines[0][0].Style; got != wantStyle {
			t.Errorf("cell 0 style: got %v, want %v", got, wantStyle)
		}
	})

	t.Run("wide image is scaled down to fit columns", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 100, 10))
		lines := halfBlockLines(img, 10, 20)
		if len(lines) == 0 {
			t.Fatal("expected lines")
		}
		if len(lines[0]) > 10 {
			t.Errorf("expected at most 10 columns, got %d", len(lines[0]))
		}
	})

	t.Run("odd pixel height leaves last bottom half unset", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 1, 3))
		lines := halfBlockLines(img, 1, 2)
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d", len(lines))
		}
	})

	t.Run("nil image returns nil", func(t *testing.T) {
		if lines := halfBlockLines(nil, 10, 10); lines != nil {
			t.Errorf("expected nil, got %d lines", len(lines))
		}
	})
}
