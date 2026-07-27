package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const managedExtensionDirName = "Facade Managed Extension"

// ManagedExtensionOptions configures the unified Facade managed browser extension.
type ManagedExtensionOptions struct {
	Identity      IdentityInfo
	PrivacyVoices []SpeechVoice // empty => identity bar only
}

// EnsureManagedExtension writes a single MV3 extension with identity bar always,
// and optional speech-voice privacy shim when PrivacyVoices is non-empty.
func EnsureManagedExtension(userDataDir string, opts ManagedExtensionOptions) (string, error) {
	userDataDir = strings.TrimSpace(userDataDir)
	if userDataDir == "" {
		return "", fmt.Errorf("user data directory is empty")
	}

	info := opts.Identity
	info.Name = strings.TrimSpace(info.Name)
	info.Code = strings.TrimSpace(info.Code)
	info.Color = strings.TrimSpace(info.Color)
	info.Proxy = strings.TrimSpace(info.Proxy)
	if info.Color == "" {
		info.Color = identityColorPalette[0]
	}
	if info.Name == "" {
		info.Name = "Facade"
	}
	if info.Proxy == "" {
		info.Proxy = "直连"
	}

	extensionDir := filepath.Join(userDataDir, managedExtensionDirName)
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		return "", fmt.Errorf("create managed extension directory: %w", err)
	}

	enableVoices := len(opts.PrivacyVoices) > 0
	manifest, err := buildManagedExtensionManifest(enableVoices)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		return "", fmt.Errorf("write managed extension manifest: %w", err)
	}

	barScript, err := buildIdentityBarScript(info)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "bar.js"), []byte(barScript), 0o644); err != nil {
		return "", fmt.Errorf("write managed identity bar script: %w", err)
	}

	voicesPath := filepath.Join(extensionDir, "voices.js")
	if enableVoices {
		voicesScript, err := buildVoicesShimScript(opts.PrivacyVoices)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(voicesPath, []byte(voicesScript), 0o644); err != nil {
			return "", fmt.Errorf("write managed privacy voices script: %w", err)
		}
	} else {
		_ = os.Remove(voicesPath)
	}

	return extensionDir, nil
}

func buildManagedExtensionManifest(enableVoices bool) (string, error) {
	type contentScript struct {
		Matches    []string `json:"matches"`
		JS         []string `json:"js"`
		RunAt      string   `json:"run_at"`
		AllFrames  bool     `json:"all_frames"`
		World      string   `json:"world,omitempty"`
	}
	type manifest struct {
		ManifestVersion int             `json:"manifest_version"`
		Name            string          `json:"name"`
		Version         string          `json:"version"`
		Description     string          `json:"description"`
		ContentScripts  []contentScript `json:"content_scripts"`
	}

	scripts := []contentScript{
		{
			Matches:   []string{"<all_urls>"},
			JS:        []string{"bar.js"},
			RunAt:     "document_start",
			AllFrames: false,
		},
	}
	if enableVoices {
		scripts = append(scripts, contentScript{
			Matches:   []string{"<all_urls>"},
			JS:        []string{"voices.js"},
			RunAt:     "document_start",
			AllFrames: true,
			World:     "MAIN",
		})
	}

	payload, err := json.MarshalIndent(manifest{
		ManifestVersion: 3,
		Name:            "Facade Managed Extension",
		Version:         "1.2.0",
		Description:     "Facade managed identity badge and optional privacy shims.",
		ContentScripts:  scripts,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode managed extension manifest: %w", err)
	}
	return string(payload) + "\n", nil
}
