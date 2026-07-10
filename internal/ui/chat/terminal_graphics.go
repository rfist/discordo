package chat

import "strings"

// graphicsProtocol identifies how the preview pane renders an image.
type graphicsProtocol int

const (
	// graphicsHalfBlock renders with unicode "▀" cells; works everywhere.
	graphicsHalfBlock graphicsProtocol = iota
	// graphicsKitty transmits the image via the kitty graphics protocol.
	graphicsKitty
)

// inTmux reports whether we are under tmux/screen, which do not forward or
// composite graphics escape sequences (absent passthrough, which we do not
// attempt). Images written to the pty never reach the host terminal there.
func inTmux(env func(string) string) bool {
	return env("TMUX") != "" ||
		strings.HasPrefix(env("TERM"), "screen") ||
		strings.HasPrefix(env("TERM"), "tmux")
}

// inHerdr reports whether we are inside the herdr multiplexer. Unlike tmux,
// herdr composites kitty graphics itself, but only via Unicode-placeholder
// placement (which the preview uses in kitty mode) and only when its own host
// terminal supports graphics.
func inHerdr(env func(string) string) bool {
	return env("HERDR_ENV") != "" || env("HERDR_PANE_ID") != ""
}

// kittyCapable reports whether "auto" should select the kitty protocol.
// Detection is environment-only (no terminal query). It is deliberately
// conservative inside multiplexers: tmux/screen cannot show graphics at all,
// and inside herdr the host terminal is unknown from here, so auto stays on
// half-blocks (an explicit protocol = "kitty" still enables it in herdr).
func kittyCapable(env func(string) string) bool {
	if inTmux(env) || inHerdr(env) {
		return false
	}

	if env("KITTY_WINDOW_ID") != "" {
		return true
	}
	if strings.Contains(env("TERM"), "kitty") {
		return true
	}
	// Ghostty and WezTerm implement the kitty protocol and set these.
	if env("GHOSTTY_RESOURCES_DIR") != "" || env("GHOSTTY_BIN_DIR") != "" {
		return true
	}
	if env("WEZTERM_EXECUTABLE") != "" || env("WEZTERM_PANE") != "" {
		return true
	}
	switch env("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return true
	}
	return false
}

// resolveGraphicsProtocol maps the configured preference and the detected
// terminal capability to the protocol the preview should actually use. Unknown
// or empty preferences fall back to half-blocks.
func resolveGraphicsProtocol(pref string, env func(string) string) graphicsProtocol {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case "kitty":
		// Explicit opt-in: honor it even where detection is unsure, including
		// inside herdr (it composites Unicode-placeholder images). Only tmux/
		// screen force half-blocks, since they cannot show graphics at all.
		if inTmux(env) {
			return graphicsHalfBlock
		}
		return graphicsKitty
	case "auto":
		if kittyCapable(env) {
			return graphicsKitty
		}
		return graphicsHalfBlock
	default: // "halfblock", "", or anything unrecognized
		return graphicsHalfBlock
	}
}
