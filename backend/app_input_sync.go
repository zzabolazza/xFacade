package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"facade/backend/internal/logger"

	"github.com/go-rod/rod"
	rodcdp "github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/proto"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	inputSyncModePage    = "page"
	inputSyncBindingName = "facadeInputSyncEvent"

	inputSyncCommandTimeout  = 3 * time.Second
	inputSyncDebugReadyWait  = 3 * time.Second
	inputSyncViewportSlack   = 2.0
	inputSyncTraceEvery      = int64(60)
	inputSyncHealthInterval  = 5 * time.Second
	inputSyncHealthLogEvery  = int64(3)
	inputSyncReconnectWait   = 8 * time.Second
	inputSyncReconnectStep   = 300 * time.Millisecond
	inputSyncTargetRefresh   = 2 * time.Second
	inputSyncActionQueueSize = 1024
	inputSyncMaxActionErrors = 5
	inputSyncMaxResyncTries  = 2
	inputSyncResyncCooldown  = 30 * time.Second

	inputSyncPauseKindChallenge       = "challenge"
	inputSyncPauseKindCDPDisconnected = "cdp_disconnected"
	inputSyncPauseKindDOMMismatch     = "dom_mismatch"
	inputSyncPauseKindURLDeviation    = "url_deviation"
	inputSyncPauseKindActionFailures  = "action_failures"

	inputSyncRecoverCheckInterval = 1 * time.Second
	inputSyncMaxRecoverAttempts   = 5
)

var inputSyncRecoverBackoffs = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
}

type BrowserInputSyncStartRequest struct {
	MasterProfileId  string   `json:"masterProfileId"`
	TargetProfileIds []string `json:"targetProfileIds"`
	Mode             string   `json:"mode"`
}

type BrowserInputSyncTargetStatus struct {
	ProfileId           string `json:"profileId"`
	ProfileName         string `json:"profileName"`
	DebugPort           int    `json:"debugPort"`
	Ready               bool   `json:"ready"`
	Paused              bool   `json:"paused"`
	PauseReason         string `json:"pauseReason,omitempty"`
	PauseKind           string `json:"pauseKind,omitempty"`
	Recoverable         bool   `json:"recoverable"`
	RecoverAttemptCount int    `json:"recoverAttemptCount"`
	NextRecoverAt       string `json:"nextRecoverAt,omitempty"`
	FailureCount        int    `json:"failureCount"`
	DroppedActionCount  int64  `json:"droppedActionCount"`
	Warning             string `json:"warning,omitempty"`
}

type BrowserInputSyncStatus struct {
	Running          bool                           `json:"running"`
	SessionId        string                         `json:"sessionId,omitempty"`
	Mode             string                         `json:"mode,omitempty"`
	MasterProfileId  string                         `json:"masterProfileId,omitempty"`
	MasterName       string                         `json:"masterName,omitempty"`
	TargetProfileIds []string                       `json:"targetProfileIds"`
	StartedAt        string                         `json:"startedAt,omitempty"`
	LastEventAt      string                         `json:"lastEventAt,omitempty"`
	EventCount       int64                          `json:"eventCount"`
	Targets          []BrowserInputSyncTargetStatus `json:"targets"`
	Warning          string                         `json:"warning,omitempty"`
}

type inputSyncManager struct {
	app *App
	mu  sync.Mutex
	cur *inputSyncSession
}

type inputSyncSession struct {
	mu               sync.RWMutex
	reconnectMu      sync.Mutex
	manager          *inputSyncManager
	id               string
	mode             string
	masterProfileID  string
	masterName       string
	targetProfileIDs []string
	startedAt        time.Time
	masterDebugPort  int
	master           *inputSyncPageClient
	masterClients    map[string]*inputSyncPageClient
	masterEvents     chan inputSyncWireEvent
	targets          []*inputSyncTarget
	cancel           context.CancelFunc
	eventCount       atomic.Int64
	healthCount      atomic.Int64
	reconnectCount   atomic.Int64
	lastEventUnix    atomic.Int64
	lastHealthUnix   atomic.Int64
	warning          string
}

type inputSyncTarget struct {
	mu              sync.Mutex
	profileID       string
	name            string
	debugPort       int
	viewport        inputSyncViewport
	client          *inputSyncPageClient
	clients         map[string]*inputSyncPageClient
	activeSourceID  string
	queue           chan inputSyncAction
	warning         string
	paused          bool
	suspendReason   string
	pauseKind       string
	recoverable     bool
	recoverAttempts int
	nextRecoverAt   time.Time
	recovering      bool
	failureCount    int
	lastFailedSeq   int64
	expectedURL     string
	resyncURL       string
	resyncFailures  int
	lastResyncAt    time.Time
	appliedActions  atomic.Int64
	failedActions   atomic.Int64
	droppedActions  atomic.Int64
}

type inputSyncTargetPauseSnapshot struct {
	paused          bool
	reason          string
	kind            string
	recoverable     bool
	failureCount    int
	lastFailedSeq   int64
	recoverAttempts int
	nextRecoverAt   time.Time
	recovering      bool
}

type inputSyncProfileRuntime struct {
	profileID  string
	name       string
	debugPort  int
	debugReady bool
	running    bool
	warning    string
}

type inputSyncViewport struct {
	width  float64
	height float64
}

type inputSyncWireEvent struct {
	SessionId      string  `json:"sessionId"`
	Kind           string  `json:"kind"`
	Type           string  `json:"type"`
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	Modifiers      int     `json:"modifiers"`
	Button         string  `json:"button"`
	Buttons        int     `json:"buttons"`
	ClickCount     int     `json:"clickCount"`
	DeltaX         float64 `json:"deltaX"`
	DeltaY         float64 `json:"deltaY"`
	Key            string  `json:"key"`
	Code           string  `json:"code"`
	Text           string  `json:"text"`
	UnmodifiedText string  `json:"unmodifiedText"`
	WindowsKeyCode int     `json:"windowsVirtualKeyCode"`
	NativeKeyCode  int     `json:"nativeVirtualKeyCode"`
	AutoRepeat     bool    `json:"autoRepeat"`
	Location       int     `json:"location"`
	IsSystemKey    bool    `json:"isSystemKey"`
	Selector       string  `json:"selector,omitempty"`
	FrameSelector  string  `json:"frameSelector,omitempty"`
	RelX           float64 `json:"relX,omitempty"`
	RelY           float64 `json:"relY,omitempty"`
	ElementX       float64 `json:"elementX,omitempty"`
	ElementY       float64 `json:"elementY,omitempty"`
	ElementWidth   float64 `json:"elementWidth,omitempty"`
	ElementHeight  float64 `json:"elementHeight,omitempty"`
	Value          string  `json:"value,omitempty"`
	Checked        bool    `json:"checked,omitempty"`
	HasChecked     bool    `json:"hasChecked,omitempty"`
	SelectionStart int     `json:"selectionStart,omitempty"`
	SelectionEnd   int     `json:"selectionEnd,omitempty"`
	ScrollLeft     float64 `json:"scrollLeft,omitempty"`
	ScrollTop      float64 `json:"scrollTop,omitempty"`
	URL            string  `json:"url,omitempty"`
	Title          string  `json:"title,omitempty"`
	SentAt         int64   `json:"sentAt,omitempty"`
	SourceTargetId string  `json:"sourceTargetId,omitempty"`
}

type inputSyncAction struct {
	Sequence       int64
	SessionID      string
	Kind           string
	Type           string
	SourceTargetID string
	Event          inputSyncWireEvent
	CreatedAt      time.Time
}

type inputSyncPageClient struct {
	role           string
	profileID      string
	profileName    string
	debugPort      int
	targetID       string
	targetURL      string
	targetTitle    string
	mu             sync.Mutex
	browser        *rod.Browser
	page           *rod.Page
	ws             *rodcdp.WebSocket
	cancel         context.CancelFunc
	events         chan inputSyncWireEvent
	done           chan struct{}
	once           sync.Once
	closing        atomic.Bool
	droppedEvents  atomic.Int64
	healthAcks     atomic.Int64
	bridgeScriptID proto.PageScriptIdentifier
}

type inputSyncPageTarget struct {
	id    string
	title string
	url   string
	wsURL string
}

func newInputSyncManager(app *App) *inputSyncManager {
	return &inputSyncManager{app: app}
}

func (a *App) BrowserInputSyncStart(req BrowserInputSyncStartRequest) (*BrowserInputSyncStatus, error) {
	if a.inputSync == nil {
		a.inputSync = newInputSyncManager(a)
	}
	return a.inputSync.Start(req)
}

func (a *App) BrowserInputSyncStop() (*BrowserInputSyncStatus, error) {
	if a.inputSync == nil {
		return (&inputSyncManager{}).Status(), nil
	}
	return a.inputSync.Stop()
}

func (a *App) BrowserInputSyncStatus() *BrowserInputSyncStatus {
	if a.inputSync == nil {
		return (&inputSyncManager{}).Status()
	}
	return a.inputSync.Status()
}

func (a *App) BrowserInputSyncRunSpecialMode() (*BrowserInputSyncStatus, error) {
	status := a.BrowserInputSyncStatus()
	return status, fmt.Errorf("窗口级同步需要系统级输入注入权限；当前版本已启用默认网页同步，窗口级同步入口已保留但尚未执行注入")
}

func (a *App) BrowserInputSyncResumeTarget(profileID string) (*BrowserInputSyncStatus, error) {
	if a.inputSync == nil {
		return (&inputSyncManager{}).Status(), fmt.Errorf("输入同步未运行")
	}
	return a.inputSync.ResumeTarget(profileID)
}

func (a *App) BrowserInputSyncResumeRecoverableTargets() (*BrowserInputSyncStatus, error) {
	if a.inputSync == nil {
		return (&inputSyncManager{}).Status(), fmt.Errorf("输入同步未运行")
	}
	return a.inputSync.ResumeRecoverableTargets()
}

func (m *inputSyncManager) Start(req BrowserInputSyncStartRequest) (*BrowserInputSyncStatus, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("输入同步服务未初始化")
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = inputSyncModePage
	}
	if mode != inputSyncModePage {
		return nil, fmt.Errorf("暂不支持同步模式：%s", mode)
	}

	masterID := strings.TrimSpace(req.MasterProfileId)
	targetIDs := normalizeInputSyncIDs(req.TargetProfileIds, masterID)
	if masterID == "" {
		return nil, fmt.Errorf("输入同步启动失败：主控实例不能为空")
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("输入同步启动失败：至少选择一个目标实例")
	}

	if _, err := m.Stop(); err != nil {
		return nil, err
	}

	master, targets, err := m.app.snapshotInputSyncProfiles(masterID, targetIDs)
	if err != nil {
		return nil, err
	}
	if inputSyncHasDebugWaitCandidate(master, targets) {
		m.app.waitForInputSyncDebugReady(master, targets)
		master, targets, err = m.app.snapshotInputSyncProfiles(masterID, targetIDs)
		if err != nil {
			return nil, err
		}
	}
	if !master.running || !master.debugReady || master.debugPort <= 0 {
		return nil, fmt.Errorf("输入同步启动失败：主控实例 %s 不可用：%s", inputSyncProfileLabel(master), inputSyncProfileNotReadyWarning(master))
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &inputSyncSession{
		manager:          m,
		id:               uuid.NewString(),
		mode:             mode,
		masterProfileID:  master.profileID,
		masterName:       master.name,
		targetProfileIDs: targetIDs,
		startedAt:        time.Now(),
		masterDebugPort:  master.debugPort,
		cancel:           cancel,
		masterClients:    make(map[string]*inputSyncPageClient),
		masterEvents:     make(chan inputSyncWireEvent, 512),
		targets:          make([]*inputSyncTarget, 0, len(targets)),
	}
	success := false
	defer func() {
		if !success {
			session.close()
		}
	}()

	masterTargets, err := listInputSyncPageTargets(master.debugPort)
	if err != nil {
		return nil, fmt.Errorf("输入同步启动失败：主控页面目标读取失败：%w", err)
	}
	if len(masterTargets) == 0 {
		return nil, fmt.Errorf("输入同步启动失败：主控没有可同步的页面目标")
	}
	masterClient, masterViewport, err := openInputSyncPageClientForTarget("master", master.profileID, master.name, master.debugPort, masterTargets[0], true)
	if err != nil {
		return nil, fmt.Errorf("输入同步启动失败：主控页面连接失败：%w", err)
	}
	session.master = masterClient
	logInputSyncTrace("输入同步主控页面已连接",
		logger.F("session_id", session.id),
		logger.F("profile_id", master.profileID),
		logger.F("profile_name", master.name),
		logger.F("debug_port", master.debugPort),
		logger.F("target_id", masterClient.targetID),
		logger.F("target_url", masterClient.targetURL),
		logger.F("viewport", formatInputSyncViewport(masterViewport)),
	)

	for _, targetProfile := range targets {
		targetStatus := &inputSyncTarget{
			profileID: targetProfile.profileID,
			name:      targetProfile.name,
			debugPort: targetProfile.debugPort,
			clients:   make(map[string]*inputSyncPageClient),
			queue:     make(chan inputSyncAction, inputSyncActionQueueSize),
		}
		if targetProfile.warning != "" {
			targetStatus.warning = targetProfile.warning
			session.targets = append(session.targets, targetStatus)
			continue
		}
		if !targetProfile.running || !targetProfile.debugReady || targetProfile.debugPort <= 0 {
			targetStatus.warning = inputSyncProfileNotReadyWarning(targetProfile)
			session.targets = append(session.targets, targetStatus)
			continue
		}
		client, viewport, targetErr := openInputSyncPageClient("target", targetProfile.profileID, targetProfile.name, targetProfile.debugPort, false)
		if targetErr != nil {
			targetStatus.warning = fmt.Sprintf("页面连接失败：%v", targetErr)
			session.targets = append(session.targets, targetStatus)
			continue
		}
		targetStatus.bindClient(masterClient.targetID, client)
		targetStatus.viewport = viewport
		logInputSyncTrace("输入同步目标页面已连接",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", targetProfile.profileID),
			logger.F("target_name", targetProfile.name),
			logger.F("target_debug_port", targetProfile.debugPort),
			logger.F("target_viewport", formatInputSyncViewport(viewport)),
		)
		if !inputSyncViewportMatches(masterViewport, viewport) {
			logger.New("InputSync").Warn("输入同步目标视口尺寸不一致，已继续同步",
				logger.F("session_id", session.id),
				logger.F("master_profile_id", master.profileID),
				logger.F("master_name", master.name),
				logger.F("master_debug_port", master.debugPort),
				logger.F("master_viewport", formatInputSyncViewport(masterViewport)),
				logger.F("target_profile_id", targetProfile.profileID),
				logger.F("target_name", targetProfile.name),
				logger.F("target_debug_port", targetProfile.debugPort),
				logger.F("target_viewport", formatInputSyncViewport(viewport)),
				logger.F("sync_strategy", "dom_relative_and_state"),
			)
		}
		session.targets = append(session.targets, targetStatus)
	}

	readyTargets := session.readyTargets()
	if len(readyTargets) == 0 {
		return nil, fmt.Errorf("输入同步启动失败：没有可同步的目标实例。%s", session.targetWarningSummary())
	}

	if err = installInputSyncBridge(ctx, masterClient, session.id); err != nil {
		return nil, fmt.Errorf("输入同步启动失败：主控页面事件桥安装失败：%w", err)
	}
	session.addMasterClient(ctx, masterClient)
	logInputSyncTrace("输入同步主控事件桥已安装",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("master_name", session.masterName),
		logger.F("target_id", masterClient.targetID),
		logger.F("target_url", masterClient.targetURL),
	)

	go m.forwardEvents(ctx, session)
	go m.monitorMasterTargets(ctx, session)
	go session.runAutoRecover(ctx)
	for _, target := range readyTargets {
		go target.runActionQueue(ctx, session)
	}

	m.mu.Lock()
	m.cur = session
	status := session.status()
	m.mu.Unlock()
	success = true
	m.emitStatus(status)
	logger.New("InputSync").Info("输入同步已启动",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("master_name", session.masterName),
		logger.F("target_count", len(readyTargets)),
		logger.F("targets", describeInputSyncTargets(readyTargets)),
	)
	return status, nil
}

