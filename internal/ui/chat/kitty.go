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
			fmt.Fprintf(&out, "a=T,f=100,q=2,i=%d,c=%d,r=%d,m=%d", id, cols, rows, boolToInt(!last))
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
