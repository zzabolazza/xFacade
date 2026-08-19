package backend

import (
	"strings"
	"testing"
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