func (m *inputSyncManager) Stop() (*BrowserInputSyncStatus, error) {
	if m == nil {
		return (&inputSyncManager{}).Status(), nil
	}

	m.mu.Lock()
	session := m.cur
	m.cur = nil
	m.mu.Unlock()

	if session != nil {
		session.close()
		logger.New("InputSync").Info("输入同步已停止", logger.F("session_id", session.id))
	}

	status := m.Status()
	m.emitStatus(status)
	return status, nil
}

func (m *inputSyncManager) ResumeTarget(profileID string) (*BrowserInputSyncStatus, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return m.Status(), fmt.Errorf("恢复失败：实例 ID 不能为空")
	}
	session := m.currentSession()
	if session == nil {
		return m.Status(), fmt.Errorf("恢复失败：输入同步未运行")
	}
	target := session.findTarget(profileID)
	if target == nil {
		return session.status(), fmt.Errorf("恢复失败：目标实例未加入当前同步")
	}
	if err := session.resumeTarget(context.Background(), target, false); err != nil {
		status := session.status()
		m.emitStatus(status)
		return status, err
	}
	status := session.status()
	m.emitStatus(status)
	return status, nil
}

func (m *inputSyncManager) ResumeRecoverableTargets() (*BrowserInputSyncStatus, error) {
	session := m.currentSession()
	if session == nil {
		return m.Status(), fmt.Errorf("恢复失败：输入同步未运行")
	}
	targets := session.recoverableTargetSnapshot(true)
	if len(targets) == 0 {
		return session.status(), fmt.Errorf("没有可自动恢复的暂停目标")
	}
	failures := make([]string, 0)
	for _, target := range targets {
		if err := session.resumeTarget(context.Background(), target, false); err != nil {
			name := strings.TrimSpace(target.name)
			if name == "" {
				name = target.profileID
			}
			failures = append(failures, fmt.Sprintf("%s：%v", name, err))
		}
	}
	status := session.status()
	m.emitStatus(status)
	if len(failures) > 0 {
		return status, fmt.Errorf("部分目标恢复失败：%s", strings.Join(failures, "；"))
	}
	return status, nil
}

func (m *inputSyncManager) Status() *BrowserInputSyncStatus {
	if m == nil {
		return &BrowserInputSyncStatus{TargetProfileIds: []string{}, Targets: []BrowserInputSyncTargetStatus{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return &BrowserInputSyncStatus{TargetProfileIds: []string{}, Targets: []BrowserInputSyncTargetStatus{}}
	}
	return m.cur.status()
}

func (m *inputSyncManager) currentSession() *inputSyncSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

func (m *inputSyncManager) emitStatus(status *BrowserInputSyncStatus) {
	if m == nil || m.app == nil || m.app.ctx == nil || status == nil {
		return
	}
	runtime.EventsEmit(m.app.ctx, "browser:input-sync:status", status)
}

func (a *App) snapshotInputSyncProfiles(masterID string, targetIDs []string) (inputSyncProfileRuntime, []inputSyncProfileRuntime, error) {
	if a == nil || a.browserMgr == nil {
		return inputSyncProfileRuntime{}, nil, fmt.Errorf("浏览器管理器未初始化")
	}

	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()

	snapshotOne := func(profileID string) (inputSyncProfileRuntime, error) {
		profile, exists := a.browserMgr.Profiles[profileID]
		if !exists || profile == nil {
			return inputSyncProfileRuntime{}, fmt.Errorf("未找到实例配置（ID=%s）", profileID)
		}
		a.ensureProfileLaunchCode(profile)
		trackedCmd := a.browserMgr.BrowserProcesses[profileID]
		running := profile.Running && isBrowserProfileLive(profile, trackedCmd)
		if profile.Running && !running {
			a.markProfileStoppedLocked(profileID, profile)
		}
		if running && profile.DebugPort > 0 && !profile.DebugReady {
			if err := probeBrowserDebugPort(profile.DebugPort, browserDebugProbeTimeout); err == nil {
				a.markProfileDebugReadyLocked(profile, profile.DebugPort)
			}
		}
		return inputSyncProfileRuntime{
			profileID:  profile.ProfileId,
			name:       profile.ProfileName,
			debugPort:  profile.DebugPort,
			debugReady: profile.DebugReady,
			running:    running,
		}, nil
	}

	master, err := snapshotOne(masterID)
	if err != nil {
		return inputSyncProfileRuntime{}, nil, err
	}

	targets := make([]inputSyncProfileRuntime, 0, len(targetIDs))
	for _, id := range targetIDs {
		target, targetErr := snapshotOne(id)
		if targetErr != nil {
			targets = append(targets, inputSyncProfileRuntime{profileID: id, warning: targetErr.Error()})
			continue
		}
		targets = append(targets, target)
	}
	return master, targets, nil
}

func normalizeInputSyncIDs(ids []string, masterID string) []string {
	masterID = strings.TrimSpace(masterID)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || id == masterID {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type inputSyncDebugWaitCandidate struct {
	profileID string
	debugPort int
}

func inputSyncHasDebugWaitCandidate(master inputSyncProfileRuntime, targets []inputSyncProfileRuntime) bool {
	if inputSyncCanWaitDebugReady(master) {
		return true
	}
	for _, target := range targets {
		if inputSyncCanWaitDebugReady(target) {
			return true
		}
	}
	return false
}

func inputSyncCanWaitDebugReady(profile inputSyncProfileRuntime) bool {
	return profile.warning == "" && profile.running && profile.debugPort > 0 && !profile.debugReady
}

func inputSyncDebugWaitCandidates(master inputSyncProfileRuntime, targets []inputSyncProfileRuntime) []inputSyncDebugWaitCandidate {
	seen := map[string]struct{}{}
	out := make([]inputSyncDebugWaitCandidate, 0, len(targets)+1)
	appendOne := func(profile inputSyncProfileRuntime) {
		if !inputSyncCanWaitDebugReady(profile) {
			return
		}
		if _, ok := seen[profile.profileID]; ok {
			return
		}
		seen[profile.profileID] = struct{}{}
		out = append(out, inputSyncDebugWaitCandidate{profileID: profile.profileID, debugPort: profile.debugPort})
	}
	appendOne(master)
	for _, target := range targets {
		appendOne(target)
	}
	return out
}

func (a *App) waitForInputSyncDebugReady(master inputSyncProfileRuntime, targets []inputSyncProfileRuntime) {
	candidates := inputSyncDebugWaitCandidates(master, targets)
	if a == nil || len(candidates) == 0 {
		return
	}

	deadline := time.Now().Add(inputSyncDebugReadyWait)
	for len(candidates) > 0 {
		remaining := candidates[:0]
		for _, candidate := range candidates {
			if err := probeBrowserDebugPort(candidate.debugPort, browserDebugProbeTimeout); err == nil {
				a.setProfileDebugReady(candidate.profileID, candidate.debugPort)
				continue
			}
			remaining = append(remaining, candidate)
		}
		candidates = remaining
		if len(candidates) == 0 || !time.Now().Before(deadline) {
			return
		}
		sleepFor := 200 * time.Millisecond
		if untilDeadline := time.Until(deadline); untilDeadline < sleepFor {
			sleepFor = untilDeadline
		}
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}
	}
}

func inputSyncProfileLabel(profile inputSyncProfileRuntime) string {
	name := strings.TrimSpace(profile.name)
	if name != "" {
		return name
	}
	id := strings.TrimSpace(profile.profileID)
	if id != "" {
		return id
	}
	return "未知实例"
}

func inputSyncProfileNotReadyWarning(profile inputSyncProfileRuntime) string {
	if !profile.running {
		return "实例未运行或运行状态已失效"
	}
	if profile.debugPort <= 0 {
		return "调试端口为空，请稍后重试"
	}
	if !profile.debugReady {
		return fmt.Sprintf("调试接口未就绪（端口 %d），请稍后重试", profile.debugPort)
	}
	return "实例未满足同步条件"
}

func logInputSyncTrace(message string, fields ...logger.Field) {
	logger.New("InputSync").Info(message, fields...)
}

func inputSyncShouldTraceEvent(event inputSyncWireEvent, count int64) bool {
	if count <= 5 {
		return true
	}
	if event.Kind == "key" {
		return true
	}
	if event.Type != "mouseMoved" {
		return true
	}
	return count%inputSyncTraceEvery == 0
}

func inputSyncEventTrace(event inputSyncWireEvent) string {
	switch event.Kind {
	case "mouse":
		return fmt.Sprintf("kind=%s type=%s source=%s x=%.0f y=%.0f button=%s buttons=%d modifiers=%d delta=%.0f,%.0f click=%d",
			event.Kind,
			event.Type,
			event.SourceTargetId,
			event.X,
			event.Y,
			event.Button,
			event.Buttons,
			event.Modifiers,
			event.DeltaX,
			event.DeltaY,
			event.ClickCount,
		)
	case "key":
		return fmt.Sprintf("kind=%s type=%s source=%s code=%s key=%s modifiers=%d repeat=%t location=%d system=%t",
			event.Kind,
			event.Type,
			event.SourceTargetId,
			event.Code,
			sanitizeInputSyncKeyForLog(event.Key),
			event.Modifiers,
			event.AutoRepeat,
			event.Location,
			event.IsSystemKey,
		)
	default:
		return fmt.Sprintf("kind=%s type=%s", event.Kind, event.Type)
	}
}

func sanitizeInputSyncKeyForLog(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len([]rune(key)) == 1 {
		return "[printable]"
	}
	return key
}

func sanitizeInputSyncSelectorForLog(selector string) string {
	selector = strings.Join(strings.Fields(strings.TrimSpace(selector)), " ")
	if len(selector) > 160 {
		return selector[:160] + "..."
	}
	return selector
}

func sanitizeInputSyncURLForLog(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if len(rawURL) > 220 {
		return rawURL[:220] + "..."
	}
	return rawURL
}

func sanitizeInputSyncTitleForLog(title string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	if len(title) > 120 {
		return title[:120] + "..."
	}
	return title
}

func inputSyncTargetStatusWarning(warning string, pauseReason string, lastFailedSeq int64) string {
	if strings.TrimSpace(pauseReason) != "" {
		return pauseReason
	}
	if strings.TrimSpace(warning) != "" {
		return warning
	}
	if lastFailedSeq > 0 {
		return fmt.Sprintf("最近一次失败 Action：#%d", lastFailedSeq)
	}
	return ""
}

func inputSyncJSONLiteral(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(data)
}

func inputSyncCanNavigate(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	return lower != "" && !strings.HasPrefix(lower, "javascript:")
}

func normalizeInputSyncURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), "#")
}

func inputSyncErrorNeedsReconnect(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	reconnectSignals := []string{
		"cdp connection closed",
		"context deadline exceeded",
		"i/o timeout",
		"websocket",
		"unexpected eof",
		"connection refused",
		"connection reset",
		"broken pipe",
		"target closed",
		"session closed",
		"not attached",
		"no such target",
		"detached",
	}
	for _, signal := range reconnectSignals {
		if strings.Contains(message, signal) {
			return true
		}
	}
	return false
}

func inputSyncPauseKindForActionError(err error) string {
	if inputSyncErrorNeedsReconnect(err) {
		return inputSyncPauseKindCDPDisconnected
	}
	if inputSyncErrorLooksDOMMismatch(err) {
		return inputSyncPauseKindDOMMismatch
	}
	return inputSyncPauseKindActionFailures
}

func inputSyncErrorLooksDOMMismatch(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	signals := []string{
		"dom",
		"selector",
		"目标元素",
		"目标滚动容器",
		"缺少可定位",
		"不存在或不可见",
		"不存在或无法写入",
	}
	for _, signal := range signals {
		if strings.Contains(message, signal) {
			return true
		}
	}
	return false
}

func inputSyncRecoverBackoffForAttempt(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if len(inputSyncRecoverBackoffs) == 0 {
		return 30 * time.Second
	}
	if attempt >= len(inputSyncRecoverBackoffs) {
		return inputSyncRecoverBackoffs[len(inputSyncRecoverBackoffs)-1]
	}
	return inputSyncRecoverBackoffs[attempt]
}

func inputSyncDetectChallengeSignal(rawURL string, title string, body string) (bool, string) {
	urlText := strings.ToLower(strings.TrimSpace(rawURL))
	titleText := strings.ToLower(strings.TrimSpace(title))
	bodyText := strings.ToLower(strings.TrimSpace(body))
	strongSignals := []struct {
		token string
		label string
	}{
		{token: "recaptcha", label: "reCAPTCHA"},
		{token: "hcaptcha", label: "hCaptcha"},
		{token: "captcha", label: "captcha"},
		{token: "sorry/index", label: "Google unusual traffic"},
		{token: "google.com/sorry", label: "Google unusual traffic"},
		{token: "unusual traffic", label: "unusual traffic"},
		{token: "verify you are human", label: "verify you are human"},
		{token: "not a robot", label: "not a robot"},
		{token: "are you a robot", label: "are you a robot"},
		{token: "our systems have detected unusual traffic", label: "Google unusual traffic"},
		{token: "cf-challenge", label: "Cloudflare challenge"},
		{token: "turnstile", label: "Cloudflare Turnstile"},
		{token: "security check", label: "security check"},
		{token: "人机验证", label: "人机验证"},
		{token: "验证您不是机器人", label: "验证您不是机器人"},
		{token: "请输入验证码", label: "验证码"},
	}
	for _, signal := range strongSignals {
		if strings.Contains(urlText, signal.token) || strings.Contains(titleText, signal.token) {
			return true, fmt.Sprintf("疑似验证码/风控页面（%s），已暂停该实例，需要人工处理或重启同步", signal.label)
		}
	}
	bodySignals := []struct {
		token string
		label string
	}{
		{token: "verify you are human", label: "verify you are human"},
		{token: "not a robot", label: "not a robot"},
		{token: "are you a robot", label: "are you a robot"},
		{token: "our systems have detected unusual traffic", label: "Google unusual traffic"},
		{token: "unusual traffic", label: "unusual traffic"},
		{token: "人机验证", label: "人机验证"},
		{token: "验证您不是机器人", label: "验证您不是机器人"},
		{token: "请输入验证码", label: "验证码"},
	}
	for _, signal := range bodySignals {
		if strings.Contains(bodyText, signal.token) {
			return true, fmt.Sprintf("疑似验证码/风控页面（%s），已暂停该实例，需要人工处理或重启同步", signal.label)
		}
	}
	hasCaptchaWord := strings.Contains(bodyText, "captcha") || strings.Contains(bodyText, "recaptcha") || strings.Contains(bodyText, "hcaptcha")
	hasChallengeWord := strings.Contains(bodyText, "verify") ||
		strings.Contains(bodyText, "human") ||
		strings.Contains(bodyText, "robot") ||
		strings.Contains(bodyText, "challenge") ||
		strings.Contains(bodyText, "unusual traffic") ||
		strings.Contains(bodyText, "security check")
	if hasCaptchaWord && hasChallengeWord {
		return true, "疑似验证码/风控页面（captcha），已暂停该实例，需要人工处理或重启同步"
	}
	return false, ""
}

