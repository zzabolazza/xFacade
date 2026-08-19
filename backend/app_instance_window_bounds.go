package backend

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"facade/backend/internal/logger"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type BrowserWindowBounds struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type BrowserWindowTileRequest struct {
	ProfileId string              `json:"profileId"`
	Bounds    BrowserWindowBounds `json:"bounds"`
}

type BrowserWindowTilingRequest struct {
	Tiles         []BrowserWindowTileRequest `json:"tiles"`
	AlignViewport bool                       `json:"alignViewport"`
}

type BrowserWindowViewportSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type BrowserWindowTilingResult struct {
	ProfileId      string                    `json:"profileId"`
	ProfileName    string                    `json:"profileName"`
	Requested      BrowserWindowBounds       `json:"requested"`
	Applied        BrowserWindowBounds       `json:"applied"`
	Actual         BrowserWindowBounds       `json:"actual"`
	VisualViewport BrowserWindowViewportSize `json:"visualViewport"`
	LayoutViewport BrowserWindowViewportSize `json:"layoutViewport"`
	ChromeWidth    int                       `json:"chromeWidth"`
	ChromeHeight   int                       `json:"chromeHeight"`
	Ready          bool                      `json:"ready"`
	Warning        string                    `json:"warning,omitempty"`
}

type browserWindowRuntimeSnapshot struct {
	windowID       int
	bounds         BrowserWindowBounds
	visualViewport inputSyncViewport
	layoutViewport inputSyncViewport
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

	windowID, err := setBrowserWindowBoundsByDebugPort(debugPort, bounds)
	if err != nil {
		return snapshot, fmt.Errorf("平铺失败：%w", err)
	}
	if err := bringBrowserWindowToFrontAfterBounds(debugPort, pid); err != nil {
		return snapshot, fmt.Errorf("平铺失败：窗口已移动，但置顶失败：%w", err)
	}

	runtimeSnapshot, inspectErr := waitForBrowserWindowRuntimeSnapshot(debugPort, windowID, bounds, 900*time.Millisecond)
	logBrowserWindowTilingResult(log, "实例窗口已平铺", profileId, snapshot.ProfileName, debugPort, pid, bounds, bounds, runtimeSnapshot, inspectErr)
	return snapshot, nil
}

func (a *App) BrowserInstancesSetWindowBounds(req BrowserWindowTilingRequest) ([]BrowserWindowTilingResult, error) {
	if len(req.Tiles) == 0 {
		return nil, fmt.Errorf("平铺失败：没有可平铺的实例")
	}

	log := logger.New("Browser")
	log.Info("浏览器窗口批量平铺开始",
		logger.F("count", len(req.Tiles)),
		logger.F("align_viewport", req.AlignViewport),
		logger.F("tiles", describeBrowserWindowTileRequests(req.Tiles)),
	)

	results := make([]BrowserWindowTilingResult, 0, len(req.Tiles))
	debugPorts := make([]int, 0, len(req.Tiles))
	for _, tile := range req.Tiles {
		result := BrowserWindowTilingResult{
			ProfileId: strings.TrimSpace(tile.ProfileId),
			Requested: tile.Bounds,
			Applied:   tile.Bounds,
		}
		profile, err := a.BrowserInstanceSetWindowBounds(tile.ProfileId, tile.Bounds)
		if profile != nil {
			result.ProfileName = profile.ProfileName
		}
		if err != nil {
			result.Warning = err.Error()
			results = append(results, result)
			debugPorts = append(debugPorts, 0)
			log.Warn("浏览器窗口批量平铺实例失败",
				logger.F("profile_id", result.ProfileId),
				logger.F("profile_name", result.ProfileName),
				logger.F("requested", formatBrowserWindowBounds(tile.Bounds)),
				logger.F("error", err.Error()),
			)
			continue
		}

		debugPort := 0
		if profile != nil {
			debugPort = profile.DebugPort
		}
		debugPorts = append(debugPorts, debugPort)
		runtimeSnapshot, inspectErr := waitForBrowserWindowRuntimeSnapshot(debugPort, 0, tile.Bounds, 900*time.Millisecond)
		applyBrowserWindowRuntimeSnapshot(&result, runtimeSnapshot, inspectErr)
		results = append(results, result)
	}

	if req.AlignViewport {
		results = a.alignBrowserWindowViewports(req.Tiles, results, debugPorts)
	}

	ready, warned := summarizeBrowserWindowTilingResults(results)
	log.Info("浏览器窗口批量平铺完成",
		logger.F("count", len(results)),
		logger.F("ready", ready),
		logger.F("warned", warned),
		logger.F("results", describeBrowserWindowTilingResults(results)),
	)
	return results, nil
}

