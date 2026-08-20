package backend

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormalizeInputSyncIDsDedupesAndSkipsMaster(t *testing.T) {
	got := normalizeInputSyncIDs([]string{" target-1 ", "master", "target-1", "", "target-2"}, "master")
	want := []string{"target-1", "target-2"}
	if len(got) != len(want) {
		t.Fatalf("expected %d ids, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("id %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestInputSyncDebugWaitCandidates(t *testing.T) {
	master := inputSyncProfileRuntime{
		profileID:  "master",
		debugPort:  9101,
		running:    true,
		debugReady: false,
	}
	targets := []inputSyncProfileRuntime{
		{profileID: "ready", debugPort: 9102, running: true, debugReady: true},
		{profileID: "missing-port", running: true},
		{profileID: "warned", debugPort: 9103, running: true, warning: "不可用"},
		{profileID: "target", debugPort: 9104, running: true},
		{profileID: "target", debugPort: 9104, running: true},
	}

	got := inputSyncDebugWaitCandidates(master, targets)
	want := []inputSyncDebugWaitCandidate{
		{profileID: "master", debugPort: 9101},
		{profileID: "target", debugPort: 9104},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d candidates, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d: expected %#v, got %#v", i, want[i], got[i])
		}
	}
}

func TestInputSyncViewportMatchesAllowsSmallSlack(t *testing.T) {
	if !inputSyncViewportMatches(
		inputSyncViewport{width: 1280, height: 720},
		inputSyncViewport{width: 1281.5, height: 718.5},
	) {
		t.Fatal("expected close viewports to match")
	}
	if inputSyncViewportMatches(
		inputSyncViewport{width: 1280, height: 720},
		inputSyncViewport{width: 1024, height: 720},
	) {
		t.Fatal("expected materially different viewports to fail")
	}
}

func TestInputSyncMouseEventToCDP(t *testing.T) {
	command, ok := inputSyncEventToCDP(inputSyncWireEvent{
		Kind:       "mouse",
		Type:       "mousePressed",
		X:          42,
		Y:          64,
		Modifiers:  10,
		Button:     "left",
		Buttons:    1,
		ClickCount: 2,
	})
	if !ok {
		t.Fatal("expected mouse event to map")
	}
	if command.method != "Input.dispatchMouseEvent" {
		t.Fatalf("unexpected method: %s", command.method)
	}
	if command.mouse == nil {
		t.Fatal("expected mouse command")
	}
	if string(command.mouse.Type) != "mousePressed" || command.mouse.X != float64(42) || command.mouse.Y != float64(64) {
		t.Fatalf("unexpected mouse command: %#v", command.mouse)
	}
	if string(command.mouse.Button) != "left" || command.mouse.Buttons == nil || *command.mouse.Buttons != 1 || command.mouse.ClickCount != 2 {
		t.Fatalf("unexpected button command: %#v", command.mouse)
	}
}

func TestInputSyncCanSkipUnmappedMouseActionOnlyForHover(t *testing.T) {
	if !inputSyncCanSkipUnmappedMouseAction(inputSyncWireEvent{Type: "mouseMoved", Buttons: 0}) {
		t.Fatal("expected hover move without DOM target to be skippable")
	}
	if inputSyncCanSkipUnmappedMouseAction(inputSyncWireEvent{Type: "mouseMoved", Buttons: 1}) {
		t.Fatal("expected drag move without DOM target to fail instead of falling back to viewport coordinates")
	}
	if inputSyncCanSkipUnmappedMouseAction(inputSyncWireEvent{Type: "mousePressed", Buttons: 1}) {
		t.Fatal("expected click without DOM target to fail instead of falling back to viewport coordinates")
	}
}

func TestInputSyncActionReadinessWaitsForPageBasics(t *testing.T) {
	got := inputSyncEvaluateActionReadiness(
		inputSyncAction{Kind: "key"},
		"",
		inputSyncPageActionSnapshot{url: "https://example.test", readyState: "loading", hasBody: false},
	)
	if got.ready || !got.waitable {
		t.Fatalf("expected basic page loading to be waitable, got %#v", got)
	}
	if !strings.Contains(got.reason, "页面主体") {
		t.Fatalf("expected body loading reason, got %q", got.reason)
	}
}

func TestInputSyncActionReadinessDoesNotWaitBeforeTargetStartsLoading(t *testing.T) {
	got := inputSyncEvaluateActionReadiness(
		inputSyncAction{Kind: "key"},
		"https://example.test/target",
		inputSyncPageActionSnapshot{url: "https://example.test/current", readyState: "complete", hasBody: true},
	)
	if got.ready || got.waitable || !got.urlMismatch {
		t.Fatalf("expected URL mismatch before loading to be terminal, got %#v", got)
	}
	if !strings.Contains(got.reason, "尚未开始导航") {
		t.Fatalf("expected URL wait reason, got %q", got.reason)
	}
}

func TestInputSyncActionReadinessWaitsAfterTargetStartsLoading(t *testing.T) {
	got := inputSyncEvaluateActionReadiness(
		inputSyncAction{Kind: "key"},
		"https://example.test/target",
		inputSyncPageActionSnapshot{url: "https://example.test/current", readyState: "loading", hasBody: true},
	)
	if got.ready || !got.waitable || !got.urlMismatch {
		t.Fatalf("expected loading URL mismatch to be waitable, got %#v", got)
	}
}

func TestInputSyncActionReadinessDoesNotWaitForCompletePageWithoutBody(t *testing.T) {
	got := inputSyncEvaluateActionReadiness(
		inputSyncAction{Kind: "key"},
		"",
		inputSyncPageActionSnapshot{url: "about:blank", readyState: "complete", hasBody: false},
	)
	if got.ready || got.waitable {
		t.Fatalf("expected complete page without body to avoid loading wait, got %#v", got)
	}
}

func TestInputSyncActionReadinessDistinguishesLoadingAndMissingSelector(t *testing.T) {
	action := inputSyncAction{Kind: "mouse", Event: inputSyncWireEvent{Selector: "#submit"}}
	loading := inputSyncEvaluateActionReadiness(
		action,
		"",
		inputSyncPageActionSnapshot{
			url:             "https://example.test",
			readyState:      "interactive",
			hasBody:         true,
			selectorChecked: true,
		},
	)
	if loading.ready || !loading.waitable {
		t.Fatalf("expected missing selector before complete to be waitable, got %#v", loading)
	}

	complete := inputSyncEvaluateActionReadiness(
		action,
		"",
		inputSyncPageActionSnapshot{
			url:             "https://example.test",
			readyState:      "complete",
			hasBody:         true,
			selectorChecked: true,
		},
	)
	if complete.ready || complete.waitable {
		t.Fatalf("expected missing selector after complete to be terminal, got %#v", complete)
	}
	if complete.reason != "目标元素不存在" {
		t.Fatalf("unexpected terminal selector reason: %q", complete.reason)
	}
}

func TestInputSyncActionReadinessWaitsForInputWritableUntilComplete(t *testing.T) {
	action := inputSyncAction{Kind: "input", Event: inputSyncWireEvent{Selector: "#email"}}
	loading := inputSyncEvaluateActionReadiness(
		action,
		"",
		inputSyncPageActionSnapshot{
			url:             "https://example.test",
			readyState:      "interactive",
			hasBody:         true,
			selectorChecked: true,
			selectorFound:   true,
			selectorVisible: true,
		},
	)
	if loading.ready || !loading.waitable {
		t.Fatalf("expected unwritable input before complete to be waitable, got %#v", loading)
	}

	ready := inputSyncEvaluateActionReadiness(
		action,
		"",
		inputSyncPageActionSnapshot{
			url:              "https://example.test",
			readyState:       "complete",
			hasBody:          true,
			selectorChecked:  true,
			selectorFound:    true,
			selectorVisible:  true,
			selectorWritable: true,
		},
	)
	if !ready.ready || ready.waitable {
		t.Fatalf("expected writable input to be ready, got %#v", ready)
	}
}

func TestInputSyncActionNeedsReadinessSkipsUnmappedHover(t *testing.T) {
	if inputSyncActionNeedsReadiness(inputSyncAction{Kind: "mouse", Event: inputSyncWireEvent{Type: "mouseMoved", Buttons: 0}}) {
		t.Fatal("expected plain hover without DOM target to skip readiness waiting")
	}
}

func TestInputSyncKeyUpClearsText(t *testing.T) {
	command, ok := inputSyncEventToCDP(inputSyncWireEvent{
		Kind:           "key",
		Type:           "keyUp",
		Key:            "a",
		Code:           "KeyA",
		Text:           "a",
		UnmodifiedText: "a",
		WindowsKeyCode: 65,
		NativeKeyCode:  65,
	})
	if !ok {
		t.Fatal("expected key event to map")
	}
	if command.method != "Input.dispatchKeyEvent" {
		t.Fatalf("unexpected method: %s", command.method)
	}
	if command.key == nil {
		t.Fatal("expected key command")
	}
	if command.key.Text != "" || command.key.UnmodifiedText != "" {
		t.Fatalf("expected keyUp text fields to be empty: %#v", command.key)
	}
	if command.key.Key != "a" || command.key.Code != "KeyA" {
		t.Fatalf("unexpected key command: %#v", command.key)
	}
}

func TestInputSyncActionFromEventCarriesDOMState(t *testing.T) {
	action, ok := inputSyncActionFromEvent(inputSyncWireEvent{
		SessionId:      "session-1",
		Kind:           "input",
		Type:           "valueChanged",
		Selector:       "#email",
		Value:          "hello@example.test",
		SourceTargetId: "page-1",
	}, 42)
	if !ok {
		t.Fatal("expected input event to map to an action")
	}
	if action.Sequence != 42 || action.Kind != "input" || action.Type != "valueChanged" {
		t.Fatalf("unexpected action metadata: %#v", action)
	}
	if action.SourceTargetID != "page-1" || action.Event.Selector != "#email" || action.Event.Value != "hello@example.test" {
		t.Fatalf("unexpected action payload: %#v", action)
	}
}

func TestInputSyncActionFromEventSkipsHealth(t *testing.T) {
	if _, ok := inputSyncActionFromEvent(inputSyncWireEvent{Kind: "health", Type: "probe"}, 1); ok {
		t.Fatal("expected health events to be skipped")
	}
}

func TestInputSyncNavigationFilter(t *testing.T) {
	if !inputSyncCanNavigate("https://example.com/path") {
		t.Fatal("expected https navigation to be allowed")
	}
	if inputSyncCanNavigate("javascript:alert(1)") {
		t.Fatal("expected javascript URLs to be blocked")
	}
}

func TestInputSyncBridgeScriptCapturesStateActions(t *testing.T) {
	script := inputSyncBridgeScript("session-1")
	for _, needle := range []string{
		"selectorFor",
		"kind: 'input'",
		"kind: 'scroll'",
		"kind: 'navigation'",
		"history.pushState",
		"visibilitychange",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("expected bridge script to contain %q", needle)
		}
	}
}

func TestInputSyncDetectChallengeSignal(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		title string
		body  string
	}{
		{name: "google-sorry-url", url: "https://www.google.com/sorry/index?continue=https://example.test"},
		{name: "recaptcha-body", body: "Please solve this reCAPTCHA challenge"},
		{name: "human-verify-title", title: "Verify you are human"},
		{name: "chinese-captcha", body: "请输入验证码继续"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, reason := inputSyncDetectChallengeSignal(tt.url, tt.title, tt.body)
			if !detected {
				t.Fatalf("expected challenge signal for %#v", tt)
			}
			if !strings.Contains(reason, "已暂停该实例") {
				t.Fatalf("expected pause reason, got %q", reason)
			}
		})
	}
}

