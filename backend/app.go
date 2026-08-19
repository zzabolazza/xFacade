package backend

import (
	"context"
	"facade/backend/internal/browser"
	"facade/backend/internal/config"
	"facade/backend/internal/database"
	"facade/backend/internal/launchcode"
	"facade/backend/internal/logger"
	"strings"
	"sync"
)

type quitMode uint8

const (
	quitModeFull quitMode = iota
	quitModeAppOnly
)

// App 应用结构体
type App struct {
	ctx            context.Context
	config         *config.Config
	db             *database.DB
	interceptor    *logger.MethodInterceptor
	browserMgr     *browser.Manager
	launchCodeSvc  *launchcode.LaunchCodeService
	launchServer   *launchcode.LaunchServer
	inputSync      *inputSyncManager
	speedScheduler *browser.ProxySpeedScheduler
	appRoot        string
	version        string

	forceQuit              bool
	quitMode               quitMode
	maintenanceMu          sync.Mutex
	deferredStartTargetsMu sync.Mutex
	deferredStartTargets   map[string][]string
	stopServicesOnce       sync.Once
	finalizeOnce           sync.Once
}

// NewApp 创建新的应用实例
func NewApp(appRoot string, appVersion ...string) *App {
	version := ""
	if len(appVersion) > 0 {
		version = strings.TrimSpace(appVersion[0])
	}
	app := &App{
		appRoot:              strings.TrimSpace(appRoot),
		version:              version,
		deferredStartTargets: make(map[string][]string),
	}
	app.inputSync = newInputSyncManager(app)
	return app
}

func (a *App) appName() string {
	if a.config != nil {
		if name := strings.TrimSpace(a.config.App.Name); name != "" {
			return name
		}
	}
	return "Facade"
}

func (a *App) appVersion() string {
	version := strings.TrimSpace(a.version)
	if version == "" {
		return "unknown"
	}
	return version
}
