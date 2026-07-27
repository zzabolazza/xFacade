//go:build darwin

package backend

import (
	"fmt"
	"os/exec"
	"strings"
)

func activateBrowserWindowByPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	script := fmt.Sprintf(`tell application "System Events"
  set procs to every process whose unix id is %d
  if (count of procs) is 0 then error "process not found"
  set frontmost of item 1 of procs to true
end tell`, pid)
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("activate pid %d: %s", pid, detail)
	}
	return nil
}
