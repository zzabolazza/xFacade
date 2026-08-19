package backend

import (
	"reflect"
	"testing"

	"facade/backend/internal/browser"
	"facade/backend/internal/config"
	"facade/backend/internal/database"
)

func newTagTestApp(t *testing.T, db *database.DB) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	mgr := browser.NewManager(cfg, t.TempDir())
	mgr.ProfileDAO = browser.NewSQLiteProfileDAO(db.GetConn())
	mgr.TagDAO = browser.NewSQLiteTagDAO(db.GetConn())
	return &App{config: cfg, browserMgr: mgr}
}

func TestBrowserCreateTagPersistsUnassignedTag(t *testing.T) {
	db := openMigratedTestDB(t, "tags.db")
	app := newTagTestApp(t, db)

	if err := app.BrowserCreateTag("  VIP客户 "); err != nil {
		t.Fatal(err)
	}

	if got := app.BrowserGetAllTags(); !reflect.DeepEqual(got, []string{"VIP客户"}) {
		t.Fatalf("expected saved tag, got %v", got)
	}

	reloaded := newTagTestApp(t, db)
	if got := reloaded.BrowserGetAllTags(); !reflect.DeepEqual(got, []string{"VIP客户"}) {
		t.Fatalf("expected saved tag after reload, got %v", got)
	}
}

func TestBrowserRenameTagPersistsUnassignedAndAssignedTags(t *testing.T) {
	db := openMigratedTestDB(t, "rename-tags.db")
	app := newTagTestApp(t, db)

	if err := app.BrowserCreateTag("待跟进"); err != nil {
		t.Fatal(err)
	}
	if err := app.browserMgr.ProfileDAO.Upsert(&browser.Profile{
		ProfileId:       "profile-1",
		ProfileName:     "测试实例",
		UserDataDir:     "profile-1",
		FingerprintArgs: []string{},
		LaunchArgs:      []string{},
		Tags:            []string{"待跟进"},
		Keywords:        []string{},
		CreatedAt:       "created",
		UpdatedAt:       "updated",
	}); err != nil {
		t.Fatal(err)
	}
	app.browserMgr.InitData()

	if err := app.BrowserRenameTag("待跟进", "已跟进"); err != nil {
		t.Fatal(err)
	}

	if got := app.BrowserGetAllTags(); !reflect.DeepEqual(got, []string{"已跟进"}) {
		t.Fatalf("expected renamed tag, got %v", got)
	}
	profile, err := app.browserMgr.ProfileDAO.GetById("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profile.Tags, []string{"已跟进"}) {
		t.Fatalf("expected profile tag to be renamed, got %v", profile.Tags)
	}

	reloaded := newTagTestApp(t, db)
	if got := reloaded.BrowserGetAllTags(); !reflect.DeepEqual(got, []string{"已跟进"}) {
		t.Fatalf("expected renamed tag after reload, got %v", got)
	}
}
