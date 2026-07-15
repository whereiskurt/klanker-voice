package studio

import (
	"os/exec"
	"runtime"
)

// OpenBrowser best-effort launches the operator's default browser at url via
// the OS opener (macOS `open`, Linux/other unix-likes `xdg-open`, Windows
// `cmd /c start`). Opening the browser is a convenience, not a requirement —
// callers print the URL regardless and treat a non-nil return as
// informational only, never fatal.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// The empty "" argument is the window title `start` expects before
		// the URL when invoked through `cmd /c`.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
