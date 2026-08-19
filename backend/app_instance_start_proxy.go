package backend

import (
	"facade/backend/internal/logger"
	"facade/backend/internal/proxy"
	"fmt"
	"strings"
)

const temporaryDirectProxyID = "__direct__"

func (a *App) resolveBrowserStartProxy(input browserStartInput, profile *BrowserProfile) (string, error) {
	log := logger.New("Browser")
	proxies := a.getLatestProxies()
	profileID := input.ProfileID

	if input.ForceDirectProxy {
		log.Warn("按请求直连启动实例",
			logger.F("profile_id", profileID),
			logger.F("proxy_id", profile.ProxyId),
		)
		return "direct://", nil
	}

	resolvedProxyID := strings.TrimSpace(profile.ProxyId)
	resolvedProxyConfig := strings.TrimSpace(profile.ProxyConfig)
	usingTemporaryProxy := input.hasTemporaryProxy()
	if usingTemporaryProxy {
		var err error
		resolvedProxyID, resolvedProxyConfig, err = resolveTemporaryBrowserStartProxy(input.TemporaryProxyID, input.TemporaryProxyConfig, proxies)
		if err != nil {
			startErr := fmt.Errorf("实例启动失败：%s", err.Error())
			profile.LastError = startErr.Error()
			log.Error("一次性代理配置无效",
				logger.F("profile_id", profileID),
				logger.F("temporary_proxy_id", input.TemporaryProxyID),
				logger.F("error", err.Error()),
				logger.F("reason", startErr.Error()),
			)
			return "", startErr
		}
	} else if resolvedProxyID != "" {
		for _, item := range proxies {
			if strings.EqualFold(item.ProxyId, resolvedProxyID) {
				resolvedProxyID = strings.TrimSpace(item.ProxyId)
				resolvedProxyConfig = strings.TrimSpace(item.ProxyConfig)
				break
			}
		}
	}

	if supported, errorMsg := proxy.ValidateProxyConfig(resolvedProxyConfig, proxies, resolvedProxyID); !supported {
		startErr := fmt.Errorf("实例启动失败：%s", errorMsg)
		profile.LastError = startErr.Error()
		log.Error("代理配置无效",
			logger.F("profile_id", profileID),
			logger.F("proxy_id", resolvedProxyID),
			logger.F("error", errorMsg),
			logger.F("reason", startErr.Error()),
		)
		return "", startErr
	}

	if resolvedProxyConfig == "" || strings.EqualFold(resolvedProxyConfig, "direct://") {
		return "direct://", nil
	}

	normalized, err := proxy.ParseNativeProxyURL(resolvedProxyConfig)
	if err != nil {
		startErr := fmt.Errorf("实例启动失败：%s", err.Error())
		profile.LastError = startErr.Error()
		return "", startErr
	}
	return normalized, nil
}

func resolveTemporaryBrowserStartProxy(proxyID string, proxyConfig string, proxies []BrowserProxy) (string, string, error) {
	proxyID = strings.TrimSpace(proxyID)
	proxyConfig = strings.TrimSpace(proxyConfig)
	if proxyID == "" {
		return "", proxyConfig, nil
	}

	for _, item := range proxies {
		if strings.EqualFold(item.ProxyId, proxyID) {
			return strings.TrimSpace(item.ProxyId), strings.TrimSpace(item.ProxyConfig), nil
		}
	}
	if strings.EqualFold(proxyID, temporaryDirectProxyID) {
		return temporaryDirectProxyID, "direct://", nil
	}
	if proxyConfig != "" {
		return "", proxyConfig, nil
	}
	return "", "", fmt.Errorf("代理ID不存在（proxy id not found: %s），且未提供 proxyConfig", proxyID)
}

func (a *App) resolveIdentityProxyName(input browserStartInput, profile *BrowserProfile, effectiveProxy string) string {
	if strings.TrimSpace(effectiveProxy) == "" || strings.EqualFold(strings.TrimSpace(effectiveProxy), "direct://") {
		return "直连"
	}

	proxyID := strings.TrimSpace(profile.ProxyId)
	if input.hasTemporaryProxy() {
		if id := strings.TrimSpace(input.TemporaryProxyID); id != "" && !strings.EqualFold(id, temporaryDirectProxyID) {
			proxyID = id
		}
	}
	if proxyID == "" {
		return ""
	}
	for _, item := range a.getLatestProxies() {
		if strings.EqualFold(strings.TrimSpace(item.ProxyId), proxyID) {
			return strings.TrimSpace(item.ProxyName)
		}
	}
	return ""
}
