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

// insideMultiplexer reports whether we are running under a terminal multiplexer
// that does not forward graphics escape sequences to the host terminal. Such a
// multiplexer sits between the app and the terminal, so kitty images written to
// the pty never reach it (they render blank), making half-blocks the safe
// choice. Covers tmux/screen and herdr; extend as needed.
func insideMultiplexer(env func(string) string) bool {
	return env("TMUX") != "" ||
		env("HERDR_ENV") != "" ||
		env("HERDR_PANE_ID") != "" ||
		strings.HasPrefix(env("TERM"), "screen") ||
		strings.HasPrefix(env("TERM"), "tmux")
}

// kittyCapable reports whether the terminal described by env supports the kitty
// graphics protocol. Detection is environment-only (no terminal query), so it
// is synchronous and side-effect free; the terminals that implement kitty
// graphics all advertise themselves through these variables.
//
// Multiplexers are treated as incapable: kitty graphics only survive inside a
// multiplexer with explicit passthrough and correct pane tracking, which we do
// not attempt.
func kittyCapable(env func(string) string) bool {
	if insideMultiplexer(env) {
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
		// Explicit opt-in: honor it even if terminal detection is unsure, but
		// never inside a multiplexer where the sequences cannot reach the
		// terminal (it would just render a blank pane).
		if insideMultiplexer(env) {
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
