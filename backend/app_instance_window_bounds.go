package backend

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"facade/backend/internal/logger"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type BrowserWindowBounds struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (a *App) BrowserInstanceSetWindowBounds(profileId string, bounds BrowserWindowBounds) (*BrowserProfile, error) {
	profileId = strings.TrimSpace(profileId)
	if profileId == "" {
		return nil, fmt.Errorf("平铺失败：实例 ID 不能为空")
	}
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return nil, fmt.Errorf("平铺失败：窗口宽高必须大于 0")
	}

	log := logger.New("Browser")
	a.browserMgr.Mutex.Lock()
	profile, exists := a.browserMgr.Profiles[profileId]
	if !exists {
		a.browserMgr.Mutex.Unlock()
		return nil, fmt.Errorf("平铺失败：未找到实例配置（ID=%s）。请刷新列表后重试。", profileId)
	}
	a.ensureProfileLaunchCode(profile)
	trackedCmd := a.browserMgr.BrowserProcesses[profileId]
	if !profile.Running {
		userDataDir := a.browserMgr.ResolveUserDataDir(profile)
		if detection, ok := detectBrowserRuntimeByUserDataDir(userDataDir); ok && detection.DebugReady {
			a.markProfileRunningLocked(profileId, profile, nil, detection.PID, detection.DebugPort, true, "")
			log.Warn("平铺前发现同一用户数据目录浏览器已运行，已同步实例状态",
				logger.F("profile_id", profileId),
				logger.F("pid", detection.PID),
				logger.F("debug_port", detection.DebugPort),
			)
		} else {
			a.browserMgr.Mutex.Unlock()
			return nil, fmt.Errorf("平铺失败：实例当前未运行，请先启动实例后再试。")
		}
	}
	if !isBrowserProfileLive(profile, trackedCmd) {
		a.markProfileStoppedLocked(profileId, profile)
		snapshot := copyBrowserProfileSnapshot(profile)
		a.browserMgr.Mutex.Unlock()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "browser:instance:stopped", profileId)
		}
		return snapshot, fmt.Errorf("平铺失败：检测到实例运行状态已失效，请先重新启动实例。")
	}
	if !profile.DebugReady || profile.DebugPort <= 0 {
		snapshot := copyBrowserProfileSnapshot(profile)
		a.browserMgr.Mutex.Unlock()
		return snapshot, fmt.Errorf("平铺失败：实例调试接口未就绪，请稍后再试。")
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
	snapshot := copyBrowserProfileSnapshot(profile)
	a.browserMgr.Mutex.Unlock()

	if err := setBrowserWindowBoundsByDebugPort(debugPort, bounds); err != nil {
		return snapshot, fmt.Errorf("平铺失败：%w", err)
	}
	if err := bringBrowserWindowToFrontAfterBounds(debugPort, pid); err != nil {
		return snapshot, fmt.Errorf("平铺失败：窗口已移动，但置顶失败：%w", err)
	}

	log.Info("实例窗口已平铺",
		logger.F("profile_id", profileId),
		logger.F("debug_port", debugPort),
		logger.F("pid", pid),
		logger.F("left", bounds.Left),
		logger.F("top", bounds.Top),
		logger.F("width", bounds.Width),
		logger.F("height", bounds.Height),
	)
	return snapshot, nil
}

func setBrowserWindowBoundsByDebugPort(debugPort int, bounds BrowserWindowBounds) error {
	targetID, err := firstBrowserPageTargetID(debugPort)
	if err != nil {
		return err
	}

	result, err := cdpBrowserCallResult(debugPort, "Browser.getWindowForTarget", map[string]any{
		"targetId": targetID,
	})
	if err != nil {
		return fmt.Errorf("获取窗口失败: %w", err)
	}
	windowID, err := parseCDPWindowID(result["windowId"])
	if err != nil {
		return err
	}

	if _, err := cdpBrowserCallResult(debugPort, "Browser.setWindowBounds", map[string]any{
		"windowId": windowID,
		"bounds": map[string]any{
			"windowState": "normal",
		},
	}); err != nil {
		return fmt.Errorf("恢复普通窗口状态失败: %w", err)
	}

	if _, err := cdpBrowserCallResult(debugPort, "Browser.setWindowBounds", map[string]any{
		"windowId": windowID,
		"bounds": map[string]any{
			"left":   bounds.Left,
			"top":    bounds.Top,
			"width":  bounds.Width,
			"height": bounds.Height,
		},
	}); err != nil {
		return fmt.Errorf("设置窗口位置失败: %w", err)
	}
	return nil
}

func bringBrowserWindowToFrontAfterBounds(debugPort int, pid int) error {
	var warnings []string
	if debugPort > 0 {
		if _, err := cdpCall(debugPort, "Page.bringToFront", map[string]any{}); err != nil {
			warnings = append(warnings, fmt.Sprintf("页面置前失败: %v", err))
		}
	}
	if pid <= 0 {
		warnings = append(warnings, "无法解析浏览器进程 PID")
		return fmt.Errorf("%s", strings.Join(warnings, "；"))
	}
	if err := activateBrowserWindowByPID(pid); err != nil {
		warnings = append(warnings, fmt.Sprintf("系统窗口置前失败: %v", err))
		return fmt.Errorf("%s", strings.Join(warnings, "；"))
	}
	return nil
}

func firstBrowserPageTargetID(debugPort int) (string, error) {
	body, err := cdpGetEndpointBody(debugPort, "/json")
	if err != nil {
		return "", fmt.Errorf("CDP /json 请求失败: %w", err)
	}

	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil || len(targets) == 0 {
		return "", fmt.Errorf("CDP targets 解析失败或为空")
	}
	for _, target := range targets {
		if target.Type == "page" && strings.TrimSpace(target.Id) != "" {
			return strings.TrimSpace(target.Id), nil
		}
	}
	return "", fmt.Errorf("未找到可用于定位窗口的页面目标")
}

func parseCDPWindowID(value any) (int, error) {
	numeric, ok := value.(float64)
	if !ok || numeric <= 0 || math.Trunc(numeric) != numeric {
		return 0, fmt.Errorf("CDP windowId 无效")
	}
	return int(numeric), nil
}
