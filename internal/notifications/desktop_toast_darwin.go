//go:build darwin

package notifications

import (
	"fmt"
	"os/exec"
)

func sendDesktopNotification(title string, message string, image string, playSound bool, _ int) error {
	if path, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", message}
		if image != "" {
			args = append(args, "-contentImage", image)
		}
		if playSound {
			args = append(args, "-sound", "default")
		}
		return exec.Command(path, args...).Run()
	}

	script := fmt.Sprintf("display notification %q with title %q", message, title)
	if playSound {
		script += " sound name \"default\""
	}

	return exec.Command("osascript", "-e", script).Run()
}