func TestInputSyncDetectChallengeSignalIgnoresGenericBodyMentions(t *testing.T) {
	detected, reason := inputSyncDetectChallengeSignal(
		"https://example.test/docs/forms",
		"Help Center",
		"This article explains how captcha widgets work in signup forms.",
	)
	if detected {
		t.Fatalf("expected generic body mention to be ignored, got reason %q", reason)
	}
}

func TestInputSyncPausedTargetDropsActions(t *testing.T) {
	target := &inputSyncTarget{queue: make(chan inputSyncAction, 1)}
	if !target.enqueueAction(inputSyncAction{Sequence: 1, Kind: "mouse"}) {
		t.Fatal("expected active target to enqueue action")
	}
	target.pause("疑似验证码/风控页面")
	if target.enqueueAction(inputSyncAction{Sequence: 2, Kind: "mouse"}) {
		t.Fatal("expected paused target to drop action")
	}
	if got := target.droppedActions.Load(); got != 1 {
		t.Fatalf("expected one dropped action, got %d", got)
	}
}

func TestInputSyncTabClosedUnbindsAndClosesClient(t *testing.T) {
	client := &inputSyncPageClient{targetID: "target-1", done: make(chan struct{})}
	target := &inputSyncTarget{clients: map[string]*inputSyncPageClient{"source-1": client}, activeSourceID: "source-1", client: client}

	err := target.applyTabAction(inputSyncAction{
		Kind:           "tab",
		Type:           "closed",
		SourceTargetID: "source-1",
	})
	if err != nil {
		t.Fatalf("expected tab close to succeed, got %v", err)
	}
	if got := target.exactClientForSource("source-1"); got != nil {
		t.Fatal("expected source client to be unbound")
	}
	if target.getClient() != nil {
		t.Fatal("expected active client to be cleared")
	}
	select {
	case <-client.done:
	default:
		t.Fatal("expected client connection to be closed")
	}
}

