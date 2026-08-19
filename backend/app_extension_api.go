package backend

import (
	"facade/backend/internal/browser"
	"facade/backend/internal/logger"
	"facade/backend/internal/proxy"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type BrowserExtension = browser.Extension
type BrowserExtensionLookupResult = browser.ExtensionLookupResult
type BrowserProfileExtensionSettings = browser.ProfileExtensionSettings

type BrowserExtensionWebStoreRequest struct {
	Query          string `json:"query"`
	UseProxy       bool   `json:"useProxy"`
	ProxyConfig    string `json:"proxyConfig"`
	AllowOverwrite bool   `json:"allowOverwrite"`
}

func (a *App) BrowserExtensionList() ([]BrowserExtension, error) {
	if a.browserMgr == nil || a.browserMgr.ExtensionDAO == nil {
		return []BrowserExtension{}, nil
	}
	return a.browserMgr.ExtensionDAO.List()
}

func (a *App) BrowserExtensionLookup(query string) (BrowserExtensionLookupResult, error) {
	return a.BrowserExtensionLookupWithProxy(BrowserExtensionWebStoreRequest{Query: query})
}

func (a *App) BrowserExtensionLookupWithProxy(input BrowserExtensionWebStoreRequest) (BrowserExtensionLookupResult, error) {
	if a.browserMgr == nil {
		return BrowserExtensionLookupResult{}, fmt.Errorf("浏览器管理器未初始化")
	}
	client, err := a.extensionDownloadHTTPClient(input.UseProxy, input.ProxyConfig)
	if err != nil {
		return BrowserExtensionLookupResult{}, fmt.Errorf("下载代理配置错误: %w", err)
	}
	return a.browserMgr.LookupExtensionWithHTTPClient(input.Query, client)
}

func (a *App) BrowserExtensionInstall(query string) (BrowserExtension, error) {
	return a.BrowserExtensionInstallWithProxy(BrowserExtensionWebStoreRequest{Query: query})
}

func (a *App) BrowserExtensionInstallWithProxy(input BrowserExtensionWebStoreRequest) (BrowserExtension, error) {
	a.maintenanceMu.Lock()
	defer a.maintenanceMu.Unlock()

	if a.ctx == nil {
		return BrowserExtension{}, fmt.Errorf("应用上下文未初始化")
	}
	if a.browserMgr == nil {
		return BrowserExtension{}, fmt.Errorf("浏览器管理器未初始化")
	}
	client, err := a.extensionDownloadHTTPClient(input.UseProxy, input.ProxyConfig)
	if err != nil {
		return BrowserExtension{}, fmt.Errorf("下载代理配置错误: %w", err)
	}
	extension, err := a.browserMgr.InstallExtensionFromWebStoreWithHTTPClient(a.ctx, input.Query, client, input.AllowOverwrite)
	if err != nil {
		return BrowserExtension{}, err
	}
	logExtensionSaved("webstore", extension)
	return extension, nil
}

func (a *App) extensionDownloadHTTPClient(useProxy bool, proxyConfig string) (*http.Client, error) {
	proxyConfig = strings.TrimSpace(proxyConfig)
	if useProxy && proxyConfig == "" {
		return nil, fmt.Errorf("已启用下载代理，但代理配置为空，请重新选择代理节点")
	}
	if proxyConfig == "" || strings.EqualFold(proxyConfig, "direct://") {
		return &http.Client{Timeout: browser.ExtensionDownloadTimeout()}, nil
	}
	proxies := a.getLatestProxies()
	return proxy.BuildProxyHTTPClient(proxyConfig, "", proxies, browser.ExtensionDownloadTimeout())
}

func (a *App) BrowserExtensionInstallLocalFile() (BrowserExtension, error) {
	a.maintenanceMu.Lock()
	defer a.maintenanceMu.Unlock()

	if a.ctx == nil {
		return BrowserExtension{}, fmt.Errorf("应用上下文未初始化")
	}
	if a.browserMgr == nil {
		return BrowserExtension{}, fmt.Errorf("浏览器管理器未初始化")
	}
	path, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择 Chrome 插件包",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Chrome 插件包 (*.crx;*.zip)", Pattern: "*.crx;*.zip"},
		},
	})
	if err != nil {
		return BrowserExtension{}, fmt.Errorf("打开文件选择框失败: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return BrowserExtension{}, fmt.Errorf("已取消选择")
	}
	extension, err := a.browserMgr.InstallExtensionPackageFile(path)
	if err != nil {
		return BrowserExtension{}, err
	}
	logExtensionSaved("local_file", extension)
	return extension, nil
}