func formatInputSyncViewport(viewport inputSyncViewport) string {
	return fmt.Sprintf("%.0fx%.0f", viewport.width, viewport.height)
}

func describeInputSyncTargets(targets []*inputSyncTarget) string {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == nil {
			continue
		}
		name := strings.TrimSpace(target.name)
		if name == "" {
			name = strings.TrimSpace(target.profileID)
		}
		parts = append(parts, fmt.Sprintf("%s(port=%d,viewport=%s)", name, target.debugPort, formatInputSyncViewport(target.viewport)))
	}
	return strings.Join(parts, "; ")
}

func (s *inputSyncSession) readyTargets() []*inputSyncTarget {
	if s == nil {
		return nil
	}
	out := make([]*inputSyncTarget, 0, len(s.targets))
	for _, target := range s.targets {
		if target != nil && target.getClient() != nil && !target.isPaused() {
			out = append(out, target)
		}
	}
	return out
}

func (s *inputSyncSession) dispatchTargets() []*inputSyncTarget {
	if s == nil {
		return nil
	}
	out := make([]*inputSyncTarget, 0, len(s.targets))
	for _, target := range s.targets {
		if target == nil || target.queue == nil {
			continue
		}
		if target.getClient() != nil || target.isPaused() {
			out = append(out, target)
		}
	}
	return out
}

func (s *inputSyncSession) status() *BrowserInputSyncStatus {
	if s == nil {
		return &BrowserInputSyncStatus{TargetProfileIds: []string{}, Targets: []BrowserInputSyncTargetStatus{}}
	}
	targets := make([]BrowserInputSyncTargetStatus, 0, len(s.targets))
	for _, target := range s.targets {
		if target == nil {
			continue
		}
		pause := target.pauseSnapshot()
		nextRecoverAt := ""
		if !pause.nextRecoverAt.IsZero() {
			nextRecoverAt = pause.nextRecoverAt.Format(time.RFC3339)
		}
		targets = append(targets, BrowserInputSyncTargetStatus{
			ProfileId:           target.profileID,
			ProfileName:         target.name,
			DebugPort:           target.debugPort,
			Ready:               target.getClient() != nil && !pause.paused,
			Paused:              pause.paused,
			PauseReason:         pause.reason,
			PauseKind:           pause.kind,
			Recoverable:         pause.recoverable,
			RecoverAttemptCount: pause.recoverAttempts,
			NextRecoverAt:       nextRecoverAt,
			FailureCount:        pause.failureCount,
			DroppedActionCount:  target.droppedActions.Load(),
			Warning:             inputSyncTargetStatusWarning(target.getWarning(), pause.reason, pause.lastFailedSeq),
		})
	}
	status := &BrowserInputSyncStatus{
		Running:          true,
		SessionId:        s.id,
		Mode:             s.mode,
		MasterProfileId:  s.masterProfileID,
		MasterName:       s.masterName,
		TargetProfileIds: append([]string{}, s.targetProfileIDs...),
		StartedAt:        s.startedAt.Format(time.RFC3339),
		EventCount:       s.eventCount.Load(),
		Targets:          targets,
		Warning:          s.warning,
	}
	if last := s.lastEventUnix.Load(); last > 0 {
		status.LastEventAt = time.UnixMilli(last).Format(time.RFC3339)
	}
	return status
}

func (s *inputSyncSession) targetWarningSummary() string {
	if s == nil || len(s.targets) == 0 {
		return "请确认目标实例已运行且 CDP 已就绪。"
	}
	parts := make([]string, 0, len(s.targets))
	for _, target := range s.targets {
		if target == nil {
			continue
		}
		name := strings.TrimSpace(target.name)
		if name == "" {
			name = strings.TrimSpace(target.profileID)
		}
		warning := target.getWarning()
		if warning == "" {
			warning = "未加入同步"
		}
		parts = append(parts, fmt.Sprintf("%s：%s", name, warning))
	}
	if len(parts) == 0 {
		return "请确认目标实例已运行且 CDP 已就绪。"
	}
	if len(parts) > 3 {
		return fmt.Sprintf("%s；另有 %d 个目标不可用。", strings.Join(parts[:3], "；"), len(parts)-3)
	}
	return strings.Join(parts, "；")
}