func TestInputSyncMasterClientDoneDispatchesTabClosed(t *testing.T) {
	sourceClient := &inputSyncPageClient{
		targetID:    "source-tab-1",
		targetURL:   "https://example.test/closed",
		targetTitle: "Closed",
		events:      make(chan inputSyncWireEvent),
		done:        make(chan struct{}),
	}
	targetClient := &inputSyncPageClient{targetID: "target-tab-1", done: make(chan struct{})}
	target := &inputSyncTarget{
		profileID: "target-profile",
		name:      "Target",
		clients:   make(map[string]*inputSyncPageClient),
		queue:     make(chan inputSyncAction, 1),
	}
	target.bindClient(sourceClient.targetID, targetClient)
	session := &inputSyncSession{
		id:            "session-1",
		masterClients: map[string]*inputSyncPageClient{sourceClient.targetID: sourceClient},
		master:        sourceClient,
		targets:       []*inputSyncTarget{target},
	}

	done := make(chan struct{})
	go func() {
		session.forwardMasterClientEvents(context.Background(), sourceClient)
		close(done)
	}()
	close(sourceClient.done)

	select {
	case action := <-target.queue:
		if action.Kind != "tab" || action.Type != "closed" {
			t.Fatalf("expected tab closed action, got kind=%s type=%s", action.Kind, action.Type)
		}
		if action.SourceTargetID != sourceClient.targetID {
			t.Fatalf("expected source target id %q, got %q", sourceClient.targetID, action.SourceTargetID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected tab closed action to be dispatched")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected master event forwarder to exit")
	}
	if session.hasMasterClient(sourceClient.targetID) {
		t.Fatal("expected source client to be removed")
	}
}

func TestInputSyncPauseSnapshotCarriesRecoverMetadata(t *testing.T) {
	target := &inputSyncTarget{}
	target.pauseWithKind(inputSyncPauseKindDOMMismatch, "目标元素不存在", true)

	snapshot := target.pauseSnapshot()
	if !snapshot.paused {
		t.Fatal("expected target to be paused")
	}
	if snapshot.kind != inputSyncPauseKindDOMMismatch {
		t.Fatalf("unexpected pause kind: %s", snapshot.kind)
	}
	if !snapshot.recoverable {
		t.Fatal("expected DOM mismatch pause to be recoverable")
	}
	if snapshot.nextRecoverAt.IsZero() {
		t.Fatal("expected next recover time to be scheduled")
	}
}

func TestInputSyncRecoverAttemptsStopAfterLimit(t *testing.T) {
	target := &inputSyncTarget{}
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	target.pauseWithKind(inputSyncPauseKindCDPDisconnected, "CDP 断开", true)

	for i := 1; i <= inputSyncMaxRecoverAttempts; i++ {
		attempt, err := target.beginRecoverAttempt(false, now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("attempt %d should start: %v", i, err)
		}
		if attempt != i {
			t.Fatalf("expected attempt %d, got %d", i, attempt)
		}
		target.finishRecoverFailure(fmt.Errorf("still offline"), now.Add(time.Duration(i)*time.Minute))
	}

	snapshot := target.pauseSnapshot()
	if snapshot.recoverable {
		t.Fatal("expected target to stop automatic recovery after max attempts")
	}
	if snapshot.nextRecoverAt.IsZero() == false {
		t.Fatal("expected next recover time to be cleared after max attempts")
	}
}

func TestInputSyncReserveResyncAttemptLimitsAndCoolsDown(t *testing.T) {
	target := &inputSyncTarget{}
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	attempt, coolingDown, reason := target.reserveResyncAttempt("source-a", "https://example.test/a", now)
	if attempt != 1 || coolingDown || reason != "" {
		t.Fatalf("unexpected first attempt: attempt=%d cooling=%t reason=%q", attempt, coolingDown, reason)
	}

	attempt, coolingDown, reason = target.reserveResyncAttempt("source-a", "https://example.test/a", now.Add(time.Second))
	if attempt != 1 || !coolingDown || reason != "" {
		t.Fatalf("expected cooldown after first attempt: attempt=%d cooling=%t reason=%q", attempt, coolingDown, reason)
	}

	attempt, coolingDown, reason = target.reserveResyncAttempt("source-a", "https://example.test/a", now.Add(inputSyncResyncCooldown+time.Second))
	if attempt != 2 || coolingDown || reason != "" {
		t.Fatalf("unexpected second attempt: attempt=%d cooling=%t reason=%q", attempt, coolingDown, reason)
	}

	attempt, coolingDown, reason = target.reserveResyncAttempt("source-a", "https://example.test/a", now.Add(2*inputSyncResyncCooldown+time.Second))
	if attempt != inputSyncMaxResyncTries || coolingDown || reason == "" {
		t.Fatalf("expected pause after max attempts: attempt=%d cooling=%t reason=%q", attempt, coolingDown, reason)
	}
	if !target.isPaused() {
		t.Fatal("expected target to be paused")
	}
	snapshot := target.pauseSnapshot()
	if snapshot.kind != inputSyncPauseKindURLDeviation || !snapshot.recoverable {
		t.Fatalf("expected recoverable URL deviation pause, got kind=%s recoverable=%t", snapshot.kind, snapshot.recoverable)
	}
}

func TestInputSyncTabClosedClearsExpectedURLIsolation(t *testing.T) {
	clientA := &inputSyncPageClient{targetID: "page-a", done: make(chan struct{})}
	clientB := &inputSyncPageClient{targetID: "page-b", done: make(chan struct{})}
	target := &inputSyncTarget{
		clients:  make(map[string]*inputSyncPageClient),
		tabState: make(map[string]*inputSyncTabState),
	}
	target.bindClient("source-a", clientA)
	target.setExpectedURL("source-a", "https://a.test")
	target.bindClient("source-b", clientB)
	target.setExpectedURL("source-b", "https://b.test")

	if err := target.applyTabAction(inputSyncAction{
		Kind:           "tab",
		Type:           "closed",
		SourceTargetID: "source-b",
	}); err != nil {
		t.Fatalf("close tab failed: %v", err)
	}

	if got := target.expectedURLFor("source-a"); got != "https://a.test" {
		t.Fatalf("expected source-a URL preserved, got %q", got)
	}
	if got := target.expectedURLFor("source-b"); got != "" {
		t.Fatalf("expected source-b URL cleared, got %q", got)
	}
	if got := target.expectedURLSnapshot(); got != "https://a.test" {
		t.Fatalf("expected mirrored active URL to become source-a, got %q", got)
	}
	entries := target.clientSourceSnapshot()
	if len(entries) != 1 || entries[0].sourceID != "source-a" {
		t.Fatalf("expected only source-a remaining for verifyState, got %#v", entries)
	}
}

func TestInputSyncResidualNavigationForClosedTabIsDropped(t *testing.T) {
	clientA := &inputSyncPageClient{targetID: "page-a", done: make(chan struct{})}
	clientB := &inputSyncPageClient{targetID: "page-b", done: make(chan struct{})}
	target := &inputSyncTarget{
		clients:  make(map[string]*inputSyncPageClient),
		tabState: make(map[string]*inputSyncTabState),
	}
	target.bindClient("source-a", clientA)
	target.setExpectedURL("source-a", "https://a.test")
	target.bindClient("source-b", clientB)
	target.setExpectedURL("source-b", "https://b.test")
	_ = target.applyTabAction(inputSyncAction{
		Kind:           "tab",
		Type:           "closed",
		SourceTargetID: "source-b",
	})

	session := &inputSyncSession{id: "session-1"}
	target.applyQueuedAction(context.Background(), session, inputSyncAction{
		Sequence:       99,
		Kind:           "navigation",
		Type:           "navigated",
		SourceTargetID: "source-b",
		Event:          inputSyncWireEvent{URL: "https://b.test/closed"},
	})

	if got := target.expectedURLFor("source-a"); got != "https://a.test" {
		t.Fatalf("expected source-a URL untouched, got %q", got)
	}
	if got := target.expectedURLFor("source-b"); got != "" {
		t.Fatalf("expected closed source URL to stay empty, got %q", got)
	}
	if got := target.expectedURLSnapshot(); got != "https://a.test" {
		t.Fatalf("expected active mirrored URL unchanged, got %q", got)
	}
	if target.appliedActions.Load() != 0 {
		t.Fatalf("expected residual navigation not applied, got %d", target.appliedActions.Load())
	}
}

func TestInputSyncClientForActionDoesNotFallbackAfterClose(t *testing.T) {
	clientA := &inputSyncPageClient{targetID: "page-a", done: make(chan struct{})}
	clientB := &inputSyncPageClient{targetID: "page-b", done: make(chan struct{})}
	target := &inputSyncTarget{clients: make(map[string]*inputSyncPageClient)}
	target.bindClient("source-a", clientA)
	target.bindClient("source-b", clientB)
	_ = target.unbindClient("source-b")

	if got := target.clientForAction(inputSyncAction{SourceTargetID: "source-b"}); got != nil {
		t.Fatal("expected closed source mapping to return nil instead of remaining tab")
	}
	if got := target.clientForAction(inputSyncAction{SourceTargetID: "source-a"}); got != clientA {
		t.Fatal("expected live source mapping to still resolve")
	}
}

func TestInputSyncMatchLivePageRequiresPreferredID(t *testing.T) {
	live := []inputSyncPageTarget{
		{id: "page-1", url: "https://one.test"},
		{id: "page-2", url: "https://two.test"},
	}
	matched, ok := inputSyncMatchLivePage(live, "page-2")
	if !ok || matched.id != "page-2" {
		t.Fatalf("expected exact match for page-2, got ok=%t id=%q", ok, matched.id)
	}
	if _, ok := inputSyncMatchLivePage(live, "missing"); ok {
		t.Fatal("expected missing preferred id to fail without falling back to [0]")
	}
	if _, ok := inputSyncMatchLivePage(live, ""); ok {
		t.Fatal("expected empty preferred id to fail")
	}
}

func TestInputSyncPickLivePageForReconnectSingleTabFallback(t *testing.T) {
	live := []inputSyncPageTarget{{id: "only-page", url: "https://only.test"}}
	matched, ok := inputSyncPickLivePageForReconnect(live, "stale-id", 1)
	if !ok || matched.id != "only-page" {
		t.Fatalf("expected single-tab fallback, got ok=%t id=%q", ok, matched.id)
	}

	multi := []inputSyncPageTarget{
		{id: "page-1", url: "https://one.test"},
		{id: "page-2", url: "https://two.test"},
	}
	if _, ok := inputSyncPickLivePageForReconnect(multi, "stale-id", 2); ok {
		t.Fatal("expected multi-tab reconnect without preferred id to refuse stealing another page")
	}
	if _, ok := inputSyncPickLivePageForReconnect(multi, "stale-id", 1); ok {
		t.Fatal("expected single mapping with multiple live pages to refuse fallback")
	}
}

func TestInputSyncReplaceClientForSourceKeepsSibling(t *testing.T) {
	clientA := &inputSyncPageClient{targetID: "page-a", done: make(chan struct{})}
	clientB := &inputSyncPageClient{targetID: "page-b", done: make(chan struct{})}
	nextA := &inputSyncPageClient{targetID: "page-a-reconnected", done: make(chan struct{})}
	target := &inputSyncTarget{clients: make(map[string]*inputSyncPageClient)}
	target.bindClient("source-a", clientA)
	target.bindClient("source-b", clientB)

	previous := target.replaceClientForSource("source-a", nextA)
	if previous != clientA {
		t.Fatal("expected previous source-a client to be returned")
	}
	if got := target.exactClientForSource("source-a"); got != nextA {
		t.Fatal("expected source-a to be replaced")
	}
	if got := target.exactClientForSource("source-b"); got != clientB {
		t.Fatal("expected source-b client to remain unchanged")
	}
}

func TestInputSyncErrorNeedsReconnect(t *testing.T) {
	if !inputSyncErrorNeedsReconnect(fmt.Errorf("websocket: close 1006 unexpected eof")) {
		t.Fatal("expected websocket EOF to allow one reconnect")
	}
	if inputSyncErrorNeedsReconnect(fmt.Errorf("execution context was destroyed, most likely because of a navigation")) {
		t.Fatal("expected page loading errors to avoid reconnect")
	}
	if inputSyncErrorNeedsReconnect(fmt.Errorf("目标元素不存在: #email")) {
		t.Fatal("expected DOM mismatch to avoid reconnect")
	}
}

func TestInputSyncPauseKindForActionError(t *testing.T) {
	if got := inputSyncPauseKindForActionError(fmt.Errorf("websocket: unexpected eof")); got != inputSyncPauseKindCDPDisconnected {
		t.Fatalf("expected CDP pause kind, got %s", got)
	}
	if got := inputSyncPauseKindForActionError(fmt.Errorf("目标元素不存在或不可见: button")); got != inputSyncPauseKindDOMMismatch {
		t.Fatalf("expected DOM pause kind, got %s", got)
	}
	if got := inputSyncPauseKindForActionError(fmt.Errorf("unknown action failure")); got != inputSyncPauseKindActionFailures {
		t.Fatalf("expected action failure pause kind, got %s", got)
	}
}

func TestInputSyncPlanTabActionSemantics(t *testing.T) {
	cases := []struct {
		name         string
		actionType   string
		hasClient    bool
		clientClosed bool
		want         inputSyncTabPlan
	}{
		{name: "activated without mapping ignores", actionType: "activated", hasClient: false, want: inputSyncTabPlanIgnore},
		{name: "activated with closed client reconnects", actionType: "activated", hasClient: true, clientClosed: true, want: inputSyncTabPlanReconnect},
		{name: "activated with live client binds", actionType: "activated", hasClient: true, clientClosed: false, want: inputSyncTabPlanBind},
		{name: "created without mapping creates", actionType: "created", hasClient: false, want: inputSyncTabPlanCreate},
		{name: "created with closed client reconnects", actionType: "created", hasClient: true, clientClosed: true, want: inputSyncTabPlanReconnect},
		{name: "created with live client binds", actionType: "created", hasClient: true, clientClosed: false, want: inputSyncTabPlanBind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inputSyncPlanTabAction(tc.actionType, tc.hasClient, tc.clientClosed)
			if got != tc.want {
				t.Fatalf("got plan %d, want %d", got, tc.want)
			}
		})
	}
}

