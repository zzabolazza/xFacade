package browser

import (
	"facade/backend/internal/database"
	"os"
	"path/filepath"
	"testing"
)

func TestEnabledExtensionDirsForProfileUsesSavedIDsOnly(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	disabledDir := extensionDirWithManifest(t, "disabled-extension")
	enabledDir := extensionDirWithManifest(t, "enabled-extension")
	dao := NewSQLiteExtensionDAO(db.GetConn())
	if err := dao.Upsert(Extension{ExtensionID: "disabled-extension", Name: "Disabled", InstallDir: disabledDir, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := dao.Upsert(Extension{ExtensionID: "enabled-extension", Name: "Enabled", InstallDir: enabledDir, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{ExtensionDAO: dao}
	if got := manager.EnabledExtensionDirsForProfile("missing-profile"); len(got) != 0 {
		t.Fatalf("missing profile settings loaded dirs = %v, want none", got)
	}

	if _, err := dao.SetProfileSettings("profile-a", []string{"disabled-extension"}, true); err != nil {
		t.Fatal(err)
	}
	got := manager.EnabledExtensionDirsForProfile("profile-a")
	if len(got) != 1 || got[0] != disabledDir {
		t.Fatalf("saved profile dirs = %v, want [%s]", got, disabledDir)
	}
}

func extensionDirWithManifest(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"manifest_version":3,"name":"test","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
