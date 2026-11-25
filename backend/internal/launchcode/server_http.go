package launchcode

import "net/http"

const statusHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>xFacade - One Facade. Endless Faces.</title>
  <style>
    :root { color-scheme: light dark; font-family: system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #070912; color: #e2e8f0; }
    main { max-width: 28rem; padding: 2rem; text-align: center; }
    h1 { font-size: 1.5rem; font-weight: 600; margin: 0 0 0.75rem; }
    p { margin: 0 0 1rem; line-height: 1.5; color: #94a3b8; }
    a { color: #93c5fd; }
  </style>
</head>
<body>
  <main>
    <h1>xFacade</h1>
    <p>桌面应用已启动。可关闭此标签页，继续在 xFacade 窗口中使用。</p>
    <p><a href="/swagger/">Launch API 文档</a> · <a href="/api/health">健康检查</a></p>
  </main>
</body>
</html>
`

func (s *LaunchServer) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/", s.handleProfileByID)
	mux.HandleFunc("/api/runtime/session", s.handleRuntimeSession)
	mux.HandleFunc("/api/runtime/status", s.handleRuntimeStatus)
	mux.HandleFunc("/api/runtime/stop", s.handleRuntimeStop)
	mux.HandleFunc("/api/launch", s.handleLaunchWithBody)
	mux.HandleFunc("/api/launch/logs", s.handleLaunchLogs)
	mux.HandleFunc("/api/launch/", s.handleLaunch)
	s.registerSwaggerRoutes(mux)
	return mux
}

func (s *LaunchServer) buildHandler(includeLocalhost bool) http.Handler {
	var handler http.Handler = s.buildMux()
	handler = s.apiAuthMiddleware(handler)
	if includeLocalhost {
		handler = s.localhostMiddleware(handler)
	}
	return handler
}

// handleRoot GET / — xLauncher 打开的状态页
func (s *LaunchServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(statusHTML))
}

// handleHealth GET /api/health 与 /health
func (s *LaunchServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// NewTestHandler 返回不含 localhost 限制的 handler，仅供测试使用
func NewTestHandler(s *LaunchServer) http.Handler {
	return s.buildHandler(false)
}