func TestInputSyncUnmappedTabsToClose(t *testing.T) {
	live := []inputSyncPageTarget{
		{id: "mapped-a"},
		{id: "popup"},
		{id: "user-opened"},
		{id: ""},
	}
	mapped := map[string]struct{}{"mapped-a": {}}
	got := inputSyncUnmappedTabsToClose(live, mapped)
	if len(got) != 2 || got[0] != "popup" || got[1] != "user-opened" {
		t.Fatalf("expected popup and user-opened, got %#v", got)
	}

	all := inputSyncUnmappedTabsToClose(live, nil)
	if len(all) != 3 || all[0] != "mapped-a" || all[1] != "popup" || all[2] != "user-opened" {
		t.Fatalf("expected all non-empty live ids when mapped empty, got %#v", all)
	}

	none := inputSyncUnmappedTabsToClose(live, map[string]struct{}{
		"mapped-a":    {},
		"popup":       {},
		"user-opened": {},
	})
	if len(none) != 0 {
		t.Fatalf("expected no tabs to close, got %#v", none)
	}
}

func TestInputSyncActivateNilClientIsSafe(t *testing.T) {
	var client *inputSyncPageClient
	if err := client.Activate(); err != nil {
		t.Fatalf("nil Activate should be safe, got %v", err)
	}
}

