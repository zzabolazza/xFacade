//go:build linux

package backend

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func activateBrowserWindowByPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	pidStr := strconv.Itoa(pid)

	if path, err := exec.LookPath("wmctrl"); err == nil {
		// wmctrl -a expects a window id; resolve via -lp (window_id desktop pid ...).
		listOut, listErr := exec.Command(path, "-lp").CombinedOutput()
		if listErr == nil {
			hwnd := findWmctrlWindowIDForPID(string(listOut), pid)
			if hwnd != "" {
				if out, actErr := exec.Command(path, "-i", "-a", hwnd).CombinedOutput(); actErr != nil {
					return fmt.Errorf("wmctrl activate: %s", strings.TrimSpace(string(out)))
				}
				return nil
			}
		}
	}

	if path, err := exec.LookPath("xdotool"); err == nil {
		searchOut, searchErr := exec.Command(path, "search", "--pid", pidStr).CombinedOutput()
		if searchErr == nil {
			windows := strings.Fields(strings.TrimSpace(string(searchOut)))
			if len(windows) > 0 {
				if out, actErr := exec.Command(path, "windowactivate", "--sync", windows[0]).CombinedOutput(); actErr != nil {
					return fmt.Errorf("xdotool activate: %s", strings.TrimSpace(string(out)))
				}
				return nil
			}
		}
	}

	return fmt.Errorf("no usable window activator for pid %d (install wmctrl or xdotool)", pid)
}

func findWmctrlWindowIDForPID(listOutput string, pid int) string {
	pidStr := strconv.Itoa(pid)
	for _, line := range strings.Split(listOutput, "\n") {
		fields := strings.Fields(line)
		// wmctrl -lp: window_id desktop pid hostname title...
		if len(fields) < 3 {
			continue
		}
		if fields[2] == pidStr {
			return fields[0]
		}
	}
	return ""
}
