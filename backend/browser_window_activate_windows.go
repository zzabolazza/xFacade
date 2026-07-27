//go:build windows

package backend

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	browserSWShow    = 5
	browserSWRestore = 9
	browserGWOwner   = 4
)

var (
	browserUser32                    = windows.NewLazySystemDLL("user32.dll")
	browserProcAllowSetForeground    = browserUser32.NewProc("AllowSetForegroundWindow")
	browserProcAttachThreadInput     = browserUser32.NewProc("AttachThreadInput")
	browserProcEnumWindows           = browserUser32.NewProc("EnumWindows")
	browserProcGetForegroundWindow   = browserUser32.NewProc("GetForegroundWindow")
	browserProcGetWindow             = browserUser32.NewProc("GetWindow")
	browserProcGetWindowTextLengthW  = browserUser32.NewProc("GetWindowTextLengthW")
	browserProcGetWindowThreadProcID = browserUser32.NewProc("GetWindowThreadProcessId")
	browserProcIsIconic              = browserUser32.NewProc("IsIconic")
	browserProcIsWindowVisible       = browserUser32.NewProc("IsWindowVisible")
	browserProcShowWindow            = browserUser32.NewProc("ShowWindow")
	browserProcSetForegroundWindow   = browserUser32.NewProc("SetForegroundWindow")
	browserProcSetWindowPos          = browserUser32.NewProc("SetWindowPos")
)

const (
	browserHWNDTopmost   = ^uintptr(0)
	browserHWNDNoTopmost = ^uintptr(1)
	browserSWPNoMove     = 0x0002
	browserSWPNoSize     = 0x0001
	browserSWPShowWindow = 0x0040
)

func activateBrowserWindowByPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	browserProcAllowSetForeground.Call(uintptr(pid))
	hwnd := findBrowserTopLevelWindowByPID(uint32(pid))
	if hwnd == 0 {
		return fmt.Errorf("no top-level window for pid %d", pid)
	}
	browserProcShowWindow.Call(hwnd, browserSWRestore)
	browserProcShowWindow.Call(hwnd, browserSWShow)
	if ok, _, _ := browserProcSetForegroundWindow.Call(hwnd); ok == 0 {
		attachBrowserForegroundThreadInput(hwnd, func() {
			browserProcSetForegroundWindow.Call(hwnd)
		})
	}
	if ok, _, _ := browserProcSetForegroundWindow.Call(hwnd); ok == 0 {
		browserProcSetWindowPos.Call(hwnd, browserHWNDTopmost, 0, 0, 0, 0, browserSWPNoMove|browserSWPNoSize|browserSWPShowWindow)
		browserProcSetWindowPos.Call(hwnd, browserHWNDNoTopmost, 0, 0, 0, 0, browserSWPNoMove|browserSWPNoSize|browserSWPShowWindow)
		browserProcSetForegroundWindow.Call(hwnd)
	}
	return nil
}

func attachBrowserForegroundThreadInput(hwnd uintptr, fn func()) {
	foreground, _, _ := browserProcGetForegroundWindow.Call()
	if foreground == 0 || hwnd == 0 {
		fn()
		return
	}
	var foregroundPID uint32
	foregroundThread, _, _ := browserProcGetWindowThreadProcID.Call(foreground, uintptr(unsafe.Pointer(&foregroundPID)))
	var targetPID uint32
	targetThread, _, _ := browserProcGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&targetPID)))
	if foregroundThread == 0 || targetThread == 0 || foregroundThread == targetThread {
		fn()
		return
	}
	browserProcAttachThreadInput.Call(foregroundThread, targetThread, 1)
	defer browserProcAttachThreadInput.Call(foregroundThread, targetThread, 0)
	fn()
}

func findBrowserTopLevelWindowByPID(pid uint32) uintptr {
	type candidateWindow struct {
		hwnd      uintptr
		visible   bool
		titled    bool
		owned     bool
		minimized bool
	}
	var candidates []candidateWindow
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var windowPID uint32
		browserProcGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if windowPID == pid {
			visible, _, _ := browserProcIsWindowVisible.Call(hwnd)
			titleLength, _, _ := browserProcGetWindowTextLengthW.Call(hwnd)
			owner, _, _ := browserProcGetWindow.Call(hwnd, browserGWOwner)
			minimized, _, _ := browserProcIsIconic.Call(hwnd)
			candidates = append(candidates, candidateWindow{
				hwnd:      hwnd,
				visible:   visible != 0,
				titled:    titleLength > 0,
				owned:     owner != 0,
				minimized: minimized != 0,
			})
		}
		return 1
	})
	browserProcEnumWindows.Call(callback, 0)
	if len(candidates) == 0 {
		return 0
	}
	for _, candidate := range candidates {
		if candidate.visible && candidate.titled && !candidate.owned {
			return candidate.hwnd
		}
	}
	for _, candidate := range candidates {
		if candidate.titled && !candidate.owned {
			return candidate.hwnd
		}
	}
	for _, candidate := range candidates {
		if candidate.visible && !candidate.owned {
			return candidate.hwnd
		}
	}
	for _, candidate := range candidates {
		if candidate.minimized && !candidate.owned {
			return candidate.hwnd
		}
	}
	return candidates[0].hwnd
}
