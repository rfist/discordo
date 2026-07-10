package chat

import "testing"

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestKittyCapable(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"kitty via window id", map[string]string{"KITTY_WINDOW_ID": "1"}, true},
		{"kitty via TERM", map[string]string{"TERM": "xterm-kitty"}, true},
		{"ghostty resources", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, true},
		{"wezterm pane", map[string]string{"WEZTERM_PANE": "0"}, true},
		{"term_program ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, true},
		{"plain xterm", map[string]string{"TERM": "xterm-256color"}, false},
		{"kitty inside tmux is disabled", map[string]string{"KITTY_WINDOW_ID": "1", "TMUX": "/tmp/tmux"}, false},
		{"kitty inside herdr is disabled", map[string]string{"KITTY_WINDOW_ID": "1", "HERDR_ENV": "1"}, false},
		{"herdr pane id also disables", map[string]string{"TERM": "xterm-kitty", "HERDR_PANE_ID": "w6:p2"}, false},
		{"screen TERM disabled", map[string]string{"TERM": "screen-256color", "KITTY_WINDOW_ID": "1"}, false},
		{"empty env", map[string]string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kittyCapable(envFrom(tc.env)); got != tc.want {
				t.Errorf("kittyCapable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveGraphicsProtocol(t *testing.T) {
	cases := []struct {
		name string
		pref string
		env  map[string]string
		want graphicsProtocol
	}{
		{"halfblock forced", "halfblock", map[string]string{"KITTY_WINDOW_ID": "1"}, graphicsHalfBlock},
		{"empty pref defaults to halfblock", "", map[string]string{"KITTY_WINDOW_ID": "1"}, graphicsHalfBlock},
		{"unknown pref defaults to halfblock", "banana", map[string]string{}, graphicsHalfBlock},
		{"kitty forced without detection", "kitty", map[string]string{}, graphicsKitty},
		{"kitty forced but tmux downgrades", "kitty", map[string]string{"TMUX": "/tmp/tmux"}, graphicsHalfBlock},
		{"kitty forced but herdr downgrades", "kitty", map[string]string{"HERDR_ENV": "1"}, graphicsHalfBlock},
		{"auto with capable terminal", "auto", map[string]string{"TERM": "xterm-kitty"}, graphicsKitty},
		{"auto with incapable terminal", "auto", map[string]string{"TERM": "xterm-256color"}, graphicsHalfBlock},
		{"auto inside herdr on ghostty falls back", "auto", map[string]string{"TERM_PROGRAM": "ghostty", "HERDR_ENV": "1"}, graphicsHalfBlock},
		{"case-insensitive", "Auto", map[string]string{"TERM": "xterm-kitty"}, graphicsKitty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveGraphicsProtocol(tc.pref, envFrom(tc.env)); got != tc.want {
				t.Errorf("resolveGraphicsProtocol(%q) = %v, want %v", tc.pref, got, tc.want)
			}
		})
	}
}
