//go:build darwin

package notifications

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
)

func sendDesktopNotification(title string, message string, image string, playSound bool, _ int) error {
	if path, err := exec.LookPath("terminal-notifier"); err != nil {
		slog.Debug("terminal-notifier not found, falling back to osascript", "err", err)
	} else {
		slog.Debug("terminal-notifier found, using it", "path", path)
		args := []string{"-title", title, "-message", message}
		if image != "" {
			args = append(args, "-contentImage", image)
		}
		if playSound {
			args = append(args, "-sound", "default")
		}
		var stderr bytes.Buffer
		cmd := exec.Command(path, args...)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			slog.Debug("terminal-notifier failed", "err", err, "stderr", stderr.String())
		} else {
			slog.Debug("terminal-notifier succeeded", "stderr", stderr.String())
			return nil
		}
	}

	slog.Debug("sending notification via osascript")
	script := fmt.Sprintf("display notification %q with title %q", message, title)
	if playSound {
		script += " sound name \"default\""
	}

	return exec.Command("osascript", "-e", script).Run()
}