func TestInputSyncAnnouncedTabDedupesCreated(t *testing.T) {
	session := &inputSyncSession{announcedTabs: make(map[string]struct{})}
	if !session.shouldAnnounceTab("master-tab-1") {
		t.Fatal("expected first announce to succeed")
	}
	if session.shouldAnnounceTab("master-tab-1") {
		t.Fatal("expected duplicate announce to be suppressed")
	}
	session.unmarkAnnouncedTab("master-tab-1")
	if !session.shouldAnnounceTab("master-tab-1") {
		t.Fatal("expected announce after closed to succeed again")
	}
}

func TestInputSyncActivatedWithoutMappingDoesNotCreate(t *testing.T) {
	target := &inputSyncTarget{
		profileID: "target",
		name:      "Target",
		debugPort: 1,
		clients:   make(map[string]*inputSyncPageClient),
		tabState:  make(map[string]*inputSyncTabState),
		ownedTabs: make(map[string]struct{}),
	}
	err := target.applyTabAction(inputSyncAction{
		Kind:           "tab",
		Type:           "activated",
		SourceTargetID: "missing-source",
		Event:          inputSyncWireEvent{URL: "https://example.test/activated"},
	})
	if err != nil {
		t.Fatalf("expected activated without mapping to be ignored, got %v", err)
	}
	if target.exactClientForSource("missing-source") != nil {
		t.Fatal("expected activated without mapping to leave clients empty")
	}
	if len(target.ownedTabs) != 0 {
		t.Fatalf("expected no owned tabs, got %#v", target.ownedTabs)
	}
}

func TestInputSyncTabActionFailureDoesNotReconnectRetry(t *testing.T) {
	client := &inputSyncPageClient{targetID: "page-1", done: make(chan struct{})}
	target := &inputSyncTarget{
		profileID: "target",
		name:      "Target",
		debugPort: 1,
		clients:   make(map[string]*inputSyncPageClient),
		tabState:  make(map[string]*inputSyncTabState),
		ownedTabs: make(map[string]struct{}),
	}
	target.bindClient("source-1", client)
	client.Close()

	session := &inputSyncSession{id: "session-1"}
	target.applyQueuedAction(context.Background(), session, inputSyncAction{
		Sequence:       7,
		Kind:           "tab",
		Type:           "activated",
		SourceTargetID: "source-1",
		Event:          inputSyncWireEvent{URL: "https://example.test/a"},
	})
	if got := target.failedActions.Load(); got != 1 {
		t.Fatalf("expected exactly one recorded failure without reconnect retry, got %d", got)
	}
	if got := target.appliedActions.Load(); got != 0 {
		t.Fatalf("expected tab reconnect failure not to count as applied, got %d", got)
	}
}