func setBrowserWindowBoundsByDebugPort(debugPort int, bounds BrowserWindowBounds) (int, error) {
	targetID, err := firstBrowserPageTargetID(debugPort)
	if err != nil {
		return 0, err
	}

	result, err := cdpBrowserCallResult(debugPort, "Browser.getWindowForTarget", map[string]any{
		"targetId": targetID,
	})
	if err != nil {
		return 0, fmt.Errorf("获取窗口失败: %w", err)
	}
	windowID, err := parseCDPWindowID(result["windowId"])
	if err != nil {
		return 0, err
	}

	if _, err := cdpBrowserCallResult(debugPort, "Browser.setWindowBounds", map[string]any{
		"windowId": windowID,
		"bounds": map[string]any{
			"windowState": "normal",
		},
	}); err != nil {
		return 0, fmt.Errorf("恢复普通窗口状态失败: %w", err)
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
		return 0, fmt.Errorf("设置窗口位置失败: %w", err)
	}
	return windowID, nil
}

func logBrowserWindowTilingResult(log *logger.Logger, message string, profileId string, profileName string, debugPort int, pid int, requested BrowserWindowBounds, applied BrowserWindowBounds, runtimeSnapshot browserWindowRuntimeSnapshot, inspectErr error) {
	if log == nil {
		return
	}
	fields := []logger.Field{
		logger.F("profile_id", profileId),
		logger.F("profile_name", profileName),
		logger.F("debug_port", debugPort),
		logger.F("pid", pid),
		logger.F("requested", formatBrowserWindowBounds(requested)),
		logger.F("applied", formatBrowserWindowBounds(applied)),
	}
	if inspectErr != nil {
		fields = append(fields, logger.F("inspect_error", inspectErr.Error()))
		log.Warn(message, fields...)
		return
	}
	fields = append(fields,
		logger.F("window_id", runtimeSnapshot.windowID),
		logger.F("actual", formatBrowserWindowBounds(runtimeSnapshot.bounds)),
		logger.F("visual_viewport", formatBrowserWindowViewport(runtimeSnapshot.visualViewport)),
		logger.F("layout_viewport", formatBrowserWindowViewport(runtimeSnapshot.layoutViewport)),
		logger.F("chrome_width", runtimeSnapshot.bounds.Width-int(math.Round(runtimeSnapshot.visualViewport.width))),
		logger.F("chrome_height", runtimeSnapshot.bounds.Height-int(math.Round(runtimeSnapshot.visualViewport.height))),
	)
	log.Info(message, fields...)
}

func (a *App) alignBrowserWindowViewports(tiles []BrowserWindowTileRequest, results []BrowserWindowTilingResult, debugPorts []int) []BrowserWindowTilingResult {
	if a == nil || len(results) < 2 {
		return results
	}

	commonWidth, commonHeight := 0, 0
	readyIndexes := make([]int, 0, len(results))
	for i, result := range results {
		if !result.Ready || result.VisualViewport.Width <= 0 || result.VisualViewport.Height <= 0 || i >= len(debugPorts) || debugPorts[i] <= 0 {
			continue
		}
		readyIndexes = append(readyIndexes, i)
		if commonWidth == 0 || result.VisualViewport.Width < commonWidth {
			commonWidth = result.VisualViewport.Width
		}
		if commonHeight == 0 || result.VisualViewport.Height < commonHeight {
			commonHeight = result.VisualViewport.Height
		}
	}
	if len(readyIndexes) < 2 || commonWidth <= 0 || commonHeight <= 0 {
		return results
	}

	if browserWindowViewportsAlreadyAligned(results, readyIndexes) {
		return results
	}

	log := logger.New("Browser")
	log.Info("浏览器窗口平铺准备校正网页视口",
		logger.F("common_viewport", fmt.Sprintf("%dx%d", commonWidth, commonHeight)),
		logger.F("before", describeBrowserWindowTilingResults(results)),
	)

	for _, index := range readyIndexes {
		result := results[index]
		desired := result.Applied
		nextWidth := commonWidth + result.ChromeWidth
		nextHeight := commonHeight + result.ChromeHeight
		if nextWidth > 0 && nextWidth < desired.Width {
			desired.Width = nextWidth
		}
		if nextHeight > 0 && nextHeight < desired.Height {
			desired.Height = nextHeight
		}
		if desired == result.Applied {
			continue
		}

		windowID, err := setBrowserWindowBoundsByDebugPort(debugPorts[index], desired)
		if err != nil {
			results[index].Warning = appendBrowserWindowWarning(results[index].Warning, fmt.Sprintf("视口校正失败：%v", err))
			results[index].Ready = false
			continue
		}
		runtimeSnapshot, inspectErr := waitForBrowserWindowRuntimeSnapshot(debugPorts[index], windowID, desired, 900*time.Millisecond)
		results[index].Applied = desired
		applyBrowserWindowRuntimeSnapshot(&results[index], runtimeSnapshot, inspectErr)
		logBrowserWindowTilingResult(log, "浏览器窗口平铺视口校正",
			results[index].ProfileId,
			results[index].ProfileName,
			debugPorts[index],
			0,
			tiles[index].Bounds,
			desired,
			runtimeSnapshot,
			inspectErr,
		)
	}

	alignedIndexes := readyBrowserWindowTilingResultIndexes(results)
	if !browserWindowViewportsAlreadyAligned(results, alignedIndexes) {
		log.Warn("浏览器窗口平铺视口校正后仍不一致",
			logger.F("common_viewport", fmt.Sprintf("%dx%d", commonWidth, commonHeight)),
			logger.F("after", describeBrowserWindowTilingResults(results)),
		)
		for _, index := range alignedIndexes {
			results[index].Warning = appendBrowserWindowWarning(results[index].Warning, "校正后网页视口仍不一致，请检查页面滚动条、浏览器工具栏或重新平铺")
		}
	}

	return results
}

