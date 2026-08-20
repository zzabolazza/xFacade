package backend

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"facade/backend/internal/config"
	"facade/backend/internal/logger"
)

func browserLightStartEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Browser.LightStartEnabled == nil {
		return true
	}
	return *cfg.Browser.LightStartEnabled
}

func resolveConfiguredStartTargets(startURLs []string, defaultStartURLs []string, skipDefaultStartURLs bool) []string {
	normalizedStartURLs := normalizeNonEmptyStrings(startURLs)
	if len(normalizedStartURLs) > 0 {
		return normalizedStartURLs
	}

	if skipDefaultStartURLs {
		return nil
	}

	return normalizeNonEmptyStrings(defaultStartURLs)
}

func buildBrowserLaunchTargets(startURLs []string, defaultStartURLs []string, skipDefaultStartURLs bool, restoreLastSession bool, lightStartEnabled bool) ([]string, []string) {
	configuredTargets := resolveConfiguredStartTargets(startURLs, defaultStartURLs, skipDefaultStartURLs)
	if lightStartEnabled && len(configuredTargets) > 0 {
		return []string{"about:blank"}, configuredTargets
	}
	if len(configuredTargets) > 0 {
		return configuredTargets, nil
	}
	if !restoreLastSession {
		return []string{"about:blank"}, nil
	}
	return nil, nil
}

func deferredStartTargetsWarning(targets []string, err error) string {
	normalized := normalizeNonEmptyStrings(targets)
	if len(normalized) <= 1 {
		return fmt.Sprintf("浏览器已启动，但启动页未能自动打开：%v。可稍后手动打开。", err)
	}
	return fmt.Sprintf("浏览器已启动，但 %d 个启动页未能自动打开：%v。可稍后手动打开。", len(normalized), err)
}

func (a *App) storeDeferredStartTargets(profileId string, targets []string) {
	if a == nil {
		return
	}

	normalized := normalizeNonEmptyStrings(targets)
	a.deferredStartTargetsMu.Lock()
	defer a.deferredStartTargetsMu.Unlock()

	if len(normalized) == 0 {
		delete(a.deferredStartTargets, profileId)
		return
	}
	a.deferredStartTargets[profileId] = append([]string{}, normalized...)
}

func (a *App) consumeDeferredStartTargets(profileId string) []string {
	if a == nil {
		return nil
	}

	a.deferredStartTargetsMu.Lock()
	defer a.deferredStartTargetsMu.Unlock()

	targets := append([]string{}, a.deferredStartTargets[profileId]...)
	delete(a.deferredStartTargets, profileId)
	return targets
}

func (a *App) clearDeferredStartTargets(profileId string) {
	if a == nil {
		return
	}

	a.deferredStartTargetsMu.Lock()
	delete(a.deferredStartTargets, profileId)
	a.deferredStartTargetsMu.Unlock()
}

func cdpBrowserCallResult(debugPort int, method string, params map[string]any) (map[string]any, error) {
	body, err := cdpGetEndpointBody(debugPort, "/json/version")
	if err != nil {
		return nil, fmt.Errorf("CDP /json/version 请求失败: %w", err)
	}
	var version cdpBrowserVersion
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, fmt.Errorf("CDP browser target 解析失败: %w", err)
	}
	wsURL := strings.TrimSpace(version.WebSocketDebuggerUrl)
	if wsURL == "" {
		return nil, fmt.Errorf("未找到浏览器级 WebSocket 调试地址")
	}

	conn, err := cdpDialWebSocket(wsURL)
	if err != nil {
		return nil, fmt.Errorf("浏览器级 WebSocket 连接失败: %w", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(cdpWebSocketReadTimeout))

	msg := cdpMessage{Id: 1, Method: method, Params: params}
	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("浏览器级 CDP 命令发送失败: %w", err)
	}

	var cdpResp cdpResponse
	if err := conn.ReadJSON(&cdpResp); err != nil {
		return nil, fmt.Errorf("浏览器级 CDP 响应读取失败: %w", err)
	}
	if cdpResp.Error != nil {
		return nil, fmt.Errorf("浏览器级 CDP 错误: %s", cdpResp.Error.Message)
	}
	return cdpResp.Result, nil
}

func createBrowserStartTarget(debugPort int, url string) error {
	_, err := cdpBrowserCallResult(debugPort, "Target.createTarget", map[string]any{
		"url": url,
	})
	return err
}

// isUnwantedExtensionStartupURL reports whether a page URL is an extension
// welcome/options page or the chrome://extensions management UI.
func isUnwantedExtensionStartupURL(raw string) bool {
	u := strings.TrimSpace(strings.ToLower(raw))
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "chrome-extension://") {
		return true
	}
	if u == "chrome://extensions" ||
		strings.HasPrefix(u, "chrome://extensions/") ||
		strings.HasPrefix(u, "chrome://extensions?") {
		return true
	}
	return false
}

