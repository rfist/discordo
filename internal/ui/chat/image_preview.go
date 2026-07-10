package chat

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/gdamore/tcell/v3"
	"golang.org/x/image/draw"
)

// maxPreviewBytes caps the attachment download so a huge file cannot stall the
// preview command goroutine indefinitely.
const maxPreviewBytes = 20 << 20

type imagePreviewLoadedMsg struct {
	messageID discord.MessageID
	image     image.Image
}

// previewOut is where kitty graphics escape sequences are written. It is the
// controlling terminal (os.Stdout); tcell renders to the same terminal, so the
// sequences composite over the drawn cells. Overridable in tests.
var previewOut io.Writer = os.Stdout

// imagePreview renders the selected message's first image attachment. In the
// default half-block mode it emits unicode "▀" cells (works in any terminal).
// In kitty mode it instead transmits the real image to the terminal via the
// graphics protocol, drawn over the (blank) pane interior; half-block cells are
// not used in that mode.
type imagePreview struct {
	*tview.TextView
	cfg  *config.Config
	chat *Model

	// protocol is the rendering path resolved once at startup from
	// cfg.ImagePreview.Protocol and the terminal environment.
	protocol graphicsProtocol

	// messageID of the image currently displayed or being loaded; stale
	// loads are dropped when the selection has moved on.
	messageID discord.MessageID

	// kitty-mode draw state (unused in half-block mode).
	img      image.Image // decoded image to draw, nil when none
	dirty    bool        // image or geometry changed; re-place on next View
	placedID uint32      // kitty image id currently on screen, 0 if none
	nextID   uint32      // monotonic id source for new placements
	lastRect [4]int      // last InnerRect the image was placed at {x,y,w,h}
}

// imagePreview is added to a flex as a passive display pane.
var _ tview.Model = (*imagePreview)(nil)

func newImagePreview(cfg *config.Config, chat *Model) *imagePreview {
	ip := &imagePreview{
		TextView: tview.NewTextView(),
		cfg:      cfg,
		chat:     chat,
		protocol: resolveGraphicsProtocol(cfg.ImagePreview.Protocol, os.Getenv),
	}
	ip.Box = ui.ConfigureBox(ip.Box, &cfg.Theme)
	ip.SetTitle("Preview")
	if ip.protocol == graphicsKitty {
		slog.Debug("image preview: using kitty graphics protocol")
	}
	ip.clear()
	return ip
}

func firstImageAttachment(message discord.Message) *discord.Attachment {
	for i, attachment := range message.Attachments {
		if strings.HasPrefix(attachment.ContentType, "image/") {
			return &message.Attachments[i]
		}
	}
	return nil
}

// loadCmd starts fetching the image for message, or clears the pane when the
// message has no image attachment.
func (ip *imagePreview) loadCmd(message discord.Message) tview.Cmd {
	attachment := firstImageAttachment(message)
	if attachment == nil {
		ip.clear()
		return nil
	}

	if ip.messageID == message.ID {
		return nil
	}
	ip.messageID = message.ID
	if ip.protocol != graphicsKitty {
		ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("Loading...", tcell.StyleDefault.Dim(true)))})
	}

	messageID := message.ID
	url := attachment.URL
	return func() tview.Msg {
		img, err := fetchImage(url)
		if err != nil {
			slog.Error("failed to fetch preview image", "err", err, "url", url)
			return imagePreviewLoadedMsg{messageID: messageID}
		}
		return imagePreviewLoadedMsg{messageID: messageID, image: img}
	}
}

func (ip *imagePreview) show(msg imagePreviewLoadedMsg) {
	// The selection may have moved while the image was downloading.
	if msg.messageID != ip.messageID {
		return
	}

	if msg.image == nil {
		ip.img = nil
		ip.dirty = true
		ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("Failed to load image", tcell.StyleDefault.Dim(true)))})
		return
	}

	if ip.protocol == graphicsKitty {
		// Blank interior; the real image is composited over it in View.
		ip.img = msg.image
		ip.dirty = true
		ip.SetLines(nil)
		return
	}

	cols, rows := ip.paneSize()
	ip.SetLines(halfBlockLines(msg.image, cols, rows))
}

func (ip *imagePreview) clear() {
	ip.messageID = 0
	ip.img = nil
	ip.dirty = true
	ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("No image", tcell.StyleDefault.Dim(true)))})
}