func (a *App) BrowserExtensionSetEnabled(extensionID string, enabled bool) (BrowserExtension, error) {
	if a.browserMgr == nil || a.browserMgr.ExtensionDAO == nil {
		return BrowserExtension{}, fmt.Errorf("插件管理器未初始化")
	}
	extensionID = strings.TrimSpace(extensionID)
	if extensionID == "" {
		return BrowserExtension{}, fmt.Errorf("插件 ID 不能为空")
	}
	if err := a.browserMgr.ExtensionDAO.SetEnabled(extensionID, enabled); err != nil {
		return BrowserExtension{}, err
	}
	return a.browserMgr.ExtensionDAO.Get(extensionID)
}

func (a *App) BrowserExtensionDelete(extensionID string) error {
	a.maintenanceMu.Lock()
	defer a.maintenanceMu.Unlock()

	if a.browserMgr == nil || a.browserMgr.ExtensionDAO == nil {
		return fmt.Errorf("插件管理器未初始化")
	}
	extensionID = strings.TrimSpace(extensionID)
	if extensionID == "" {
		return fmt.Errorf("插件 ID 不能为空")
	}
	extension, err := a.browserMgr.ExtensionDAO.Get(extensionID)
	if err != nil {
		return err
	}
	target, err := a.resolveBrowserExtensionInstallDir(extension.InstallDir)
	if err != nil {
		return err
	}
	if target != "" {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("删除插件目录失败: %w", err)
		}
	}
	if err := a.browserMgr.ExtensionDAO.Delete(extensionID); err != nil {
		return err
	}
	logger.New("Extension").Info("插件已删除",
		logger.F("extension_id", extension.ExtensionID),
		logger.F("name", extension.Name),
		logger.F("version", extension.Version),
	)
	return nil
}

func canonicalPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func (a *App) resolveBrowserExtensionInstallDir(installDir string) (string, error) {
	installDir = strings.TrimSpace(installDir)
	if installDir == "" {
		return "", nil
	}
	target, err := canonicalPath(installDir)
	if err != nil {
		return "", fmt.Errorf("解析插件目录失败: %w", err)
	}
	root, err := canonicalPath(a.resolveAppPath(filepath.ToSlash(filepath.Join("data", "extensions"))))
	if err != nil {
		return "", fmt.Errorf("解析插件根目录失败: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("拒绝删除插件根目录外的路径: %s", installDir)
	}
	return target, nil
}

func (a *App) BrowserProfileExtensionGet(profileID string) (BrowserProfileExtensionSettings, error) {
	if a.browserMgr == nil || a.browserMgr.ExtensionDAO == nil {
		return BrowserProfileExtensionSettings{}, fmt.Errorf("插件管理器未初始化")
	}
	return a.browserMgr.ExtensionDAO.GetProfileSettings(profileID)
}

func (a *App) BrowserProfileExtensionSave(profileID string, extensionIDs []string, configured bool) (BrowserProfileExtensionSettings, error) {
	if a.browserMgr == nil || a.browserMgr.ExtensionDAO == nil {
		return BrowserProfileExtensionSettings{}, fmt.Errorf("插件管理器未初始化")
	}
	settings, err := a.browserMgr.ExtensionDAO.SetProfileSettings(profileID, extensionIDs, configured)
	if err != nil {
		return BrowserProfileExtensionSettings{}, err
	}
	logger.New("Extension").Info("实例扩展配置已保存",
		logger.F("profile_id", settings.ProfileID),
		logger.F("extension_count", len(settings.ExtensionIDs)),
	)
	return settings, nil
}

func logExtensionSaved(source string, extension BrowserExtension) {
	logger.New("Extension").Info("插件已安装",
		logger.F("extension_id", extension.ExtensionID),
		logger.F("name", extension.Name),
		logger.F("version", extension.Version),
		logger.F("source", source),
	)
}
