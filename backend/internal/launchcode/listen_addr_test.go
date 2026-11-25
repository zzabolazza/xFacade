package launchcode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseListenAddr(t *testing.T) {
	host, port, err := ParseListenAddr("127.0.0.1:9001")
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 9001 {
		t.Fatalf("got %s:%d", host, port)
	}

	host, port, err = ParseListenAddr(":19876")
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 19876 {
		t.Fatalf("empty host should default to loopback, got %s:%d", host, port)
	}

	if _, _, err := ParseListenAddr(""); err == nil {
		t.Fatal("expected error for empty addr")
	}
	if _, _, err := ParseListenAddr("no-port"); err == nil {
		t.Fatal("expected error for missing port")
	}
	if _, _, err := ParseListenAddr(":99999"); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestResolveLaunchListenPrecedence(t *testing.T) {
	env := map[string]string{EnvListenAddr: "127.0.0.1:9002"}
	getenv := func(k string) string { return env[k] }

	host, port, err := ResolveLaunchListen([]string{"xFacade", "-addr", "127.0.0.1:9003"}, getenv, 19876)
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 9003 {
		t.Fatalf("-addr should win, got %s:%d", host, port)
	}

	host, port, err = ResolveLaunchListen([]string{"xFacade"}, getenv, 19876)
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 9002 {
		t.Fatalf("env should win over config, got %s:%d", host, port)
	}

	host, port, err = ResolveLaunchListen([]string{"xFacade"}, func(string) string { return "" }, 19876)
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 19876 {
		t.Fatalf("config port fallback, got %s:%d", host, port)
	}
}

func TestStatusRootPage(t *testing.T) {
	srv := NewLaunchServer(nil, nil, nil, 0)
	handler := NewTestHandler(srv)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "xFacade - One Facade. Endless Faces.") {
		t.Fatalf("missing title in %q", body)
	}
	if !strings.Contains(body, "桌面应用已启动") {
		t.Fatalf("missing status copy in %q", body)
	}

	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", notFound.Code)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"ok":true`) {
		t.Fatalf("GET /health = %d %q", health.Code, health.Body.String())
	}
}