// View draws the pane box, then in kitty mode composites the real image over
// the blank interior. The half-block path is unaffected (returns immediately).
func (ip *imagePreview) View(screen tcell.Screen) {
	ip.TextView.View(screen)
	if ip.protocol != graphicsKitty {
		return
	}

	x, y, w, h := ip.InnerRect()
	if ip.img == nil {
		ip.deleteKitty()
		return
	}
	if w <= 0 || h <= 0 {
		return
	}

	// Re-transmit only when the image or geometry changed. Between changes the
	// terminal keeps the placed image, and tcell does not touch these cells, so
	// nothing needs re-emitting (avoids per-frame flicker and overhead).
	rect := [4]int{x, y, w, h}
	if !ip.dirty && ip.placedID != 0 && rect == ip.lastRect {
		return
	}
	ip.lastRect = rect
	ip.dirty = false
	ip.placeKitty(x, y, w, h)
}

// placeKitty transmits ip.img into the w×h cell rectangle at (x,y), deleting any
// previously placed image first. Written straight to the terminal (tcell can't
// see graphics); the cursor is saved/moved/restored so tcell's own cursor
// bookkeeping is undisturbed.
func (ip *imagePreview) placeKitty(x, y, w, h int) {
	cols, rows := fitCells(ip.img.Bounds().Dx(), ip.img.Bounds().Dy(), w, h)

	ip.nextID++
	id := ip.nextID
	seq, err := kittyPlacement(id, ip.img, cols, rows)
	if err != nil {
		slog.Error("failed to encode kitty image", "err", err)
		return
	}

	var buf bytes.Buffer
	if ip.placedID != 0 {
		buf.Write(kittyDelete(ip.placedID))
	}
	buf.WriteString("\x1b7")                   // save cursor position
	fmt.Fprintf(&buf, "\x1b[%d;%dH", y+1, x+1) // move to pane origin (1-based)
	buf.Write(seq)
	buf.WriteString("\x1b8") // restore cursor position

	if _, err := previewOut.Write(buf.Bytes()); err != nil {
		slog.Error("failed to write kitty image", "err", err)
		return
	}
	ip.placedID = id
}

// deleteKitty removes the on-screen image, if any. Safe to call when nothing is
// placed and outside the draw loop (e.g. when the pane is toggled off).
func (ip *imagePreview) deleteKitty() {
	if ip.placedID == 0 {
		return
	}
	if _, err := previewOut.Write(kittyDelete(ip.placedID)); err != nil {
		slog.Error("failed to delete kitty image", "err", err)
	}
	ip.placedID = 0
	ip.lastRect = [4]int{}
}

func (ip *imagePreview) paneSize() (int, int) {
	_, _, width, height := ip.InnerRect()
	if width <= 0 || height <= 0 {
		// Not laid out yet (pane was just toggled); a reasonable default that
		// gets corrected on the next selection change.
		return 40, 20
	}
	return width, height
}

func fetchImage(url string) (image.Image, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	img, _, err := image.Decode(io.LimitReader(resp.Body, maxPreviewBytes))
	return img, err
}

// halfBlockLines scales img to fit maxCols x maxRows cells and renders it as
// "▀" runes: foreground is the top pixel, background the bottom pixel, giving
// two vertical pixels per cell.
func halfBlockLines(img image.Image, maxCols, maxRows int) []tview.Line {
	if img == nil || maxCols <= 0 || maxRows <= 0 {
		return nil
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return nil
	}

	boxWidth, boxHeight := maxCols, maxRows*2
	ratio := min(float64(boxWidth)/float64(width), float64(boxHeight)/float64(height))
	dstWidth := max(int(float64(width)*ratio), 1)
	dstHeight := max(int(float64(height)*ratio), 1)

	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	if dstWidth == width && dstHeight == height {
		draw.Copy(dst, image.Point{}, img, bounds, draw.Over, nil)
	} else {
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	}

	rows := (dstHeight + 1) / 2
	lines := make([]tview.Line, 0, rows)
	for row := range rows {
		line := make(tview.Line, 0, dstWidth)
		for x := range dstWidth {
			style := tcell.StyleDefault.Foreground(rgbaCellColor(dst, x, row*2))
			if row*2+1 < dstHeight {
				style = style.Background(rgbaCellColor(dst, x, row*2+1))
			}
			line = append(line, tview.NewSegment("▀", style))
		}
		lines = append(lines, line)
	}
	return lines
}

func rgbaCellColor(img *image.RGBA, x, y int) tcell.Color {
	c := img.RGBAAt(x, y)
	return tcell.NewRGBColor(int32(c.R), int32(c.G), int32(c.B))
}
