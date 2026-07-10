package chat

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strconv"
)

// kittyChunkSize is the maximum base64 payload bytes per kitty escape chunk, as
// mandated by the graphics protocol.
const kittyChunkSize = 4096

// kittyPlacement builds a kitty graphics "transmit and display" escape sequence
// that renders img scaled to cols×rows terminal cells, tagged with id so a
// later kittyDelete can remove it. The image is PNG-encoded and its base64
// payload is split into protocol-legal chunks.
//
// q=2 suppresses the terminal's success/error acknowledgements so they are not
// injected into the input stream (tcell would otherwise read them as key
// events). The caller is responsible for positioning the cursor at the target
// cell before writing these bytes.
func kittyPlacement(id uint32, img image.Image, cols, rows int) ([]byte, error) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}

	payload := make([]byte, base64.StdEncoding.EncodedLen(pngBuf.Len()))
	base64.StdEncoding.Encode(payload, pngBuf.Bytes())

	var out bytes.Buffer
	for i := 0; i < len(payload); i += kittyChunkSize {
		end := min(i+kittyChunkSize, len(payload))
		chunk := payload[i:end]
		last := end == len(payload)

		out.WriteString("\x1b_G")
		if i == 0 {
			// First (or only) chunk carries the full control block.
			// C=1 keeps the cursor put (kitty otherwise advances it past the
			// image, which can scroll the screen when placing near the bottom).
			fmt.Fprintf(&out, "a=T,f=100,q=2,C=1,i=%d,c=%d,r=%d,m=%d", id, cols, rows, boolToInt(!last))
		} else {
			fmt.Fprintf(&out, "m=%d", boolToInt(!last))
		}
		out.WriteByte(';')
		out.Write(chunk)
		out.WriteString("\x1b\\")
	}
	return out.Bytes(), nil
}

// kittyDelete builds a sequence that removes the image with the given id along
// with its placements and frees the associated data (a=d, d=I).
func kittyDelete(id uint32) []byte {
	return []byte("\x1b_Ga=d,d=I,i=" + strconv.FormatUint(uint64(id), 10) + "\x1b\\")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fitCells picks the cell dimensions to scale an imgW×imgH image into, bounded
// by maxCols×maxRows, preserving aspect ratio. Terminal cells are roughly twice
// as tall as they are wide, so a pixel row occupies about half the vertical
// space of a pixel column; we approximate the cell aspect as 1:2 (exact pixel
// metrics from the terminal would refine this). Result is always >= 1 in each
// axis and never exceeds the bounds.
func fitCells(imgW, imgH, maxCols, maxRows int) (cols, rows int) {
	if imgW <= 0 || imgH <= 0 || maxCols <= 0 || maxRows <= 0 {
		return max(maxCols, 1), max(maxRows, 1)
	}

	// Vertical is measured in half-cell units to account for the 1:2 aspect.
	scale := min(
		float64(maxCols)/float64(imgW),
		float64(maxRows)/(float64(imgH)/2),
	)
	cols = min(max(int(float64(imgW)*scale), 1), maxCols)
	rows = min(max(int(float64(imgH)/2*scale), 1), maxRows)
	return cols, rows
}
