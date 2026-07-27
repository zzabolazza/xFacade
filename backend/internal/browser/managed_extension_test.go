package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityColorForProfileIDStable(t *testing.T) {
	a := IdentityColorForProfileID("profile-alpha")
	b := IdentityColorForProfileID("profile-alpha")
	if a != b {
		t.Fatalf("color not stable: %q vs %q", a, b)
	}
	if a == "" || !strings.HasPrefix(a, "#") {
		t.Fatalf("unexpected color %q", a)
	}
	empty := IdentityColorForProfileID("")
	if empty != identityColorPalette[0] {
		t.Fatalf("empty id color = %q, want %q", empty, identityColorPalette[0])
	}
}

func TestFormatIdentityProxyLabel(t *testing.T) {
	if got := FormatIdentityProxyLabel("direct://", ""); got != "直连" {
		t.Fatalf("direct = %q", got)
	}
	if got := FormatIdentityProxyLabel("socks5://user:pass@1.2.3.4:1080", "US-NY"); got != "US-NY" {
		t.Fatalf("named proxy = %q", got)
	}
	if got := FormatIdentityProxyLabel("socks5://user:pass@1.2.3.4:1080", ""); got != "socks5://1.2.3.4:1080" {
		t.Fatalf("sanitized proxy = %q", got)
	}
}

func TestEnsureManagedExtensionIdentityOnly(t *testing.T) {
	userDataDir := t.TempDir()
	dir, err := EnsureManagedExtension(userDataDir, ManagedExtensionOptions{
		Identity: IdentityInfo{
			Name:  "Shop-US-01",
			Code:  "abc12345",
			Color: "#0D9488",
			Proxy: "US-East",
		},
	})
	if err != nil {
		t.Fatalf("EnsureManagedExtension() error = %v", err)
	}
	if filepath.Base(dir) != managedExtensionDirName {
		t.Fatalf("dir base = %q, want %q", filepath.Base(dir), managedExtensionDirName)
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	if !strings.Contains(text, `"bar.js"`) {
		t.Fatalf("manifest missing bar.js: %s", text)
	}
	if strings.Contains(text, "voices.js") || strings.Contains(text, `"MAIN"`) {
		t.Fatalf("identity-only manifest should not include voices/MAIN: %s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "voices.js")); !os.IsNotExist(err) {
		t.Fatalf("voices.js should be absent when privacy disabled, err=%v", err)
	}

	script, err := os.ReadFile(filepath.Join(dir, "bar.js"))
	if err != nil {
		t.Fatal(err)
	}
	bar := string(script)
	for _, want := range []string{
		`"name":"Shop-US-01"`,
		`"proxy":"US-East"`,
		"facade-identity-bar",
		"background:rgba(12,14,18,.72)",
		`<circle cx="12" cy="12" r="10"/>`,
		"is-collapsed",
		"scheduleCollapse",
		"pointer-events:auto",
		"calc(-100% + 5px)",
	} {
		if !strings.Contains(bar, want) {
			t.Fatalf("bar.js missing %q", want)
		}
	}
	if strings.Contains(bar, "linear-gradient") {
		t.Fatalf("bar background must not use gradient")
	}
	if strings.Contains(bar, "padding-top") {
		t.Fatalf("floating bar should not reserve page space")
	}
	if strings.Contains(bar, "pointer-events:none") {
		t.Fatalf("bar must allow clicks; found pointer-events:none")
	}
	if strings.Contains(bar, "is-collapsed .facade-id-name") {
		t.Fatalf("collapsed state should slide up, not hide content into a dot")
	}
}

func TestEnsureManagedExtensionWithVoices(t *testing.T) {
	userDataDir := t.TempDir()
	dir, err := EnsureManagedExtension(userDataDir, ManagedExtensionOptions{
		Identity:      IdentityInfo{Name: "Facade"},
		PrivacyVoices: SpeechVoicesForLanguage("en-SG"),
	})
	if err != nil {
		t.Fatalf("EnsureManagedExtension() error = %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	if !strings.Contains(text, `"bar.js"`) || !strings.Contains(text, `"voices.js"`) {
		t.Fatalf("manifest should include both scripts: %s", text)
	}
	if !strings.Contains(text, `"world": "MAIN"`) && !strings.Contains(text, `"world":"MAIN"`) {
		t.Fatalf("voices content script should use MAIN world: %s", text)
	}

	voices, err := os.ReadFile(filepath.Join(dir, "voices.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(voices), `"lang":"en-SG"`) {
		t.Fatalf("voices script missing en-SG: %s", voices)
	}
}

func TestEnsureManagedExtensionRemovesStaleVoices(t *testing.T) {
	userDataDir := t.TempDir()
	dir, err := EnsureManagedExtension(userDataDir, ManagedExtensionOptions{
		Identity:      IdentityInfo{Name: "A"},
		PrivacyVoices: SpeechVoicesForLanguage("en-US"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "voices.js")); err != nil {
		t.Fatalf("expected voices.js after enabling privacy: %v", err)
	}

	dir, err = EnsureManagedExtension(userDataDir, ManagedExtensionOptions{
		Identity: IdentityInfo{Name: "A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "voices.js")); !os.IsNotExist(err) {
		t.Fatalf("voices.js should be removed when privacy disabled, err=%v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "voices.js") {
		t.Fatalf("manifest should drop voices.js: %s", manifest)
	}
}
