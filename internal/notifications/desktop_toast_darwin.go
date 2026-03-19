//go:build darwin

package notifications

import (
	"fmt"
	"os/exec"
)

func sendDesktopNotification(title string, message string, _ string, playSound bool, _ int) error {
	script := fmt.Sprintf("display notification %q with title %q", message, title)
	if playSound {
		script += " sound name \"default\""
	}

	return exec.Command("osascript", "-e", script).Run()
}
