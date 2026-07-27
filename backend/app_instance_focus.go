package backend

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"facade/backend/internal/browser"
	"facade/backend/internal/logger"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// BrowserInstanceFocusResult reports whether the OS raise and CDP flash succeeded.
type BrowserInstanceFocusResult struct {
	Raised  bool   `json:"raised"`
	Flashed bool   `json:"flashed"`
	Warning string `json:"warning,omitempty"`
}

// BrowserInstanceFocus brings a running browser instance window to the front and
// briefly flashes a colored border on the active page.
func (a *App) BrowserInstanceFocus(profileId string) (*BrowserInstanceFocusResult, error) {
	profileId = strings.TrimSpace(profileId)
	if profileId == "" {
		return nil, fmt.Errorf("聚焦失败：实例 ID 不能为空")
	}
	log := logger.New("Browser")

	a.browserMgr.Mutex.Lock()
	profile, exists := a.browserMgr.Profiles[profileId]
	if !exists {
		a.browserMgr.Mutex.Unlock()
		return nil, fmt.Errorf("聚焦失败：未找到实例配置（ID=%s）。请刷新列表后重试。", profileId)
	}
	a.ensureProfileLaunchCode(profile)
	trackedCmd := a.browserMgr.BrowserProcesses[profileId]
	if !profile.Running {
		userDataDir := a.browserMgr.ResolveUserDataDir(profile)
		if detection, ok := detectBrowserRuntimeByUserDataDir(userDataDir); ok {
			a.markProfileRunningLocked(profileId, profile, nil, detection.PID, detection.DebugPort, detection.DebugReady, "")
			log.Warn("聚焦前发现同一用户数据目录浏览器已运行，已同步实例状态",
				logger.F("profile_id", profileId),
				logger.F("pid", detection.PID),
				logger.F("debug_port", detection.DebugPort),
			)
		} else {
			a.browserMgr.Mutex.Unlock()
			return nil, fmt.Errorf("聚焦失败：实例当前未运行，请先启动实例后再试。")
		}
	}
	if !isBrowserProfileLive(profile, trackedCmd) {
		a.markProfileStoppedLocked(profileId, profile)
		a.browserMgr.Mutex.Unlock()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "browser:instance:stopped", profileId)
		}
		return nil, fmt.Errorf("聚焦失败：检测到实例运行状态已失效，请先重新启动实例。")
	}

	pid := resolveBrowserFocusPID(profile, trackedCmd)
	if pid <= 0 {
		userDataDir := a.browserMgr.ResolveUserDataDir(profile)
		if detection, ok := detectBrowserRuntimeByUserDataDir(userDataDir); ok && detection.PID > 0 {
			pid = detection.PID
			if profile.Pid <= 0 {
				profile.Pid = detection.PID
			}
			if detection.DebugPort > 0 && profile.DebugPort <= 0 {
				profile.DebugPort = detection.DebugPort
				profile.DebugReady = detection.DebugReady
			}
		}
	}
	debugPort := profile.DebugPort
	debugReady := profile.DebugReady
	color := browser.IdentityColorForProfileID(profile.ProfileId)
	a.browserMgr.Mutex.Unlock()

	result := &BrowserInstanceFocusResult{}
	var raiseErr error
	if pid > 0 {
		raiseErr = activateBrowserWindowByPID(pid)
		if raiseErr == nil {
			result.Raised = true
		} else {
			log.Warn("前置浏览器窗口失败",
				logger.F("profile_id", profileId),
				logger.F("pid", pid),
				logger.F("error", raiseErr.Error()),
			)
		}
	} else {
		raiseErr = fmt.Errorf("无法解析浏览器进程 PID")
	}

	if debugReady && debugPort > 0 {
		if err := flashBrowserFocusBorder(debugPort, color); err != nil {
			log.Warn("聚焦边框闪烁失败",
				logger.F("profile_id", profileId),
				logger.F("debug_port", debugPort),
				logger.F("error", err.Error()),
			)
		} else {
			result.Flashed = true
		}
	}

	switch {
	case result.Raised && result.Flashed:
		return result, nil
	case result.Raised && !result.Flashed:
		result.Warning = "窗口已前置，但调试接口未就绪，未能高亮边框"
		return result, nil
	case !result.Raised && result.Flashed:
		result.Warning = "已尝试高亮，但未能前置系统窗口"
		return result, nil
	default:
		if raiseErr != nil {
			return result, fmt.Errorf("聚焦失败：%v", raiseErr)
		}
		return result, fmt.Errorf("聚焦失败：无法前置窗口且调试接口不可用")
	}
}

func resolveBrowserFocusPID(profile *BrowserProfile, trackedCmd *exec.Cmd) int {
	if profile != nil && profile.Pid > 0 {
		return profile.Pid
	}
	if trackedCmd != nil && trackedCmd.Process != nil && trackedCmd.Process.Pid > 0 {
		return trackedCmd.Process.Pid
	}
	return 0
}

func flashBrowserFocusBorder(debugPort int, color string) error {
	color = strings.TrimSpace(color)
	if color == "" {
		color = "#4F46E5"
	}
	colorJSON, err := json.Marshal(color)
	if err != nil {
		return err
	}
	expression := `(function () {
  try {
    var color = ` + string(colorJSON) + `;
    var id = 'facade-focus-flash';
    var existing = document.getElementById(id);
    if (existing) existing.remove();
    var el = document.createElement('div');
    el.id = id;
    el.setAttribute('data-facade-focus-flash', '1');
    el.style.cssText = [
      'position:fixed',
      'inset:0',
      'z-index:2147483647',
      'pointer-events:none',
      'box-sizing:border-box',
      'border:6px solid ' + color,
      'box-shadow:inset 0 0 0 2px rgba(255,255,255,0.45)',
      'transition:opacity 200ms ease'
    ].join(';');
    var root = document.documentElement || document.body;
    if (!root) return false;
    root.appendChild(el);
    setTimeout(function () {
      try { el.style.opacity = '0'; } catch (_) {}
    }, 900);
    setTimeout(function () {
      try { el.remove(); } catch (_) {}
    }, 1200);
    return true;
  } catch (e) {
    return false;
  }
})()`
	_, _ = cdpCall(debugPort, "Page.bringToFront", map[string]any{})
	_, err = cdpCall(debugPort, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	return err
}
