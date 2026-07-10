package chat

import (
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
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

// imagePreview renders the selected message's first image attachment as
// unicode half-blocks. Pure cells, so it works in any terminal and never
// fights the draw loop.
type imagePreview struct {
	*tview.TextView
	cfg  *config.Config
	chat *Model

	// messageID of the image currently displayed or being loaded; stale
	// loads are dropped when the selection has moved on.
	messageID discord.MessageID
}

// imagePreview is added to a flex as a passive display pane.
var _ tview.Model = (*imagePreview)(nil)

func newImagePreview(cfg *config.Config, chat *Model) *imagePreview {
	ip := &imagePreview{
		TextView: tview.NewTextView(),
		cfg:      cfg,
		chat:     chat,
	}
	ip.Box = ui.ConfigureBox(ip.Box, &cfg.Theme)
	ip.SetTitle("Preview")
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
	ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("Loading...", tcell.StyleDefault.Dim(true)))})

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
		ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("Failed to load image", tcell.StyleDefault.Dim(true)))})
		return
	}

	cols, rows := ip.paneSize()
	ip.SetLines(halfBlockLines(msg.image, cols, rows))
}

func (ip *imagePreview) clear() {
	ip.messageID = 0
	ip.SetLines([]tview.Line{tview.NewLine(tview.NewSegment("No image", tcell.StyleDefault.Dim(true)))})
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