func listCDPPageTargets(debugPort int) ([]cdpTarget, error) {
	body, err := cdpGetEndpointBody(debugPort, "/json")
	if err != nil {
		return nil, fmt.Errorf("CDP /json 请求失败: %w", err)
	}
	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("CDP targets 解析失败: %w", err)
	}
	pages := make([]cdpTarget, 0, len(targets))
	for _, target := range targets {
		if strings.EqualFold(strings.TrimSpace(target.Type), "page") {
			pages = append(pages, target)
		}
	}
	return pages, nil
}

func closeUnwantedExtensionStartupTabs(debugPort int) (int, error) {
	if debugPort <= 0 {
		return 0, nil
	}
	pages, err := listCDPPageTargets(debugPort)
	if err != nil {
		return 0, err
	}

	toClose := make([]cdpTarget, 0)
	for _, page := range pages {
		if isUnwantedExtensionStartupURL(page.Url) {
			toClose = append(toClose, page)
		}
	}
	if len(toClose) == 0 {
		return 0, nil
	}

	if len(pages)-len(toClose) <= 0 {
		if err := createBrowserStartTarget(debugPort, "about:blank"); err != nil {
			return 0, fmt.Errorf("创建空白页失败: %w", err)
		}
	}

	closed := 0
	var firstErr error
	for _, page := range toClose {
		id := strings.TrimSpace(page.Id)
		if id == "" {
			continue
		}
		if _, err := cdpBrowserCallResult(debugPort, "Target.closeTarget", map[string]any{
			"targetId": id,
		}); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		closed++
	}
	if closed == 0 && firstErr != nil {
		return 0, firstErr
	}
	return closed, nil
}

// scheduleUnwantedExtensionStartupTabSweep closes extension welcome / extensions
// pages shortly after launch. With many --load-extension packages, onInstalled
// tabs can appear several seconds later, so we sweep over ~10s without blocking start.
func scheduleUnwantedExtensionStartupTabSweep(debugPort int) {
	if debugPort <= 0 {
		return
	}
	go func() {
		log := logger.New("Browser")
		// Cumulative ~10s: 0 + 1s + 1.5s + 2s + 2.5s + 3s
		waits := []time.Duration{
			0,
			1 * time.Second,
			1500 * time.Millisecond,
			2 * time.Second,
			2500 * time.Millisecond,
			3 * time.Second,
		}
		for pass, wait := range waits {
			if wait > 0 {
				time.Sleep(wait)
			}
			closed, err := closeUnwantedExtensionStartupTabs(debugPort)
			if err != nil {
				log.Debug("关闭扩展启动页失败",
					logger.F("debug_port", debugPort),
					logger.F("pass", pass+1),
					logger.F("error", err.Error()),
				)
				continue
			}
			if closed > 0 {
				log.Info("已关闭扩展启动页",
					logger.F("debug_port", debugPort),
					logger.F("closed", closed),
					logger.F("pass", pass+1),
				)
			}
		}
	}()
}

func openBrowserStartTargets(debugPort int, targets []string) error {
	normalized := normalizeNonEmptyStrings(targets)
	if len(normalized) == 0 {
		return nil
	}

	_, navigateErr := cdpCall(debugPort, "Page.navigate", map[string]any{
		"url": normalized[0],
	})
	if navigateErr == nil {
		for _, url := range normalized[1:] {
			if err := createBrowserStartTarget(debugPort, url); err != nil {
				return err
			}
		}
		return nil
	}

	for _, url := range normalized {
		if err := createBrowserStartTarget(debugPort, url); err != nil {
			return fmt.Errorf("首个页面改为新标签回退失败：navigate=%v, createTarget=%w", navigateErr, err)
		}
	}
	return nil
}

func (a *App) setProfileRuntimeWarning(profileId string, debugPort int, warning string) (*BrowserProfile, bool) {
	if a == nil || a.browserMgr == nil {
		return nil, false
	}

	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()

	profile, exists := a.browserMgr.Profiles[profileId]
	if !exists || profile == nil || !profile.Running || profile.DebugPort != debugPort {
		return nil, false
	}

	if profile.RuntimeWarning == warning && profile.LastError == "" {
		return copyBrowserProfileSnapshot(profile), false
	}
	profile.RuntimeWarning = warning
	profile.LastError = ""
	return copyBrowserProfileSnapshot(profile), true
}

func (a *App) finalizeDeferredStartTargets(profileId string, debugPort int) (*BrowserProfile, bool) {
	targets := a.consumeDeferredStartTargets(profileId)
	if len(targets) == 0 {
		return nil, false
	}

	if err := openBrowserStartTargets(debugPort, targets); err != nil {
		warning := deferredStartTargetsWarning(targets, err)
		snapshot, changed := a.setProfileRuntimeWarning(profileId, debugPort, warning)
		logger.New("Browser").Warn("浏览器已就绪，但启动页延后打开失败",
			logger.F("profile_id", profileId),
			logger.F("debug_port", debugPort),
			logger.F("target_count", len(targets)),
			logger.F("error", err.Error()),
			logger.F("warning", warning),
		)
		return snapshot, changed
	}

	return nil, false
}