func (s *inputSyncSession) findTarget(profileID string) *inputSyncTarget {
	if s == nil {
		return nil
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, target := range s.targets {
		if target != nil && target.profileID == profileID {
			return target
		}
	}
	return nil
}

func (s *inputSyncSession) recoverableTargetSnapshot(includeCoolingDown bool) []*inputSyncTarget {
	if s == nil {
		return nil
	}
	now := time.Now()
	out := make([]*inputSyncTarget, 0)
	s.mu.RLock()
	targets := append([]*inputSyncTarget{}, s.targets...)
	s.mu.RUnlock()
	for _, target := range targets {
		if target == nil {
			continue
		}
		pause := target.pauseSnapshot()
		if !pause.paused || !pause.recoverable || pause.recovering {
			continue
		}
		if !includeCoolingDown && !pause.nextRecoverAt.IsZero() && pause.nextRecoverAt.After(now) {
			continue
		}
		out = append(out, target)
	}
	return out
}

func (s *inputSyncSession) runAutoRecover(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(inputSyncRecoverCheckInterval)
	defer ticker.Stop()
	logInputSyncTrace("输入同步自动恢复循环已启动", logger.F("session_id", s.id))
	for {
		select {
		case <-ctx.Done():
			logInputSyncTrace("输入同步自动恢复循环已停止",
				logger.F("session_id", s.id),
				logger.F("reason", "context_done"),
			)
			return
		case <-ticker.C:
			for _, target := range s.recoverableTargetSnapshot(false) {
				if ctx.Err() != nil {
					return
				}
				if err := s.resumeTarget(ctx, target, true); err != nil {
					logger.New("InputSync").Warn("输入同步从浏览器自动恢复暂未成功",
						logger.F("session_id", s.id),
						logger.F("target_profile_id", target.profileID),
						logger.F("target_name", target.name),
						logger.F("target_debug_port", target.debugPort),
						logger.F("error", err.Error()),
					)
				}
			}
		}
	}
}

func (s *inputSyncSession) resumeTarget(ctx context.Context, target *inputSyncTarget, automatic bool) error {
	if s == nil || target == nil {
		return fmt.Errorf("恢复失败：目标不存在")
	}
	pause := target.pauseSnapshot()
	if !pause.paused {
		return nil
	}
	if automatic {
		if !pause.recoverable {
			return nil
		}
		if !pause.nextRecoverAt.IsZero() && pause.nextRecoverAt.After(time.Now()) {
			return nil
		}
	}
	attempt, err := target.beginRecoverAttempt(automatic, time.Now())
	if err != nil {
		return err
	}
	if attempt == 0 {
		return nil
	}
	err = s.recoverTargetClientAndState(ctx, target)
	if err != nil {
		finalReason := target.finishRecoverFailure(err, time.Now())
		if finalReason != "" {
			logger.New("InputSync").Warn("输入同步从浏览器恢复失败次数已达上限，保持暂停",
				logger.F("session_id", s.id),
				logger.F("target_profile_id", target.profileID),
				logger.F("target_name", target.name),
				logger.F("target_debug_port", target.debugPort),
				logger.F("recover_attempt", attempt),
				logger.F("reason", finalReason),
				logger.F("error", err.Error()),
			)
		}
		if s.manager != nil {
			s.manager.emitStatus(s.status())
		}
		return err
	}
	target.finishRecoverSuccess()
	logger.New("InputSync").Info("输入同步从浏览器已恢复",
		logger.F("session_id", s.id),
		logger.F("target_profile_id", target.profileID),
		logger.F("target_name", target.name),
		logger.F("target_debug_port", target.debugPort),
		logger.F("recover_attempt", attempt),
	)
	if s.manager != nil {
		s.manager.emitStatus(s.status())
	}
	return nil
}

func (s *inputSyncSession) recoverTargetClientAndState(ctx context.Context, target *inputSyncTarget) error {
	if s == nil || target == nil {
		return fmt.Errorf("恢复失败：目标不存在")
	}
	if s.manager != nil && s.manager.app != nil {
		_, targets, err := s.manager.app.snapshotInputSyncProfiles(s.masterProfileID, []string{target.profileID})
		if err == nil && len(targets) > 0 {
			runtimeTarget := targets[0]
			if runtimeTarget.warning != "" {
				return fmt.Errorf("%s", runtimeTarget.warning)
			}
			if !runtimeTarget.running || !runtimeTarget.debugReady || runtimeTarget.debugPort <= 0 {
				return fmt.Errorf("%s", inputSyncProfileNotReadyWarning(runtimeTarget))
			}
			target.mu.Lock()
			target.name = runtimeTarget.name
			target.debugPort = runtimeTarget.debugPort
			target.mu.Unlock()
		} else if err != nil {
			return err
		}
	}

	target.mu.Lock()
	profileID := target.profileID
	name := target.name
	debugPort := target.debugPort
	expectedURL := strings.TrimSpace(target.expectedURL)
	target.mu.Unlock()
	if debugPort <= 0 {
		return fmt.Errorf("目标调试端口不可用")
	}
	client, viewport, err := openInputSyncPageClient("target", profileID, name, debugPort, false)
	if err != nil {
		return err
	}
	clientAccepted := false
	defer func() {
		if !clientAccepted {
			client.Close()
		}
	}()
	detectCtx, detectCancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
	detected, reason, detectErr := client.DetectChallenge(detectCtx)
	detectCancel()
	if detectErr == nil && detected {
		target.pauseWithKind(inputSyncPauseKindChallenge, reason, false)
		return fmt.Errorf("%s", reason)
	}
	if detectErr != nil {
		logger.New("InputSync").Debug("输入同步恢复时验证码/风控检测跳过",
			logger.F("session_id", s.id),
			logger.F("target_profile_id", profileID),
			logger.F("target_name", name),
			logger.F("target_debug_port", debugPort),
			logger.F("error", detectErr.Error()),
		)
	}
	previous := target.replaceClient(client)
	clientAccepted = true
	target.viewport = viewport
	if previous != nil && previous != client {
		previous.Close()
	}
	if expectedURL != "" {
		navCtx, navCancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
		currentURL, currentErr := client.CurrentURL(navCtx)
		navCancel()
		if currentErr != nil {
			return currentErr
		}
		if normalizeInputSyncURL(currentURL) != normalizeInputSyncURL(expectedURL) {
			if err := client.ApplyAction(inputSyncAction{
				SessionID: s.id,
				Kind:      "navigation",
				Type:      "recover",
				Event:     inputSyncWireEvent{URL: expectedURL},
				CreatedAt: time.Now(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *inputSyncSession) close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	for _, master := range s.masterClientSnapshot() {
		if master != nil {
			ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
			_ = uninstallInputSyncBridge(ctx, master)
			cancel()
			master.Close()
		}
	}
	for _, target := range s.targets {
		if target != nil {
			for _, client := range target.clientSnapshot() {
				client.Close()
			}
		}
	}
}

func (s *inputSyncSession) getMaster() *inputSyncPageClient {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.master
}

func (s *inputSyncSession) masterClientSnapshot() []*inputSyncPageClient {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	clients := make([]*inputSyncPageClient, 0, len(s.masterClients))
	for _, client := range s.masterClients {
		if client != nil {
			clients = append(clients, client)
		}
	}
	if len(clients) == 0 && s.master != nil {
		clients = append(clients, s.master)
	}
	return clients
}

func (s *inputSyncSession) hasMasterClient(targetID string) bool {
	if s == nil || strings.TrimSpace(targetID) == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	client := s.masterClients[targetID]
	return client != nil && !client.isClosed()
}

func (s *inputSyncSession) addMasterClient(ctx context.Context, client *inputSyncPageClient) {
	if s == nil || client == nil {
		return
	}
	targetID := strings.TrimSpace(client.targetID)
	if targetID == "" {
		targetID = client.targetURL
	}
	if targetID == "" {
		targetID = uuid.NewString()
	}

	s.mu.Lock()
	previous := s.masterClients[targetID]
	s.masterClients[targetID] = client
	if s.master == nil || s.master.isClosed() {
		s.master = client
	}
	s.mu.Unlock()

	if previous != nil && previous != client {
		previous.Close()
	}
	go s.forwardMasterClientEvents(ctx, client)
}

func (s *inputSyncSession) removeMasterClient(targetID string, client *inputSyncPageClient) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if existing := s.masterClients[targetID]; existing == client {
		delete(s.masterClients, targetID)
	}
	if s.master == client {
		s.master = nil
		for _, next := range s.masterClients {
			if next != nil && !next.isClosed() {
				s.master = next
				break
			}
		}
	}
	s.mu.Unlock()
}

func (s *inputSyncSession) forwardMasterClientEvents(ctx context.Context, client *inputSyncPageClient) {
	if s == nil || client == nil {
		return
	}
	targetID := client.targetID
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			s.removeMasterClient(targetID, client)
			logInputSyncTrace("输入同步主控页面目标已断开",
				logger.F("session_id", s.id),
				logger.F("target_id", client.targetID),
				logger.F("target_url", client.targetURL),
				logger.F("profile_id", client.profileID),
				logger.F("profile_name", client.profileName),
			)
			return
		case event, ok := <-client.events:
			if !ok {
				s.removeMasterClient(targetID, client)
				return
			}
			event.SourceTargetId = client.targetID
			if event.Kind == "health" {
				client.healthAcks.Add(1)
			}
			select {
			case s.masterEvents <- event:
			default:
				dropped := client.droppedEvents.Add(1)
				if dropped <= 3 || dropped%inputSyncTraceEvery == 0 {
					logger.New("InputSync").Warn("输入同步主控聚合事件队列已满，事件被丢弃",
						logger.F("session_id", s.id),
						logger.F("target_id", client.targetID),
						logger.F("target_url", client.targetURL),
						logger.F("dropped", dropped),
						logger.F("event", inputSyncEventTrace(event)),
					)
				}
			}
		}
	}
}

func (s *inputSyncSession) replaceMaster(next *inputSyncPageClient) *inputSyncPageClient {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	previous := s.master
	s.master = next
	s.mu.Unlock()
	return previous
}

func (t *inputSyncTarget) getClient() *inputSyncPageClient {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.client
}

func (t *inputSyncTarget) bindClient(sourceTargetID string, client *inputSyncPageClient) {
	if t == nil || client == nil {
		return
	}
	sourceTargetID = strings.TrimSpace(sourceTargetID)
	t.mu.Lock()
	if t.clients == nil {
		t.clients = make(map[string]*inputSyncPageClient)
	}
	if sourceTargetID != "" {
		t.clients[sourceTargetID] = client
		t.activeSourceID = sourceTargetID
	}
	t.client = client
	t.warning = ""
	t.mu.Unlock()
}

func (t *inputSyncTarget) clientForAction(action inputSyncAction) *inputSyncPageClient {
	if t == nil {
		return nil
	}
	sourceTargetID := strings.TrimSpace(action.SourceTargetID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if sourceTargetID != "" {
		if client := t.clients[sourceTargetID]; client != nil && !client.isClosed() {
			t.activeSourceID = sourceTargetID
			t.client = client
			return client
		}
	}
	return t.client
}

func (t *inputSyncTarget) exactClientForSource(sourceTargetID string) *inputSyncPageClient {
	if t == nil || strings.TrimSpace(sourceTargetID) == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clients[strings.TrimSpace(sourceTargetID)]
}

func (t *inputSyncTarget) clientSnapshot() []*inputSyncPageClient {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[*inputSyncPageClient]struct{}{}
	out := make([]*inputSyncPageClient, 0, len(t.clients)+1)
	for _, client := range t.clients {
		if client == nil {
			continue
		}
		if _, ok := seen[client]; ok {
			continue
		}
		seen[client] = struct{}{}
		out = append(out, client)
	}
	if t.client != nil {
		if _, ok := seen[t.client]; !ok {
			out = append(out, t.client)
		}
	}
	return out
}

func (t *inputSyncTarget) unbindClient(sourceTargetID string) *inputSyncPageClient {
	if t == nil || strings.TrimSpace(sourceTargetID) == "" {
		return nil
	}
	sourceTargetID = strings.TrimSpace(sourceTargetID)
	t.mu.Lock()
	client := t.clients[sourceTargetID]
	delete(t.clients, sourceTargetID)
	if t.activeSourceID == sourceTargetID {
		t.activeSourceID = ""
		t.client = nil
		for nextSource, next := range t.clients {
			if next != nil && !next.isClosed() {
				t.activeSourceID = nextSource
				t.client = next
				break
			}
		}
	}
	t.mu.Unlock()
	return client
}

func (t *inputSyncTarget) replaceClient(next *inputSyncPageClient) *inputSyncPageClient {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	previous := t.client
	t.client = next
	if t.clients == nil {
		t.clients = make(map[string]*inputSyncPageClient)
	}
	if t.activeSourceID != "" {
		t.clients[t.activeSourceID] = next
	}
	t.warning = ""
	t.mu.Unlock()
	return previous
}

func (m *inputSyncManager) forwardEvents(ctx context.Context, s *inputSyncSession) {
	if s == nil || s.masterEvents == nil {
		return
	}
	logInputSyncTrace("输入同步事件转发循环已启动",
		logger.F("session_id", s.id),
		logger.F("master_profile_id", s.masterProfileID),
		logger.F("ready_targets", len(s.readyTargets())),
	)
	for {
		select {
		case <-ctx.Done():
			logInputSyncTrace("输入同步事件转发循环已停止",
				logger.F("session_id", s.id),
				logger.F("reason", "context_done"),
				logger.F("event_count", s.eventCount.Load()),
			)
			return
		case event, ok := <-s.masterEvents:
			if !ok {
				m.markSessionBroken(s, "主控聚合事件通道已关闭，同步已停止")
				return
			}
			if event.SessionId != s.id {
				logInputSyncTrace("输入同步忽略非当前会话事件",
					logger.F("session_id", s.id),
					logger.F("event_session_id", event.SessionId),
					logger.F("event", inputSyncEventTrace(event)),
				)
				continue
			}
			if event.Kind == "health" {
				healthCount := s.healthCount.Add(1)
				s.lastHealthUnix.Store(time.Now().UnixMilli())
				if healthCount <= 3 || healthCount%inputSyncHealthLogEvery == 0 {
					logInputSyncTrace("输入同步主控事件桥心跳已回传",
						logger.F("session_id", s.id),
						logger.F("health_count", healthCount),
						logger.F("user_event_count", s.eventCount.Load()),
					)
				}
				continue
			}
			count := s.eventCount.Add(1)
			s.lastEventUnix.Store(time.Now().UnixMilli())
			if inputSyncShouldTraceEvent(event, count) {
				logInputSyncTrace("输入同步收到主控事件",
					logger.F("session_id", s.id),
					logger.F("event_count", count),
					logger.F("event", inputSyncEventTrace(event)),
					logger.F("source_target_id", event.SourceTargetId),
					logger.F("ready_targets", len(s.readyTargets())),
				)
			}
			s.dispatch(event, count)
		}
	}
}

func (s *inputSyncSession) dispatch(event inputSyncWireEvent, eventCount int64) {
	action, ok := inputSyncActionFromEvent(event, eventCount)
	if !ok {
		logInputSyncTrace("输入同步跳过不可映射事件",
			logger.F("session_id", s.id),
			logger.F("event_count", eventCount),
			logger.F("event", inputSyncEventTrace(event)),
		)
		return
	}
	readyTargets := s.readyTargets()
	if inputSyncShouldTraceAction(action) {
		logInputSyncTrace("输入同步控制器广播 Action",
			logger.F("session_id", s.id),
			logger.F("action_seq", action.Sequence),
			logger.F("action", inputSyncActionTrace(action)),
			logger.F("target_count", len(readyTargets)),
		)
	}
	for _, target := range s.dispatchTargets() {
		if !target.enqueueAction(action) {
			dropped := target.droppedActions.Load()
			if dropped <= 3 || dropped%inputSyncTraceEvery == 0 {
				message := "输入同步从浏览器 Action 队列已满，已丢弃"
				if target.isPaused() {
					message = "输入同步从浏览器已暂停，Action 已丢弃"
				}
				logger.New("InputSync").Warn(message,
					logger.F("session_id", s.id),
					logger.F("target_profile_id", target.profileID),
					logger.F("target_name", target.name),
					logger.F("target_debug_port", target.debugPort),
					logger.F("dropped", dropped),
					logger.F("action_seq", action.Sequence),
					logger.F("action", inputSyncActionTrace(action)),
				)
			}
		}
	}
}

func (s *inputSyncSession) dispatchSystemEvent(event inputSyncWireEvent) {
	if s == nil {
		return
	}
	event.SessionId = s.id
	count := s.eventCount.Add(1)
	s.lastEventUnix.Store(time.Now().UnixMilli())
	if inputSyncShouldTraceEvent(event, count) {
		logInputSyncTrace("输入同步控制器产生系统事件",
			logger.F("session_id", s.id),
			logger.F("event_count", count),
			logger.F("event", inputSyncEventTrace(event)),
		)
	}
	s.dispatch(event, count)
}

func inputSyncActionFromEvent(event inputSyncWireEvent, sequence int64) (inputSyncAction, bool) {
	kind := strings.TrimSpace(event.Kind)
	if kind == "" || kind == "health" {
		return inputSyncAction{}, false
	}
	action := inputSyncAction{
		Sequence:       sequence,
		SessionID:      event.SessionId,
		Kind:           kind,
		Type:           strings.TrimSpace(event.Type),
		SourceTargetID: strings.TrimSpace(event.SourceTargetId),
		Event:          event,
		CreatedAt:      time.Now(),
	}
	return action, true
}

func inputSyncShouldTraceAction(action inputSyncAction) bool {
	if action.Sequence <= 5 {
		return true
	}
	if action.Kind == "key" || action.Kind == "input" || action.Kind == "scroll" || action.Kind == "navigation" || action.Kind == "tab" {
		return true
	}
	if action.Type != "mouseMoved" {
		return true
	}
	return action.Sequence%inputSyncTraceEvery == 0
}

func inputSyncActionTrace(action inputSyncAction) string {
	event := action.Event
	switch action.Kind {
	case "input":
		return fmt.Sprintf("seq=%d kind=input selector=%s value_len=%d checked=%t has_checked=%t source=%s",
			action.Sequence,
			sanitizeInputSyncSelectorForLog(event.Selector),
			len(event.Value),
			event.Checked,
			event.HasChecked,
			action.SourceTargetID,
		)
	case "scroll":
		return fmt.Sprintf("seq=%d kind=scroll selector=%s left=%.0f top=%.0f source=%s",
			action.Sequence,
			sanitizeInputSyncSelectorForLog(event.Selector),
			event.ScrollLeft,
			event.ScrollTop,
			action.SourceTargetID,
		)
	case "navigation":
		return fmt.Sprintf("seq=%d kind=navigation url=%s title=%s source=%s",
			action.Sequence,
			sanitizeInputSyncURLForLog(event.URL),
			sanitizeInputSyncTitleForLog(event.Title),
			action.SourceTargetID,
		)
	default:
		return fmt.Sprintf("seq=%d %s", action.Sequence, inputSyncEventTrace(event))
	}
}

func (m *inputSyncManager) markSessionBroken(session *inputSyncSession, warning string) {
	if m == nil || session == nil {
		return
	}
	session.warning = warning
	logger.New("InputSync").Warn("输入同步会话断开",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("master_name", session.masterName),
		logger.F("event_count", session.eventCount.Load()),
		logger.F("warning", warning),
	)
	m.emitStatus(session.status())

	m.mu.Lock()
	if m.cur == session {
		m.cur = nil
	}
	m.mu.Unlock()
	session.close()
	m.emitStatus(m.Status())
}

func (s *inputSyncSession) reconnectTarget(target *inputSyncTarget) bool {
	if s == nil || target == nil {
		return false
	}
	deadline := time.Now().Add(inputSyncCommandTimeout)
	var lastErr error
	attempt := 0
	for {
		attempt++
		client, viewport, err := openInputSyncPageClient("target", target.profileID, target.name, target.debugPort, false)
		if err == nil {
			previous := target.replaceClient(client)
			target.viewport = viewport
			if previous != nil && previous != client {
				previous.Close()
			}
			logInputSyncTrace("输入同步目标 CDP 重连成功",
				logger.F("session_id", s.id),
				logger.F("target_profile_id", target.profileID),
				logger.F("target_name", target.name),
				logger.F("target_debug_port", target.debugPort),
				logger.F("viewport", formatInputSyncViewport(viewport)),
				logger.F("attempt", attempt),
			)
			if s.manager != nil {
				s.manager.emitStatus(s.status())
			}
			return true
		}
		lastErr = err
		if client != nil {
			client.Close()
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	errMessage := ""
	if lastErr != nil {
		errMessage = lastErr.Error()
	}
	logger.New("InputSync").Warn("输入同步目标 CDP 重连失败",
		logger.F("session_id", s.id),
		logger.F("target_profile_id", target.profileID),
		logger.F("target_name", target.name),
		logger.F("target_debug_port", target.debugPort),
		logger.F("attempts", attempt),
		logger.F("error", errMessage),
	)
	return false
}

func (m *inputSyncManager) reconnectMaster(ctx context.Context, session *inputSyncSession, reason string, force bool) bool {
	if m == nil || session == nil {
		return false
	}
	session.reconnectMu.Lock()
	defer session.reconnectMu.Unlock()

	current := session.getMaster()
	if !force && current != nil && !current.isClosed() {
		probeCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
		ok, err := probeInputSyncBridge(probeCtx, current, session.id)
		cancel()
		if err == nil && ok {
			return true
		}
	}

	reconnectID := session.reconnectCount.Add(1)
	logger.New("InputSync").Warn("输入同步主控 CDP 准备重连",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("master_name", session.masterName),
		logger.F("reason", reason),
		logger.F("reconnect_id", reconnectID),
		logger.F("event_count", session.eventCount.Load()),
	)

	deadline := time.Now().Add(inputSyncReconnectWait)
	var lastErr error
	attempt := 0
	for {
		attempt++
		if ctx.Err() != nil {
			return false
		}

		debugPort := resolveInputSyncMasterDebugPort(m.app, session.masterProfileID, currentDebugPort(current))
		if debugPort <= 0 {
			lastErr = fmt.Errorf("主控调试端口不可用")
		} else {
			client, viewport, err := openInputSyncPageClient("master", session.masterProfileID, session.masterName, debugPort, true)
			if err == nil {
				installCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
				err = installInputSyncBridge(installCtx, client, session.id)
				cancel()
			}
			if err == nil {
				previous := session.replaceMaster(client)
				if previous != nil && previous != client {
					previous.Close()
				}
				logInputSyncTrace("输入同步主控 CDP 重连成功",
					logger.F("session_id", session.id),
					logger.F("master_profile_id", session.masterProfileID),
					logger.F("master_name", session.masterName),
					logger.F("debug_port", debugPort),
					logger.F("viewport", formatInputSyncViewport(viewport)),
					logger.F("reconnect_id", reconnectID),
					logger.F("attempt", attempt),
				)
				return true
			}
			lastErr = err
			if client != nil {
				client.Close()
			}
		}

		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(inputSyncReconnectStep):
		}
	}

	errMessage := ""
	if lastErr != nil {
		errMessage = lastErr.Error()
	}
	logger.New("InputSync").Warn("输入同步主控 CDP 重连失败",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("master_name", session.masterName),
		logger.F("reason", reason),
		logger.F("reconnect_id", reconnectID),
		logger.F("attempts", attempt),
		logger.F("error", errMessage),
	)
	return false
}

func resolveInputSyncMasterDebugPort(app *App, profileID string, fallback int) int {
	if app == nil || app.browserMgr == nil {
		return fallback
	}
	app.browserMgr.Mutex.Lock()
	defer app.browserMgr.Mutex.Unlock()
	profile, exists := app.browserMgr.Profiles[profileID]
	if !exists || profile == nil || !profile.Running || profile.DebugPort <= 0 {
		return fallback
	}
	if !profile.DebugReady {
		if err := probeBrowserDebugPort(profile.DebugPort, browserDebugProbeTimeout); err == nil {
			app.markProfileDebugReadyLocked(profile, profile.DebugPort)
		}
	}
	if profile.DebugReady {
		return profile.DebugPort
	}
	return fallback
}

func currentDebugPort(client *inputSyncPageClient) int {
	if client == nil {
		return 0
	}
	return client.debugPort
}

func (m *inputSyncManager) monitorMasterBridge(ctx context.Context, session *inputSyncSession) {
	if m == nil || session == nil || session.getMaster() == nil {
		return
	}
	ticker := time.NewTicker(inputSyncHealthInterval)
	defer ticker.Stop()

	logInputSyncTrace("输入同步主控事件桥健康检查已启动",
		logger.F("session_id", session.id),
		logger.F("interval_ms", inputSyncHealthInterval.Milliseconds()),
	)
	for {
		select {
		case <-ctx.Done():
			logInputSyncTrace("输入同步主控事件桥健康检查已停止",
				logger.F("session_id", session.id),
				logger.F("reason", "context_done"),
			)
			return
		case <-ticker.C:
			master := session.getMaster()
			if master == nil {
				continue
			}
			beforeHealthCount := session.healthCount.Load()
			probeCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
			ok, err := probeInputSyncBridge(probeCtx, master, session.id)
			cancel()
			if err == nil && ok && waitForInputSyncHealthAck(session, beforeHealthCount, 800*time.Millisecond) {
				continue
			}
			if err == nil && ok {
				err = fmt.Errorf("主控 JS 心跳已触发，但后端未收到 Runtime.bindingCalled 回传")
			}

			fields := []logger.Field{
				logger.F("session_id", session.id),
				logger.F("master_profile_id", session.masterProfileID),
				logger.F("master_name", session.masterName),
				logger.F("event_count", session.eventCount.Load()),
				logger.F("health_count", session.healthCount.Load()),
				logger.F("probe_ok", ok),
			}
			if err != nil {
				fields = append(fields, logger.F("error", err.Error()))
			}
			logger.New("InputSync").Warn("输入同步主控事件桥健康检查失败，准备重连/重装", fields...)

			_ = m.reconnectMaster(ctx, session, "主控事件桥健康检查失败", true)
		}
	}
}

func waitForInputSyncHealthAck(session *inputSyncSession, before int64, timeout time.Duration) bool {
	if session == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if session.healthCount.Load() > before {
			return true
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *inputSyncManager) monitorMasterTargets(ctx context.Context, session *inputSyncSession) {
	if m == nil || session == nil {
		return
	}
	ticker := time.NewTicker(inputSyncTargetRefresh)
	defer ticker.Stop()

	logInputSyncTrace("输入同步主控页面目标监控已启动",
		logger.F("session_id", session.id),
		logger.F("master_profile_id", session.masterProfileID),
		logger.F("interval_ms", inputSyncTargetRefresh.Milliseconds()),
	)
	for {
		m.syncMasterTargets(ctx, session)
		m.probeMasterClients(ctx, session)

		select {
		case <-ctx.Done():
			logInputSyncTrace("输入同步主控页面目标监控已停止",
				logger.F("session_id", session.id),
				logger.F("reason", "context_done"),
			)
			return
		case <-ticker.C:
		}
	}
}

func (m *inputSyncManager) syncMasterTargets(ctx context.Context, session *inputSyncSession) {
	if m == nil || session == nil || ctx.Err() != nil {
		return
	}
	debugPort := resolveInputSyncMasterDebugPort(m.app, session.masterProfileID, session.masterDebugPort)
	if debugPort <= 0 {
		logger.New("InputSync").Warn("输入同步主控页面目标扫描失败",
			logger.F("session_id", session.id),
			logger.F("master_profile_id", session.masterProfileID),
			logger.F("reason", "debug_port_unavailable"),
		)
		return
	}
	session.masterDebugPort = debugPort
	targets, err := listInputSyncPageTargets(debugPort)
	if err != nil {
		logger.New("InputSync").Warn("输入同步主控页面目标扫描失败",
			logger.F("session_id", session.id),
			logger.F("master_profile_id", session.masterProfileID),
			logger.F("debug_port", debugPort),
			logger.F("error", err.Error()),
		)
		return
	}

	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		seen[target.id] = struct{}{}
		if session.hasMasterClient(target.id) {
			continue
		}
		client, viewport, err := openInputSyncPageClientForTarget("master", session.masterProfileID, session.masterName, debugPort, target, true)
		if err == nil {
			installCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
			err = installInputSyncBridge(installCtx, client, session.id)
			cancel()
		}
		if err != nil {
			if client != nil {
				client.Close()
			}
			logger.New("InputSync").Warn("输入同步主控页面目标连接失败",
				logger.F("session_id", session.id),
				logger.F("target_id", target.id),
				logger.F("target_url", target.url),
				logger.F("debug_port", debugPort),
				logger.F("error", err.Error()),
			)
			continue
		}
		session.addMasterClient(ctx, client)
		session.dispatchSystemEvent(inputSyncWireEvent{
			Kind:           "tab",
			Type:           "created",
			URL:            target.url,
			Title:          target.title,
			SourceTargetId: target.id,
		})
		logInputSyncTrace("输入同步主控页面目标已接入",
			logger.F("session_id", session.id),
			logger.F("target_id", target.id),
			logger.F("target_title", target.title),
			logger.F("target_url", target.url),
			logger.F("debug_port", debugPort),
			logger.F("viewport", formatInputSyncViewport(viewport)),
		)
	}

	for _, client := range session.masterClientSnapshot() {
		if client == nil || client.targetID == "" {
			continue
		}
		if _, ok := seen[client.targetID]; !ok {
			session.removeMasterClient(client.targetID, client)
			client.Close()
			session.dispatchSystemEvent(inputSyncWireEvent{
				Kind:           "tab",
				Type:           "closed",
				URL:            client.targetURL,
				Title:          client.targetTitle,
				SourceTargetId: client.targetID,
			})
			logInputSyncTrace("输入同步主控页面目标已移除",
				logger.F("session_id", session.id),
				logger.F("target_id", client.targetID),
				logger.F("target_url", client.targetURL),
			)
		}
	}
}

func (m *inputSyncManager) probeMasterClients(ctx context.Context, session *inputSyncSession) {
	if m == nil || session == nil || ctx.Err() != nil {
		return
	}
	for _, client := range session.masterClientSnapshot() {
		if client == nil || client.isClosed() {
			continue
		}
		before := client.healthAcks.Load()
		probeCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
		ok, err := probeInputSyncBridge(probeCtx, client, session.id)
		cancel()
		if err == nil && ok && waitForInputSyncClientHealthAck(client, before, 800*time.Millisecond) {
			continue
		}
		if err == nil && ok {
			err = fmt.Errorf("主控 JS 心跳已触发，但后端未收到 Runtime.bindingCalled 回传")
		}
		fields := []logger.Field{
			logger.F("session_id", session.id),
			logger.F("master_profile_id", session.masterProfileID),
			logger.F("target_id", client.targetID),
			logger.F("target_url", client.targetURL),
			logger.F("health_count", client.healthAcks.Load()),
			logger.F("probe_ok", ok),
		}
		if err != nil {
			fields = append(fields, logger.F("error", err.Error()))
		}
		logger.New("InputSync").Warn("输入同步主控页面目标健康检查失败，移除后等待重连", fields...)
		session.removeMasterClient(client.targetID, client)
		client.Close()
	}
}

func waitForInputSyncClientHealthAck(client *inputSyncPageClient, before int64, timeout time.Duration) bool {
	if client == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if client.healthAcks.Load() > before {
			return true
		}
		if timeout <= 0 || !time.Now().Before(deadline) || client.isClosed() {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (t *inputSyncTarget) setWarning(warning string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.warning = warning
	t.mu.Unlock()
}

func (t *inputSyncTarget) getWarning() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.warning
}

func (t *inputSyncTarget) pause(reason string) bool {
	return t.pauseWithKind(inputSyncPauseKindActionFailures, reason, true)
}

func (t *inputSyncTarget) pauseWithKind(kind string, reason string, recoverable bool) bool {
	if t == nil {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "输入同步已暂停"
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = inputSyncPauseKindActionFailures
	}
	now := time.Now()
	t.mu.Lock()
	changed := !t.paused || t.suspendReason != reason || t.pauseKind != kind
	t.paused = true
	t.suspendReason = reason
	t.pauseKind = kind
	t.recoverable = recoverable
	t.warning = reason
	if changed {
		t.recovering = false
		t.recoverAttempts = 0
		if recoverable {
			t.nextRecoverAt = now.Add(inputSyncRecoverBackoffForAttempt(0))
		} else {
			t.nextRecoverAt = time.Time{}
		}
	}
	t.mu.Unlock()
	return changed
}

func (t *inputSyncTarget) isPaused() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused
}

func (t *inputSyncTarget) pauseSnapshot() inputSyncTargetPauseSnapshot {
	if t == nil {
		return inputSyncTargetPauseSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return inputSyncTargetPauseSnapshot{
		paused:          t.paused,
		reason:          t.suspendReason,
		kind:            t.pauseKind,
		recoverable:     t.recoverable,
		failureCount:    t.failureCount,
		lastFailedSeq:   t.lastFailedSeq,
		recoverAttempts: t.recoverAttempts,
		nextRecoverAt:   t.nextRecoverAt,
		recovering:      t.recovering,
	}
}

func (t *inputSyncTarget) recordActionFailure(action inputSyncAction, err error) int {
	if t == nil {
		return 0
	}
	t.failedActions.Add(1)
	t.mu.Lock()
	t.failureCount++
	t.lastFailedSeq = action.Sequence
	count := t.failureCount
	t.warning = fmt.Sprintf("Action #%d 执行失败：%v", action.Sequence, err)
	t.mu.Unlock()
	return count
}

func (t *inputSyncTarget) resetActionFailures() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.failureCount = 0
	t.lastFailedSeq = 0
	if !t.paused {
		t.warning = ""
	}
	t.mu.Unlock()
}

func (t *inputSyncTarget) setExpectedURL(rawURL string) {
	if t == nil {
		return
	}
	rawURL = strings.TrimSpace(rawURL)
	t.mu.Lock()
	if t.expectedURL != rawURL {
		t.expectedURL = rawURL
		t.resyncURL = ""
		t.resyncFailures = 0
		t.lastResyncAt = time.Time{}
	}
	t.mu.Unlock()
}

func (t *inputSyncTarget) resetResyncState(expectedURL string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.expectedURL == expectedURL || expectedURL == "" {
		t.resyncURL = ""
		t.resyncFailures = 0
		t.lastResyncAt = time.Time{}
	}
	t.mu.Unlock()
}

func (t *inputSyncTarget) reserveResyncAttempt(expectedURL string, now time.Time) (int, bool, string) {
	if t == nil {
		return 0, false, ""
	}
	expectedURL = strings.TrimSpace(expectedURL)
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paused {
		return t.resyncFailures, false, t.suspendReason
	}
	if t.resyncURL != expectedURL {
		t.resyncURL = expectedURL
		t.resyncFailures = 0
		t.lastResyncAt = time.Time{}
	}
	if t.resyncFailures >= inputSyncMaxResyncTries {
		reason := fmt.Sprintf("状态偏离后已重同步 %d 次仍未恢复，已暂停该实例", inputSyncMaxResyncTries)
		t.paused = true
		t.suspendReason = reason
		t.pauseKind = inputSyncPauseKindURLDeviation
		t.recoverable = true
		t.recoverAttempts = 0
		t.recovering = false
		t.nextRecoverAt = now.Add(inputSyncRecoverBackoffForAttempt(0))
		t.warning = reason
		return t.resyncFailures, false, reason
	}
	if !t.lastResyncAt.IsZero() && now.Sub(t.lastResyncAt) < inputSyncResyncCooldown {
		return t.resyncFailures, true, ""
	}
	t.resyncFailures++
	t.lastResyncAt = now
	return t.resyncFailures, false, ""
}

func (t *inputSyncTarget) beginRecoverAttempt(automatic bool, now time.Time) (int, error) {
	if t == nil {
		return 0, fmt.Errorf("恢复失败：目标不存在")
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.paused {
		return 0, nil
	}
	if automatic && !t.recoverable {
		return 0, nil
	}
	if automatic && !t.nextRecoverAt.IsZero() && t.nextRecoverAt.After(now) {
		return 0, nil
	}
	if t.recovering {
		return 0, nil
	}
	if t.recoverAttempts >= inputSyncMaxRecoverAttempts {
		t.recoverable = false
		t.nextRecoverAt = time.Time{}
		t.warning = fmt.Sprintf("自动恢复已尝试 %d 次仍失败，需要人工处理", inputSyncMaxRecoverAttempts)
		t.suspendReason = t.warning
		return 0, fmt.Errorf("%s", t.warning)
	}
	t.recovering = true
	t.recoverAttempts++
	t.nextRecoverAt = time.Time{}
	return t.recoverAttempts, nil
}

func (t *inputSyncTarget) finishRecoverFailure(err error, now time.Time) string {
	if t == nil {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	errMessage := "恢复失败"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		errMessage = err.Error()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recovering = false
	if t.pauseKind == inputSyncPauseKindChallenge {
		t.recoverable = false
		t.nextRecoverAt = time.Time{}
		t.warning = t.suspendReason
		return t.suspendReason
	}
	if t.recoverAttempts >= inputSyncMaxRecoverAttempts {
		reason := fmt.Sprintf("自动恢复已尝试 %d 次仍失败，需要人工处理：%s", inputSyncMaxRecoverAttempts, errMessage)
		t.recoverable = false
		t.nextRecoverAt = time.Time{}
		t.suspendReason = reason
		t.warning = reason
		return reason
	}
	t.recoverable = true
	t.nextRecoverAt = now.Add(inputSyncRecoverBackoffForAttempt(t.recoverAttempts))
	t.warning = fmt.Sprintf("恢复第 %d 次失败：%s", t.recoverAttempts, errMessage)
	return ""
}

func (t *inputSyncTarget) finishRecoverSuccess() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.paused = false
	t.suspendReason = ""
	t.pauseKind = ""
	t.recoverable = false
	t.recoverAttempts = 0
	t.nextRecoverAt = time.Time{}
	t.recovering = false
	t.failureCount = 0
	t.lastFailedSeq = 0
	t.resyncURL = ""
	t.resyncFailures = 0
	t.lastResyncAt = time.Time{}
	t.warning = ""
	t.mu.Unlock()
}

func (t *inputSyncTarget) enqueueAction(action inputSyncAction) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	queue := t.queue
	paused := t.paused
	t.mu.Unlock()
	if paused {
		t.droppedActions.Add(1)
		return false
	}
	if queue == nil {
		t.droppedActions.Add(1)
		return false
	}
	select {
	case queue <- action:
		return true
	default:
		t.droppedActions.Add(1)
		return false
	}
}

func (t *inputSyncTarget) runActionQueue(ctx context.Context, session *inputSyncSession) {
	if t == nil || session == nil {
		return
	}
	logInputSyncTrace("输入同步从浏览器执行器已启动",
		logger.F("session_id", session.id),
		logger.F("target_profile_id", t.profileID),
		logger.F("target_name", t.name),
		logger.F("target_debug_port", t.debugPort),
	)
	healthTicker := time.NewTicker(inputSyncHealthInterval)
	defer healthTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			logInputSyncTrace("输入同步从浏览器执行器已停止",
				logger.F("session_id", session.id),
				logger.F("target_profile_id", t.profileID),
				logger.F("target_name", t.name),
				logger.F("reason", "context_done"),
				logger.F("applied_actions", t.appliedActions.Load()),
				logger.F("failed_actions", t.failedActions.Load()),
			)
			return
		case <-healthTicker.C:
			t.verifyState(ctx, session)
		case action := <-t.queue:
			t.applyQueuedAction(ctx, session, action)
		}
	}
}

func (t *inputSyncTarget) applyQueuedAction(ctx context.Context, session *inputSyncSession, action inputSyncAction) {
	if t == nil || session == nil {
		return
	}
	if t.isPaused() {
		t.droppedActions.Add(1)
		return
	}
	if action.Kind == "navigation" && strings.TrimSpace(action.Event.URL) != "" {
		t.setExpectedURL(action.Event.URL)
	}
	err := t.applyActionOnce(action)
	if err != nil {
		if detected, reason := t.detectChallengeForAction(ctx, action); detected {
			if t.pauseWithKind(inputSyncPauseKindChallenge, reason, false) && session.manager != nil {
				session.manager.emitStatus(session.status())
			}
			logger.New("InputSync").Warn("输入同步从浏览器 Action 失败后检测到验证码/风控，已暂停该实例",
				logger.F("session_id", session.id),
				logger.F("target_profile_id", t.profileID),
				logger.F("target_name", t.name),
				logger.F("target_debug_port", t.debugPort),
				logger.F("action_seq", action.Sequence),
				logger.F("action", inputSyncActionTrace(action)),
				logger.F("reason", reason),
				logger.F("error", err.Error()),
			)
			return
		}
		failureCount := t.recordActionFailure(action, err)
		if failureCount >= inputSyncMaxActionErrors {
			reason := fmt.Sprintf("连续 %d 个 Action 执行失败，已暂停该实例", failureCount)
			t.pauseWithKind(inputSyncPauseKindForActionError(err), reason, true)
			logger.New("InputSync").Warn("输入同步从浏览器连续失败，已暂停该实例",
				logger.F("session_id", session.id),
				logger.F("target_profile_id", t.profileID),
				logger.F("target_name", t.name),
				logger.F("target_debug_port", t.debugPort),
				logger.F("action_seq", action.Sequence),
				logger.F("failure_count", failureCount),
				logger.F("error", err.Error()),
			)
			if session.manager != nil {
				session.manager.emitStatus(session.status())
			}
			return
		}
		if !inputSyncErrorNeedsReconnect(err) {
			logger.New("InputSync").Warn("输入同步从浏览器 Action 执行失败，已记录但不重连",
				logger.F("session_id", session.id),
				logger.F("target_profile_id", t.profileID),
				logger.F("target_name", t.name),
				logger.F("target_debug_port", t.debugPort),
				logger.F("action_seq", action.Sequence),
				logger.F("failure_count", failureCount),
				logger.F("action", inputSyncActionTrace(action)),
				logger.F("error", err.Error()),
			)
			if session.manager != nil {
				session.manager.emitStatus(session.status())
			}
			return
		}
		logger.New("InputSync").Warn("输入同步从浏览器 CDP/连接类 Action 失败，准备单独重连一次",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("action_seq", action.Sequence),
			logger.F("failure_count", failureCount),
			logger.F("action", inputSyncActionTrace(action)),
			logger.F("error", err.Error()),
		)
		if !session.reconnectTarget(t) {
			t.setWarning(fmt.Sprintf("Action #%d 执行失败且重连失败：%v", action.Sequence, err))
			if session.manager != nil {
				session.manager.emitStatus(session.status())
			}
			return
		}
		err = t.applyActionOnce(action)
	}
	if err != nil {
		if detected, reason := t.detectChallengeForAction(ctx, action); detected {
			t.pauseWithKind(inputSyncPauseKindChallenge, reason, false)
		} else {
			t.recordActionFailure(action, err)
			t.setWarning(fmt.Sprintf("Action #%d 重试失败：%v", action.Sequence, err))
		}
		logger.New("InputSync").Warn("输入同步从浏览器 Action 重试仍失败",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("action_seq", action.Sequence),
			logger.F("action", inputSyncActionTrace(action)),
			logger.F("error", err.Error()),
		)
		if session.manager != nil {
			session.manager.emitStatus(session.status())
		}
		return
	}
	t.resetActionFailures()
	applied := t.appliedActions.Add(1)
	if inputSyncShouldTraceAction(action) {
		logInputSyncTrace("输入同步从浏览器 Action 已执行",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("action_seq", action.Sequence),
			logger.F("applied_actions", applied),
			logger.F("action", inputSyncActionTrace(action)),
		)
	}
}

func (t *inputSyncTarget) detectChallengeForAction(ctx context.Context, action inputSyncAction) (bool, string) {
	if t == nil || action.Kind == "tab" && action.Type == "closed" {
		return false, ""
	}
	client := t.clientForAction(action)
	if client == nil || client.isClosed() {
		return false, ""
	}
	checkCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
	defer cancel()
	detected, reason, err := client.DetectChallenge(checkCtx)
	if err != nil {
		logger.New("InputSync").Debug("输入同步验证码/风控检测跳过",
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("action_seq", action.Sequence),
			logger.F("error", err.Error()),
		)
		return false, ""
	}
	return detected, reason
}

func (t *inputSyncTarget) applyActionOnce(action inputSyncAction) error {
	if action.Kind == "tab" {
		return t.applyTabAction(action)
	}
	client := t.clientForAction(action)
	if client == nil {
		return fmt.Errorf("目标 CDP client 不存在")
	}
	return client.ApplyAction(action)
}

func (t *inputSyncTarget) applyTabAction(action inputSyncAction) error {
	sourceTargetID := strings.TrimSpace(action.SourceTargetID)
	if sourceTargetID == "" {
		return nil
	}
	switch action.Type {
	case "closed":
		client := t.unbindClient(sourceTargetID)
		if client != nil {
			client.Close()
		}
		return nil
	case "created", "activated":
		if client := t.exactClientForSource(sourceTargetID); client != nil && !client.isClosed() {
			t.bindClient(sourceTargetID, client)
			return nil
		}
		client, viewport, err := openInputSyncNewPageClient("target", t.profileID, t.name, t.debugPort, action.Event.URL)
		if err != nil {
			return err
		}
		t.bindClient(sourceTargetID, client)
		t.viewport = viewport
		return nil
	default:
		return nil
	}
}

func (t *inputSyncTarget) verifyState(ctx context.Context, session *inputSyncSession) {
	if t == nil || session == nil || ctx.Err() != nil {
		return
	}
	if t.isPaused() {
		return
	}
	t.mu.Lock()
	expectedURL := t.expectedURL
	t.mu.Unlock()
	if strings.TrimSpace(expectedURL) == "" {
		return
	}
	client := t.getClient()
	if client == nil || client.isClosed() {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
	defer cancel()
	currentURL, err := client.CurrentURL(checkCtx)
	if err != nil {
		logger.New("InputSync").Warn("输入同步从浏览器状态校验失败",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("error", err.Error()),
		)
		return
	}
	if normalizeInputSyncURL(currentURL) == normalizeInputSyncURL(expectedURL) {
		t.resetResyncState(expectedURL)
		return
	}
	detectCtx, detectCancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
	detected, reason, detectErr := client.DetectChallenge(detectCtx)
	detectCancel()
	if detectErr == nil && detected {
		if t.pauseWithKind(inputSyncPauseKindChallenge, reason, false) {
			logger.New("InputSync").Warn("输入同步从浏览器状态校验检测到验证码/风控，已暂停该实例",
				logger.F("session_id", session.id),
				logger.F("target_profile_id", t.profileID),
				logger.F("target_name", t.name),
				logger.F("target_debug_port", t.debugPort),
				logger.F("current_url", sanitizeInputSyncURLForLog(currentURL)),
				logger.F("expected_url", sanitizeInputSyncURLForLog(expectedURL)),
				logger.F("reason", reason),
			)
			if session.manager != nil {
				session.manager.emitStatus(session.status())
			}
		}
		return
	}
	attempt, coolingDown, pauseReason := t.reserveResyncAttempt(expectedURL, time.Now())
	if pauseReason != "" {
		logger.New("InputSync").Warn("输入同步从浏览器 URL 偏离超过重同步次数，已暂停该实例",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("current_url", sanitizeInputSyncURLForLog(currentURL)),
			logger.F("expected_url", sanitizeInputSyncURLForLog(expectedURL)),
			logger.F("reason", pauseReason),
		)
		if session.manager != nil {
			session.manager.emitStatus(session.status())
		}
		return
	}
	if coolingDown {
		return
	}
	logger.New("InputSync").Warn("输入同步从浏览器 URL 偏离，单独有限重同步",
		logger.F("session_id", session.id),
		logger.F("target_profile_id", t.profileID),
		logger.F("target_name", t.name),
		logger.F("target_debug_port", t.debugPort),
		logger.F("current_url", sanitizeInputSyncURLForLog(currentURL)),
		logger.F("expected_url", sanitizeInputSyncURLForLog(expectedURL)),
		logger.F("resync_attempt", attempt),
		logger.F("resync_max", inputSyncMaxResyncTries),
	)
	if err := client.ApplyAction(inputSyncAction{
		SessionID: session.id,
		Kind:      "navigation",
		Type:      "resync",
		Event:     inputSyncWireEvent{URL: expectedURL},
		CreatedAt: time.Now(),
	}); err != nil {
		logger.New("InputSync").Warn("输入同步从浏览器有限重同步失败",
			logger.F("session_id", session.id),
			logger.F("target_profile_id", t.profileID),
			logger.F("target_name", t.name),
			logger.F("target_debug_port", t.debugPort),
			logger.F("expected_url", sanitizeInputSyncURLForLog(expectedURL)),
			logger.F("resync_attempt", attempt),
			logger.F("error", err.Error()),
		)
	}
}

type inputSyncCDPDispatch struct {
	method string
	mouse  *proto.InputDispatchMouseEvent
	key    *proto.InputDispatchKeyEvent
}

func inputSyncEventToCDP(event inputSyncWireEvent) (inputSyncCDPDispatch, bool) {
	switch event.Kind {
	case "mouse":
		buttons := event.Buttons
		req := &proto.InputDispatchMouseEvent{
			Type:        proto.InputDispatchMouseEventType(event.Type),
			X:           event.X,
			Y:           event.Y,
			Modifiers:   event.Modifiers,
			Button:      inputSyncMouseButton(event.Button),
			Buttons:     &buttons,
			ClickCount:  event.ClickCount,
			PointerType: proto.InputDispatchMouseEventPointerTypeMouse,
		}
		if event.Type == string(proto.InputDispatchMouseEventTypeMouseWheel) {
			req.DeltaX = event.DeltaX
			req.DeltaY = event.DeltaY
		}
		return inputSyncCDPDispatch{method: "Input.dispatchMouseEvent", mouse: req}, true
	case "key":
		text := event.Text
		unmodifiedText := event.UnmodifiedText
		if event.Type == "keyUp" {
			text = ""
			unmodifiedText = ""
		}
		location := event.Location
		req := &proto.InputDispatchKeyEvent{
			Type:                  proto.InputDispatchKeyEventType(event.Type),
			Modifiers:             event.Modifiers,
			Key:                   event.Key,
			Code:                  event.Code,
			Text:                  text,
			UnmodifiedText:        unmodifiedText,
			WindowsVirtualKeyCode: event.WindowsKeyCode,
			NativeVirtualKeyCode:  event.NativeKeyCode,
			AutoRepeat:            event.AutoRepeat,
			Location:              &location,
			IsSystemKey:           event.IsSystemKey,
		}
		return inputSyncCDPDispatch{method: "Input.dispatchKeyEvent", key: req}, true
	default:
		return inputSyncCDPDispatch{}, false
	}
}

func inputSyncMouseButton(button string) proto.InputMouseButton {
	switch button {
	case string(proto.InputMouseButtonLeft):
		return proto.InputMouseButtonLeft
	case string(proto.InputMouseButtonMiddle):
		return proto.InputMouseButtonMiddle
	case string(proto.InputMouseButtonRight):
		return proto.InputMouseButtonRight
	case string(proto.InputMouseButtonBack):
		return proto.InputMouseButtonBack
	case string(proto.InputMouseButtonForward):
		return proto.InputMouseButtonForward
	default:
		return proto.InputMouseButtonNone
	}
}

func openInputSyncPageClient(role string, profileID string, profileName string, debugPort int, withEvents bool) (*inputSyncPageClient, inputSyncViewport, error) {
	targets, err := listInputSyncPageTargets(debugPort)
	if err != nil {
		return nil, inputSyncViewport{}, err
	}
	if len(targets) == 0 {
		return nil, inputSyncViewport{}, fmt.Errorf("未找到可同步的页面目标")
	}
	return openInputSyncPageClientForTarget(role, profileID, profileName, debugPort, targets[0], withEvents)
}

func openInputSyncPageClientForTarget(role string, profileID string, profileName string, debugPort int, target inputSyncPageTarget, withEvents bool) (*inputSyncPageClient, inputSyncViewport, error) {
	client, err := newInputSyncPageClient(role, profileID, profileName, debugPort, target, withEvents)
	if err != nil {
		return nil, inputSyncViewport{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	viewport, err := client.Viewport(ctx)
	if err != nil {
		client.Close()
		return nil, inputSyncViewport{}, err
	}
	return client, viewport, nil
}

func openInputSyncNewPageClient(role string, profileID string, profileName string, debugPort int, rawURL string) (*inputSyncPageClient, inputSyncViewport, error) {
	client, err := newInputSyncNewPageClient(role, profileID, profileName, debugPort, rawURL)
	if err != nil {
		return nil, inputSyncViewport{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	viewport, err := client.Viewport(ctx)
	if err != nil {
		client.Close()
		return nil, inputSyncViewport{}, err
	}
	return client, viewport, nil
}

func listInputSyncPageTargets(debugPort int) ([]inputSyncPageTarget, error) {
	body, err := cdpGetEndpointBody(debugPort, "/json")
	if err != nil {
		return nil, fmt.Errorf("CDP /json 请求失败: %w", err)
	}
	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil || len(targets) == 0 {
		return nil, fmt.Errorf("CDP targets 解析失败或为空")
	}
	out := make([]inputSyncPageTarget, 0, len(targets))
	for _, target := range targets {
		wsURL := strings.TrimSpace(target.WebSocketDebuggerUrl)
		if target.Type != "page" || wsURL == "" {
			continue
		}
		id := strings.TrimSpace(target.Id)
		if id == "" {
			id = wsURL
		}
		out = append(out, inputSyncPageTarget{
			id:    id,
			title: strings.TrimSpace(target.Title),
			url:   strings.TrimSpace(target.Url),
			wsURL: wsURL,
		})
	}
	return out, nil
}

func inputSyncBrowserWebSocketURL(debugPort int) (string, error) {
	body, err := cdpGetEndpointBody(debugPort, "/json/version")
	if err != nil {
		return "", fmt.Errorf("CDP /json/version 请求失败: %w", err)
	}
	var version cdpBrowserVersion
	if err := json.Unmarshal(body, &version); err != nil {
		return "", fmt.Errorf("CDP browser target 解析失败: %w", err)
	}
	wsURL := strings.TrimSpace(version.WebSocketDebuggerUrl)
	if wsURL == "" {
		return "", fmt.Errorf("未找到浏览器级 WebSocket 调试地址")
	}
	return wsURL, nil
}

func newInputSyncPageClient(role string, profileID string, profileName string, debugPort int, target inputSyncPageTarget, withEvents bool) (*inputSyncPageClient, error) {
	browser, ws, cancel, err := connectInputSyncRodBrowser(debugPort)
	if err != nil {
		return nil, err
	}
	page, err := browser.PageFromTarget(proto.TargetTargetID(target.id))
	if err != nil {
		_ = ws.Close()
		cancel()
		return nil, fmt.Errorf("rod 页面 target 连接失败: %w", err)
	}
	return newInputSyncPageClientFromPage(role, profileID, profileName, debugPort, target, browser, page, ws, cancel, withEvents), nil
}

func newInputSyncNewPageClient(role string, profileID string, profileName string, debugPort int, rawURL string) (*inputSyncPageClient, error) {
	browser, ws, cancel, err := connectInputSyncRodBrowser(debugPort)
	if err != nil {
		return nil, err
	}
	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = ws.Close()
		cancel()
		return nil, fmt.Errorf("rod 从浏览器新建页面失败: %w", err)
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" && rawURL != "about:blank" && inputSyncCanNavigate(rawURL) {
		ctx, navCancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
		_, navErr := proto.PageNavigate{URL: rawURL}.Call(page.Context(ctx))
		navCancel()
		if navErr != nil {
			logger.New("InputSync").Warn("输入同步从浏览器新建 tab 后导航失败，保留空白页等待后续导航 Action",
				logger.F("profile_id", profileID),
				logger.F("profile_name", profileName),
				logger.F("debug_port", debugPort),
				logger.F("url", sanitizeInputSyncURLForLog(rawURL)),
				logger.F("error", navErr.Error()),
			)
		}
	}
	target := inputSyncPageTarget{
		id:    string(page.TargetID),
		title: "",
		url:   rawURL,
	}
	return newInputSyncPageClientFromPage(role, profileID, profileName, debugPort, target, browser, page, ws, cancel, false), nil
}

func connectInputSyncRodBrowser(debugPort int) (*rod.Browser, *rodcdp.WebSocket, context.CancelFunc, error) {
	browserURL, err := inputSyncBrowserWebSocketURL(debugPort)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ws := &rodcdp.WebSocket{}
	connectCtx, connectCancel := context.WithTimeout(ctx, inputSyncCommandTimeout)
	err = ws.Connect(connectCtx, browserURL, nil)
	connectCancel()
	if err != nil {
		cancel()
		return nil, nil, nil, fmt.Errorf("rod CDP websocket 连接失败: %w", err)
	}

	browser := rod.New().
		NoDefaultDevice().
		Client(rodcdp.New().Start(ws)).
		Context(ctx)
	if err := browser.Connect(); err != nil {
		_ = ws.Close()
		cancel()
		return nil, nil, nil, fmt.Errorf("rod 浏览器连接失败: %w", err)
	}
	return browser, ws, cancel, nil
}

func newInputSyncPageClientFromPage(role string, profileID string, profileName string, debugPort int, target inputSyncPageTarget, browser *rod.Browser, page *rod.Page, ws *rodcdp.WebSocket, cancel context.CancelFunc, withEvents bool) *inputSyncPageClient {
	client := &inputSyncPageClient{
		role:        role,
		profileID:   profileID,
		profileName: profileName,
		debugPort:   debugPort,
		targetID:    target.id,
		targetURL:   target.url,
		targetTitle: target.title,
		browser:     browser,
		page:        page,
		ws:          ws,
		cancel:      cancel,
		events:      make(chan inputSyncWireEvent, 256),
		done:        make(chan struct{}),
	}
	if !withEvents {
		client.events = nil
	} else {
		go client.eventLoop()
	}
	return client
}

func (c *inputSyncPageClient) Call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if c == nil {
		return nil, fmt.Errorf("CDP client is nil")
	}
	if c.isClosed() {
		return nil, fmt.Errorf("CDP connection closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	page := c.page.Context(ctx)
	resultBytes, err := page.Call(ctx, string(page.SessionID), method, params)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) == 0 {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("CDP 响应解析失败: %w", err)
	}
	return result, nil
}

func (c *inputSyncPageClient) Send(command inputSyncCDPDispatch) error {
	if c == nil {
		return fmt.Errorf("CDP client is nil")
	}
	if c.isClosed() {
		return fmt.Errorf("CDP connection closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	page := c.page.Context(ctx)
	switch {
	case command.mouse != nil:
		return command.mouse.Call(page)
	case command.key != nil:
		return command.key.Call(page)
	default:
		return fmt.Errorf("unsupported input sync command")
	}
}

func (c *inputSyncPageClient) ApplyAction(action inputSyncAction) error {
	if c == nil {
		return fmt.Errorf("CDP client is nil")
	}
	if c.isClosed() {
		return fmt.Errorf("CDP connection closed")
	}
	switch action.Kind {
	case "mouse":
		return c.applyMouseAction(action)
	case "key":
		command, ok := inputSyncEventToCDP(action.Event)
		if !ok {
			return fmt.Errorf("不可映射键盘事件: %s", inputSyncActionTrace(action))
		}
		return c.Send(command)
	case "input":
		return c.applyInputState(action)
	case "scroll":
		return c.applyScrollState(action)
	case "navigation":
		return c.applyNavigationState(action)
	case "tab":
		return nil
	default:
		return fmt.Errorf("未知 Action 类型: %s", action.Kind)
	}
}

func (c *inputSyncPageClient) applyMouseAction(action inputSyncAction) error {
	event := action.Event
	if strings.TrimSpace(event.Selector) == "" {
		if inputSyncCanSkipUnmappedMouseAction(event) {
			return nil
		}
		return fmt.Errorf("鼠标 Action 缺少可定位 DOM selector: %s", inputSyncActionTrace(action))
	}

	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	x, y, ok, err := c.resolveSelectorPoint(ctx, event)
	cancel()
	if err != nil {
		return fmt.Errorf("DOM 坐标解析失败: %w", err)
	}
	if !ok {
		if inputSyncCanSkipUnmappedMouseAction(event) {
			return nil
		}
		return fmt.Errorf("目标元素不存在或不可见: %s", sanitizeInputSyncSelectorForLog(event.Selector))
	}
	event.X = x
	event.Y = y
	command, ok := inputSyncEventToCDP(event)
	if !ok {
		return fmt.Errorf("不可映射鼠标事件: %s", inputSyncActionTrace(action))
	}
	return c.Send(command)
}

func inputSyncCanSkipUnmappedMouseAction(event inputSyncWireEvent) bool {
	return event.Type == string(proto.InputDispatchMouseEventTypeMouseMoved) && event.Buttons == 0
}

func (c *inputSyncPageClient) resolveSelectorPoint(ctx context.Context, event inputSyncWireEvent) (float64, float64, bool, error) {
	selector := strings.TrimSpace(event.Selector)
	if selector == "" {
		return event.X, event.Y, false, nil
	}
	relX := event.RelX
	relY := event.RelY
	if relX == 0 && event.ElementWidth > 0 {
		relX = (event.X - event.ElementX) / event.ElementWidth
	}
	if relY == 0 && event.ElementHeight > 0 {
		relY = (event.Y - event.ElementY) / event.ElementHeight
	}
	if relX < 0 {
		relX = 0
	}
	if relX > 1 {
		relX = 1
	}
	if relY < 0 {
		relY = 0
	}
	if relY > 1 {
		relY = 1
	}
	expression := `(function () {
  var selector = ` + inputSyncJSONLiteral(selector) + `;
  var relX = ` + inputSyncJSONLiteral(relX) + `;
  var relY = ` + inputSyncJSONLiteral(relY) + `;
  var el = document.querySelector(selector);
  if (!el) return { ok: false };
  var rect = el.getBoundingClientRect();
  if (!rect || rect.width <= 0 || rect.height <= 0) return { ok: false };
  return { ok: true, x: rect.left + rect.width * relX, y: rect.top + rect.height * relY };
})()`
	value, err := c.evaluateRuntimeValue(ctx, expression)
	if err != nil {
		return event.X, event.Y, false, err
	}
	item, ok := value.(map[string]any)
	if !ok {
		return event.X, event.Y, false, nil
	}
	resolved, _ := item["ok"].(bool)
	if !resolved {
		return event.X, event.Y, false, nil
	}
	x, xOK := inputSyncNumber(item["x"])
	y, yOK := inputSyncNumber(item["y"])
	if !xOK || !yOK {
		return event.X, event.Y, false, nil
	}
	return x, y, true, nil
}

func (c *inputSyncPageClient) applyInputState(action inputSyncAction) error {
	event := action.Event
	selector := strings.TrimSpace(event.Selector)
	if selector == "" {
		return fmt.Errorf("输入 Action 缺少 selector")
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	expression := `(function () {
  var selector = ` + inputSyncJSONLiteral(selector) + `;
  var value = ` + inputSyncJSONLiteral(event.Value) + `;
  var hasChecked = ` + inputSyncJSONLiteral(event.HasChecked) + `;
  var checked = ` + inputSyncJSONLiteral(event.Checked) + `;
  var selectionStart = ` + inputSyncJSONLiteral(event.SelectionStart) + `;
  var selectionEnd = ` + inputSyncJSONLiteral(event.SelectionEnd) + `;
  var el = document.querySelector(selector);
  if (!el) return false;
  try { el.focus({ preventScroll: true }); } catch (_) { try { el.focus(); } catch (_) {} }
  if (hasChecked && 'checked' in el) {
    el.checked = !!checked;
  }
  if ('value' in el) {
    var proto = Object.getPrototypeOf(el);
    var desc = proto && Object.getOwnPropertyDescriptor(proto, 'value');
    if (desc && typeof desc.set === 'function') desc.set.call(el, value);
    else el.value = value;
    try {
      if (typeof el.setSelectionRange === 'function') {
        el.setSelectionRange(Math.max(0, selectionStart || value.length), Math.max(0, selectionEnd || value.length));
      }
    } catch (_) {}
  } else if (el.isContentEditable) {
    el.textContent = value;
  }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
})()`
	ok, err := c.evaluateRuntimeBool(ctx, expression)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("目标元素不存在或无法写入: %s", sanitizeInputSyncSelectorForLog(selector))
	}
	return nil
}

func (c *inputSyncPageClient) applyScrollState(action inputSyncAction) error {
	event := action.Event
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	expression := `(function () {
  var selector = ` + inputSyncJSONLiteral(strings.TrimSpace(event.Selector)) + `;
  var left = ` + inputSyncJSONLiteral(event.ScrollLeft) + `;
  var top = ` + inputSyncJSONLiteral(event.ScrollTop) + `;
  var el = selector ? document.querySelector(selector) : (document.scrollingElement || document.documentElement || document.body);
  if (!el) return false;
  if (el === document || el === window) el = document.scrollingElement || document.documentElement || document.body;
  el.scrollLeft = left;
  el.scrollTop = top;
  el.dispatchEvent(new Event('scroll', { bubbles: true }));
  return true;
})()`
	ok, err := c.evaluateRuntimeBool(ctx, expression)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("目标滚动容器不存在: %s", sanitizeInputSyncSelectorForLog(event.Selector))
	}
	return nil
}

func (c *inputSyncPageClient) applyNavigationState(action inputSyncAction) error {
	url := strings.TrimSpace(action.Event.URL)
	if url == "" {
		return nil
	}
	if !inputSyncCanNavigate(url) {
		return fmt.Errorf("不支持同步此 URL: %s", sanitizeInputSyncURLForLog(url))
	}
	ctx, cancel := context.WithTimeout(context.Background(), inputSyncCommandTimeout)
	defer cancel()
	_, err := proto.PageNavigate{URL: url}.Call(c.page.Context(ctx))
	return err
}

func (c *inputSyncPageClient) CurrentURL(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("CDP client is nil")
	}
	value, err := c.evaluateRuntimeValue(ctx, `location.href`)
	if err != nil {
		return "", err
	}
	url, _ := value.(string)
	return url, nil
}

func (c *inputSyncPageClient) DetectChallenge(ctx context.Context) (bool, string, error) {
	value, err := c.evaluateRuntimeValue(ctx, `(function () {
  var bodyText = '';
  try {
    bodyText = (document.body && (document.body.innerText || document.body.textContent)) || '';
  } catch (_) {}
  return {
    url: String(location.href || ''),
    title: String(document.title || ''),
    body: String(bodyText || '').slice(0, 4096)
  };
})()`)
	if err != nil {
		return false, "", err
	}
	item, ok := value.(map[string]any)
	if !ok {
		return false, "", nil
	}
	rawURL, _ := item["url"].(string)
	title, _ := item["title"].(string)
	body, _ := item["body"].(string)
	detected, reason := inputSyncDetectChallengeSignal(rawURL, title, body)
	return detected, reason, nil
}

func (c *inputSyncPageClient) evaluateRuntimeBool(ctx context.Context, expression string) (bool, error) {
	value, err := c.evaluateRuntimeValue(ctx, expression)
	if err != nil {
		return false, err
	}
	ok, _ := value.(bool)
	return ok, nil
}

func (c *inputSyncPageClient) evaluateRuntimeValue(ctx context.Context, expression string) (any, error) {
	if c == nil || c.page == nil {
		return nil, fmt.Errorf("CDP client is nil")
	}
	if c.isClosed() {
		return nil, fmt.Errorf("CDP connection closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := proto.RuntimeEvaluate{
		Expression:            expression,
		IncludeCommandLineAPI: false,
		ReturnByValue:         true,
	}.Call(c.page.Context(ctx))
	if err != nil {
		return nil, err
	}
	if result.ExceptionDetails != nil {
		return nil, fmt.Errorf("页面脚本执行异常: %s", result.ExceptionDetails.Text)
	}
	if result.Result == nil {
		return nil, nil
	}
	return result.Result.Value.Val(), nil
}

func (c *inputSyncPageClient) Viewport(ctx context.Context) (inputSyncViewport, error) {
	if c == nil || c.page == nil {
		return inputSyncViewport{}, fmt.Errorf("CDP client is nil")
	}
	if c.isClosed() {
		return inputSyncViewport{}, fmt.Errorf("CDP connection closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := proto.PageGetLayoutMetrics{}.Call(c.page.Context(ctx))
	if err != nil {
		return inputSyncViewport{}, err
	}
	if result.CSSVisualViewport != nil && result.CSSVisualViewport.ClientWidth > 0 && result.CSSVisualViewport.ClientHeight > 0 {
		return inputSyncViewport{width: result.CSSVisualViewport.ClientWidth, height: result.CSSVisualViewport.ClientHeight}, nil
	}
	if result.CSSLayoutViewport != nil && result.CSSLayoutViewport.ClientWidth > 0 && result.CSSLayoutViewport.ClientHeight > 0 {
		return inputSyncViewport{width: float64(result.CSSLayoutViewport.ClientWidth), height: float64(result.CSSLayoutViewport.ClientHeight)}, nil
	}
	return inputSyncViewport{}, fmt.Errorf("未能读取页面视口尺寸")
}

func (c *inputSyncPageClient) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.closing.Store(true)
		if c.cancel != nil {
			c.cancel()
		}
		if c.ws != nil {
			_ = c.ws.Close()
		}
		close(c.done)
	})
}

func (c *inputSyncPageClient) eventLoop() {
	if c == nil || c.page == nil || c.events == nil {
		return
	}
	wait := c.page.EachEvent(func(event *proto.RuntimeBindingCalled) {
		if event == nil || event.Name != inputSyncBindingName || strings.TrimSpace(event.Payload) == "" {
			return
		}
		var wire inputSyncWireEvent
		if err := json.Unmarshal([]byte(event.Payload), &wire); err != nil {
			logger.New("InputSync").Warn("输入同步主控事件解析失败",
				logger.F("role", c.role),
				logger.F("profile_id", c.profileID),
				logger.F("profile_name", c.profileName),
				logger.F("debug_port", c.debugPort),
				logger.F("target_id", c.targetID),
				logger.F("error", err.Error()),
			)
			return
		}
		select {
		case c.events <- wire:
		default:
			dropped := c.droppedEvents.Add(1)
			if dropped <= 3 || dropped%inputSyncTraceEvery == 0 {
				logger.New("InputSync").Warn("输入同步主控事件队列已满，事件被丢弃",
					logger.F("role", c.role),
					logger.F("profile_id", c.profileID),
					logger.F("profile_name", c.profileName),
					logger.F("debug_port", c.debugPort),
					logger.F("target_id", c.targetID),
					logger.F("dropped", dropped),
					logger.F("event", inputSyncEventTrace(wire)),
				)
			}
		}
	})
	wait()
	if c.closing.Load() {
		logger.New("InputSync").Debug("输入同步 rod 事件监听已关闭",
			logger.F("role", c.role),
			logger.F("profile_id", c.profileID),
			logger.F("profile_name", c.profileName),
			logger.F("debug_port", c.debugPort),
			logger.F("target_id", c.targetID),
		)
		return
	}
	logger.New("InputSync").Warn("输入同步 rod 事件监听异常结束",
		logger.F("role", c.role),
		logger.F("profile_id", c.profileID),
		logger.F("profile_name", c.profileName),
		logger.F("debug_port", c.debugPort),
		logger.F("target_id", c.targetID),
	)
	c.Close()
}

func (c *inputSyncPageClient) isClosed() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *inputSyncPageClient) logReadLoopClosed(err error) {
	if c == nil || err == nil {
		return
	}
	fields := []logger.Field{
		logger.F("role", c.role),
		logger.F("profile_id", c.profileID),
		logger.F("profile_name", c.profileName),
		logger.F("debug_port", c.debugPort),
		logger.F("error", err.Error()),
	}
	if c.closing.Load() {
		logger.New("InputSync").Debug("输入同步 CDP 连接已关闭", fields...)
		return
	}
	logger.New("InputSync").Warn("输入同步 CDP 连接异常断开", fields...)
}

func parseInputSyncViewport(value any) (inputSyncViewport, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return inputSyncViewport{}, false
	}
	width, widthOK := inputSyncNumber(item["clientWidth"])
	height, heightOK := inputSyncNumber(item["clientHeight"])
	if !widthOK || !heightOK || width <= 0 || height <= 0 {
		return inputSyncViewport{}, false
	}
	return inputSyncViewport{width: width, height: height}, true
}

func inputSyncNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	default:
		return 0, false
	}
}

func inputSyncViewportMatches(a inputSyncViewport, b inputSyncViewport) bool {
	return math.Abs(a.width-b.width) <= inputSyncViewportSlack && math.Abs(a.height-b.height) <= inputSyncViewportSlack
}

func probeInputSyncBridge(ctx context.Context, client *inputSyncPageClient, sessionID string) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("CDP client is nil")
	}
	sessionJSON, _ := json.Marshal(sessionID)
	result, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": `(function () {
  try {
    if (window.__facadeInputSyncSessionId !== ` + string(sessionJSON) + `) return false;
    if (typeof window.__facadeInputSyncProbe !== 'function') return false;
    return window.__facadeInputSyncProbe();
  } catch (_) {
    return false;
  }
})()`,
		"includeCommandLineAPI": false,
		"returnByValue":         true,
	})
	if err != nil {
		return false, err
	}
	return inputSyncRuntimeEvaluateBool(result), nil
}

func inputSyncRuntimeEvaluateBool(result map[string]any) bool {
	remoteObject, ok := result["result"].(map[string]any)
	if !ok {
		return false
	}
	value, ok := remoteObject["value"].(bool)
	return ok && value
}

func installInputSyncBridge(ctx context.Context, client *inputSyncPageClient, sessionID string) error {
	if client == nil {
		return fmt.Errorf("CDP client is nil")
	}
	if _, err := client.Call(ctx, "Runtime.enable", nil); err != nil {
		return err
	}
	if _, err := client.Call(ctx, "Page.enable", nil); err != nil {
		return err
	}
	if _, err := client.Call(ctx, "Runtime.addBinding", map[string]any{"name": inputSyncBindingName}); err != nil && !isInputSyncBindingAlreadyExistsError(err) {
		return err
	}
	if client.bridgeScriptID != "" {
		_, _ = client.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
			"identifier": client.bridgeScriptID,
		})
		client.bridgeScriptID = ""
	}
	source := inputSyncBridgeScript(sessionID)
	result, err := client.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": source})
	if err != nil {
		return err
	}
	if identifier, ok := result["identifier"].(string); ok {
		client.bridgeScriptID = proto.PageScriptIdentifier(identifier)
	}
	_, err = client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":            source,
		"includeCommandLineAPI": false,
		"returnByValue":         true,
	})
	return err
}

func isInputSyncBindingAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already") || strings.Contains(message, "duplicate")
}

func uninstallInputSyncBridge(ctx context.Context, client *inputSyncPageClient) error {
	if client == nil {
		return nil
	}
	if client.bridgeScriptID != "" {
		_, _ = client.Call(ctx, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{
			"identifier": client.bridgeScriptID,
		})
		client.bridgeScriptID = ""
	}
	_, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    `try { if (window.__facadeInputSyncCleanup) window.__facadeInputSyncCleanup(); true } catch (_) { false }`,
		"returnByValue": true,
	})
	_, _ = client.Call(ctx, "Runtime.removeBinding", map[string]any{"name": inputSyncBindingName})
	return err
}

func inputSyncBridgeScript(sessionID string) string {
	sessionJSON, _ := json.Marshal(sessionID)
	bindingJSON, _ := json.Marshal(inputSyncBindingName)
	return `(function () {
  var sessionId = ` + string(sessionJSON) + `;
  var bindingName = ` + string(bindingJSON) + `;
  try {
    if (window.__facadeInputSyncCleanup) window.__facadeInputSyncCleanup();
  } catch (_) {}

  var cleanups = [];
  var lastMoveAt = 0;
  var lastScrollAt = new WeakMap();
  var lastDocumentScrollAt = 0;
  var lastURL = '';
  var originalPushState = history.pushState;
  var originalReplaceState = history.replaceState;
  window.__facadeInputSyncSessionId = sessionId;

  function modifiersOf(event) {
    return (event.altKey ? 1 : 0) | (event.ctrlKey ? 2 : 0) | (event.metaKey ? 4 : 0) | (event.shiftKey ? 8 : 0);
  }

  function buttonName(button) {
    if (button === 0) return 'left';
    if (button === 1) return 'middle';
    if (button === 2) return 'right';
    if (button === 3) return 'back';
    if (button === 4) return 'forward';
    return 'none';
  }

  function printableText(event) {
    if (!event || event.ctrlKey || event.metaKey || event.altKey) return '';
    if (typeof event.key !== 'string') return '';
    return event.key.length === 1 ? event.key : '';
  }

  function cssEscape(value) {
    if (window.CSS && typeof window.CSS.escape === 'function') return window.CSS.escape(value);
    return String(value).replace(/[^a-zA-Z0-9_-]/g, function (ch) { return '\\' + ch; });
  }

  function selectorFor(element) {
    if (!element || element === window || element === document) return '';
    if (element.nodeType !== 1) element = element.parentElement;
    if (!element) return '';
    if (element.id) return '#' + cssEscape(element.id);
    var stableAttrs = ['data-testid', 'data-test', 'data-cy', 'name', 'aria-label', 'role'];
    for (var i = 0; i < stableAttrs.length; i++) {
      var attr = stableAttrs[i];
      var value = element.getAttribute && element.getAttribute(attr);
      if (value) {
        var byAttr = element.tagName.toLowerCase() + '[' + attr + '=' + JSON.stringify(value) + ']';
        try {
          if (document.querySelectorAll(byAttr).length === 1) return byAttr;
        } catch (_) {}
      }
    }
    var parts = [];
    var current = element;
    while (current && current.nodeType === 1 && current !== document.documentElement) {
      var tag = current.tagName.toLowerCase();
      var index = 1;
      var sibling = current;
      while ((sibling = sibling.previousElementSibling)) {
        if (sibling.tagName === current.tagName) index++;
      }
      parts.unshift(tag + ':nth-of-type(' + index + ')');
      var candidate = parts.join(' > ');
      try {
        if (document.querySelectorAll(candidate).length === 1) return candidate;
      } catch (_) {}
      current = current.parentElement;
    }
    if (parts.length) return parts.join(' > ');
    return '';
  }

  function elementBoxPayload(element, clientX, clientY) {
    var rect = element && element.getBoundingClientRect ? element.getBoundingClientRect() : null;
    if (!rect) {
      return { selector: '', relX: 0, relY: 0, elementX: 0, elementY: 0, elementWidth: 0, elementHeight: 0 };
    }
    var width = rect.width || 1;
    var height = rect.height || 1;
    return {
      selector: selectorFor(element),
      relX: Math.max(0, Math.min(1, (clientX - rect.left) / width)),
      relY: Math.max(0, Math.min(1, (clientY - rect.top) / height)),
      elementX: rect.left,
      elementY: rect.top,
      elementWidth: rect.width || 0,
      elementHeight: rect.height || 0
    };
  }

  function isEditable(element) {
    if (!element) return false;
    if (element.isContentEditable) return true;
    var tag = element.tagName ? element.tagName.toLowerCase() : '';
    return tag === 'input' || tag === 'textarea' || tag === 'select';
  }

  function shouldSyncKeyEvent(event) {
    if (!event) return false;
    if (event.ctrlKey || event.metaKey || event.altKey) return true;
    if (!isEditable(event.target)) return true;
    var key = event.key || '';
    if (key === 'Enter' || key === 'Tab' || key === 'Escape' || key === 'Backspace' || key === 'Delete') return true;
    if (key === 'Home' || key === 'End' || key === 'PageUp' || key === 'PageDown') return true;
    if (key.indexOf('Arrow') === 0) return true;
    return false;
  }

  function send(payload) {
    try {
      var binding = window[bindingName];
      if (typeof binding !== 'function') return false;
      payload.sessionId = sessionId;
      payload.sentAt = Date.now();
      binding(JSON.stringify(payload));
      return true;
    } catch (_) {
      return false;
    }
  }

  function add(type, handler) {
    window.addEventListener(type, handler, true);
    cleanups.push(function () { window.removeEventListener(type, handler, true); });
  }

  function pointerPayload(event, type) {
    var box = elementBoxPayload(event.target, event.clientX, event.clientY);
    return {
      kind: 'mouse',
      type: type,
      x: event.clientX,
      y: event.clientY,
      selector: box.selector,
      relX: box.relX,
      relY: box.relY,
      elementX: box.elementX,
      elementY: box.elementY,
      elementWidth: box.elementWidth,
      elementHeight: box.elementHeight,
      modifiers: modifiersOf(event),
      button: type === 'mouseMoved' ? 'none' : buttonName(event.button),
      buttons: event.buttons || 0,
      clickCount: event.detail || (type === 'mousePressed' || type === 'mouseReleased' ? 1 : 0)
    };
  }

  add('mousemove', function (event) {
    var now = Date.now();
    if (now - lastMoveAt < 16) return;
    lastMoveAt = now;
    send(pointerPayload(event, 'mouseMoved'));
  });

  add('mousedown', function (event) {
    send(pointerPayload(event, 'mousePressed'));
  });

  add('mouseup', function (event) {
    send(pointerPayload(event, 'mouseReleased'));
  });

  add('input', function (event) {
    var el = event.target;
    if (!el) return;
    var value = '';
    if ('value' in el) value = String(el.value == null ? '' : el.value);
    else if (el.isContentEditable) value = String(el.textContent == null ? '' : el.textContent);
    var selectionStart = 0;
    var selectionEnd = 0;
    try {
      if (typeof el.selectionStart === 'number') selectionStart = el.selectionStart;
      if (typeof el.selectionEnd === 'number') selectionEnd = el.selectionEnd;
    } catch (_) {}
    send({
      kind: 'input',
      type: 'valueChanged',
      selector: selectorFor(el),
      value: value,
      checked: !!el.checked,
      hasChecked: 'checked' in el,
      selectionStart: selectionStart,
      selectionEnd: selectionEnd
    });
  });

  add('scroll', function (event) {
    var target = event.target;
    var isDocument = target === document || target === window || target === document.documentElement || target === document.body;
    var now = Date.now();
    if (isDocument) {
      if (now - lastDocumentScrollAt < 50) return;
      lastDocumentScrollAt = now;
    } else {
      var previous = lastScrollAt.get(target) || 0;
      if (now - previous < 50) return;
      lastScrollAt.set(target, now);
    }
    var el = isDocument ? (document.scrollingElement || document.documentElement || document.body) : target;
    send({
      kind: 'scroll',
      type: 'position',
      selector: isDocument ? '' : selectorFor(el),
      scrollLeft: el ? (el.scrollLeft || 0) : 0,
      scrollTop: el ? (el.scrollTop || 0) : 0
    });
  });

  add('keydown', function (event) {
    if (!shouldSyncKeyEvent(event)) return;
    send({
      kind: 'key',
      type: 'keyDown',
      modifiers: modifiersOf(event),
      key: event.key || '',
      code: event.code || '',
      text: isEditable(event.target) ? '' : printableText(event),
      unmodifiedText: isEditable(event.target) ? '' : printableText(event),
      windowsVirtualKeyCode: event.keyCode || event.which || 0,
      nativeVirtualKeyCode: event.keyCode || event.which || 0,
      autoRepeat: !!event.repeat,
      location: event.location || 0,
      isSystemKey: !!event.altKey
    });
  });

  add('keyup', function (event) {
    if (!shouldSyncKeyEvent(event)) return;
    send({
      kind: 'key',
      type: 'keyUp',
      modifiers: modifiersOf(event),
      key: event.key || '',
      code: event.code || '',
      text: '',
      unmodifiedText: '',
      windowsVirtualKeyCode: event.keyCode || event.which || 0,
      nativeVirtualKeyCode: event.keyCode || event.which || 0,
      autoRepeat: false,
      location: event.location || 0,
      isSystemKey: !!event.altKey
    });
  });

  function notifyNavigation(reason) {
    var current = String(location.href || '');
    if (!current || current === lastURL) return;
    lastURL = current;
    send({
      kind: 'navigation',
      type: reason || 'navigated',
      url: current,
      title: String(document.title || '')
    });
  }

  try {
    history.pushState = function () {
      var result = originalPushState.apply(this, arguments);
      setTimeout(function () { notifyNavigation('pushState'); }, 0);
      return result;
    };
    history.replaceState = function () {
      var result = originalReplaceState.apply(this, arguments);
      setTimeout(function () { notifyNavigation('replaceState'); }, 0);
      return result;
    };
    cleanups.push(function () {
      try { history.pushState = originalPushState; } catch (_) {}
      try { history.replaceState = originalReplaceState; } catch (_) {}
    });
  } catch (_) {}

  add('popstate', function () { setTimeout(function () { notifyNavigation('popstate'); }, 0); });
  add('hashchange', function () { setTimeout(function () { notifyNavigation('hashchange'); }, 0); });
  add('pageshow', function () { setTimeout(function () { notifyNavigation('pageshow'); }, 0); });
  add('visibilitychange', function () {
    if (!document.hidden) {
      send({
        kind: 'tab',
        type: 'activated',
        url: String(location.href || ''),
        title: String(document.title || '')
      });
    }
  });
  setTimeout(function () { notifyNavigation('initial'); }, 0);
  var navigationPoll = setInterval(function () { notifyNavigation('poll'); }, 500);
  cleanups.push(function () { clearInterval(navigationPoll); });

  window.__facadeInputSyncProbe = function () {
    return send({
      kind: 'health',
      type: 'probe'
    });
  };

  window.__facadeInputSyncCleanup = function () {
    while (cleanups.length) {
      try { cleanups.pop()(); } catch (_) {}
    }
    try { delete window.__facadeInputSyncProbe; } catch (_) { window.__facadeInputSyncProbe = null; }
    try { delete window.__facadeInputSyncSessionId; } catch (_) { window.__facadeInputSyncSessionId = null; }
    try { delete window.__facadeInputSyncCleanup; } catch (_) { window.__facadeInputSyncCleanup = null; }
  };
  return true;
})()`
}