func readyBrowserWindowTilingResultIndexes(results []BrowserWindowTilingResult) []int {
	indexes := make([]int, 0, len(results))
	for index, result := range results {
		if result.Ready && result.VisualViewport.Width > 0 && result.VisualViewport.Height > 0 {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func browserWindowViewportsAlreadyAligned(results []BrowserWindowTilingResult, indexes []int) bool {
	if len(indexes) < 2 {
		return true
	}
	first := results[indexes[0]].VisualViewport
	for _, index := range indexes[1:] {
		next := results[index].VisualViewport
		if math.Abs(float64(first.Width-next.Width)) > inputSyncViewportSlack ||
			math.Abs(float64(first.Height-next.Height)) > inputSyncViewportSlack {
			return false
		}
	}
	return true
}

func waitForBrowserWindowRuntimeSnapshot(debugPort int, windowID int, requested BrowserWindowBounds, timeout time.Duration) (browserWindowRuntimeSnapshot, error) {
	if debugPort <= 0 {
		return browserWindowRuntimeSnapshot{}, fmt.Errorf("调试端口为空")
	}

	deadline := time.Now().Add(timeout)
	var last browserWindowRuntimeSnapshot
	var lastErr error
	for {
		snapshot, err := inspectBrowserWindowRuntimeSnapshot(debugPort, windowID)
		if err == nil {
			last = snapshot
			if browserWindowDimensionsClose(snapshot.bounds, requested) {
				return snapshot, nil
			}
		} else {
			lastErr = err
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			if last.windowID > 0 {
				return last, nil
			}
			if lastErr != nil {
				return browserWindowRuntimeSnapshot{}, lastErr
			}
			return browserWindowRuntimeSnapshot{}, fmt.Errorf("未能读取窗口状态")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func inspectBrowserWindowRuntimeSnapshot(debugPort int, windowID int) (browserWindowRuntimeSnapshot, error) {
	if windowID <= 0 {
		targetID, err := firstBrowserPageTargetID(debugPort)
		if err != nil {
			return browserWindowRuntimeSnapshot{}, err
		}
		result, err := cdpBrowserCallResult(debugPort, "Browser.getWindowForTarget", map[string]any{
			"targetId": targetID,
		})
		if err != nil {
			return browserWindowRuntimeSnapshot{}, fmt.Errorf("获取窗口失败: %w", err)
		}
		parsedWindowID, err := parseCDPWindowID(result["windowId"])
		if err != nil {
			return browserWindowRuntimeSnapshot{}, err
		}
		windowID = parsedWindowID
	}

	result, err := cdpBrowserCallResult(debugPort, "Browser.getWindowBounds", map[string]any{
		"windowId": windowID,
	})
	if err != nil {
		return browserWindowRuntimeSnapshot{}, fmt.Errorf("读取窗口尺寸失败: %w", err)
	}
	bounds, ok := parseCDPBrowserWindowBounds(result["bounds"])
	if !ok {
		return browserWindowRuntimeSnapshot{}, fmt.Errorf("CDP window bounds 无效")
	}

	visualViewport, layoutViewport, err := readBrowserPageViewportPair(debugPort)
	if err != nil {
		return browserWindowRuntimeSnapshot{}, err
	}
	return browserWindowRuntimeSnapshot{
		windowID:       windowID,
		bounds:         bounds,
		visualViewport: visualViewport,
		layoutViewport: layoutViewport,
	}, nil
}

func readBrowserPageViewportPair(debugPort int) (inputSyncViewport, inputSyncViewport, error) {
	result, err := cdpCall(debugPort, "Page.getLayoutMetrics", nil)
	if err != nil {
		return inputSyncViewport{}, inputSyncViewport{}, fmt.Errorf("读取页面视口失败: %w", err)
	}
	visualViewport, visualOK := parseInputSyncViewport(result["cssVisualViewport"])
	layoutViewport, layoutOK := parseInputSyncViewport(result["cssLayoutViewport"])
	if !visualOK && !layoutOK {
		return inputSyncViewport{}, inputSyncViewport{}, fmt.Errorf("未能读取页面视口尺寸")
	}
	if !visualOK {
		visualViewport = layoutViewport
	}
	if !layoutOK {
		layoutViewport = visualViewport
	}
	return visualViewport, layoutViewport, nil
}

func applyBrowserWindowRuntimeSnapshot(result *BrowserWindowTilingResult, snapshot browserWindowRuntimeSnapshot, err error) {
	if result == nil {
		return
	}
	if err != nil {
		result.Warning = appendBrowserWindowWarning(result.Warning, fmt.Sprintf("读取实际窗口/视口失败：%v", err))
		result.Ready = false
		return
	}
	result.Actual = snapshot.bounds
	result.VisualViewport = inputSyncViewportToWindowSize(snapshot.visualViewport)
	result.LayoutViewport = inputSyncViewportToWindowSize(snapshot.layoutViewport)
	result.ChromeWidth = snapshot.bounds.Width - result.VisualViewport.Width
	result.ChromeHeight = snapshot.bounds.Height - result.VisualViewport.Height
	result.Ready = result.VisualViewport.Width > 0 && result.VisualViewport.Height > 0
}

func browserWindowDimensionsClose(actual BrowserWindowBounds, requested BrowserWindowBounds) bool {
	return math.Abs(float64(actual.Width-requested.Width)) <= inputSyncViewportSlack &&
		math.Abs(float64(actual.Height-requested.Height)) <= inputSyncViewportSlack
}

func parseCDPBrowserWindowBounds(value any) (BrowserWindowBounds, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return BrowserWindowBounds{}, false
	}
	width, widthOK := inputSyncNumber(item["width"])
	height, heightOK := inputSyncNumber(item["height"])
	if !widthOK || !heightOK || width <= 0 || height <= 0 {
		return BrowserWindowBounds{}, false
	}
	left, _ := inputSyncNumber(item["left"])
	top, _ := inputSyncNumber(item["top"])
	return BrowserWindowBounds{
		Left:   int(math.Round(left)),
		Top:    int(math.Round(top)),
		Width:  int(math.Round(width)),
		Height: int(math.Round(height)),
	}, true
}

func inputSyncViewportToWindowSize(viewport inputSyncViewport) BrowserWindowViewportSize {
	return BrowserWindowViewportSize{
		Width:  int(math.Round(viewport.width)),
		Height: int(math.Round(viewport.height)),
	}
}

func appendBrowserWindowWarning(current string, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return next
	}
	return current + "；" + next
}

func summarizeBrowserWindowTilingResults(results []BrowserWindowTilingResult) (int, int) {
	ready, warned := 0, 0
	for _, result := range results {
		if result.Ready {
			ready++
		}
		if result.Warning != "" {
			warned++
		}
	}
	return ready, warned
}

func describeBrowserWindowTileRequests(tiles []BrowserWindowTileRequest) string {
	parts := make([]string, 0, len(tiles))
	for _, tile := range tiles {
		parts = append(parts, fmt.Sprintf("%s=%s", strings.TrimSpace(tile.ProfileId), formatBrowserWindowBounds(tile.Bounds)))
	}
	return strings.Join(parts, "; ")
}

func describeBrowserWindowTilingResults(results []BrowserWindowTilingResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		label := strings.TrimSpace(result.ProfileName)
		if label == "" {
			label = strings.TrimSpace(result.ProfileId)
		}
		state := "ready"
		if !result.Ready {
			state = "not-ready"
		}
		if result.Warning != "" {
			state += ":" + result.Warning
		}
		parts = append(parts, fmt.Sprintf("%s requested=%s applied=%s actual=%s visual=%dx%d layout=%dx%d chrome=%dx%d %s",
			label,
			formatBrowserWindowBounds(result.Requested),
			formatBrowserWindowBounds(result.Applied),
			formatBrowserWindowBounds(result.Actual),
			result.VisualViewport.Width,
			result.VisualViewport.Height,
			result.LayoutViewport.Width,
			result.LayoutViewport.Height,
			result.ChromeWidth,
			result.ChromeHeight,
			state,
		))
	}
	return strings.Join(parts, "; ")
}

func formatBrowserWindowBounds(bounds BrowserWindowBounds) string {
	return fmt.Sprintf("%d,%d %dx%d", bounds.Left, bounds.Top, bounds.Width, bounds.Height)
}

func formatBrowserWindowViewport(viewport inputSyncViewport) string {
	return fmt.Sprintf("%dx%d", int(math.Round(viewport.width)), int(math.Round(viewport.height)))
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
