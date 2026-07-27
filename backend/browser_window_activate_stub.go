//go:build !windows && !darwin && !linux

package backend

import "fmt"

func activateBrowserWindowByPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	return fmt.Errorf("window activation is not supported on this platform")
}
