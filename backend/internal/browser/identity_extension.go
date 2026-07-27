package browser

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
)

// IdentityInfo is baked into the managed identity content script at launch.
type IdentityInfo struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Color string `json:"color"`
	Proxy string `json:"proxy"`
}

var identityColorPalette = []string{
	"#0F766E",
	"#B45309",
	"#9F1239",
	"#1D4ED8",
	"#047857",
	"#C2410C",
	"#6D28D9",
	"#0E7490",
	"#A16207",
	"#BE123C",
	"#334155",
	"#115E59",
}

// IdentityColorForProfileID returns a stable palette color for a profile id.
func IdentityColorForProfileID(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return identityColorPalette[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(profileID))
	return identityColorPalette[int(h.Sum32())%len(identityColorPalette)]
}

// FormatIdentityProxyLabel builds a short proxy label for the identity bar.
func FormatIdentityProxyLabel(effectiveProxy string, proxyName string) string {
	if name := strings.TrimSpace(proxyName); name != "" {
		return name
	}
	effectiveProxy = strings.TrimSpace(effectiveProxy)
	if effectiveProxy == "" || strings.EqualFold(effectiveProxy, "direct://") {
		return "直连"
	}
	parsed, err := url.Parse(effectiveProxy)
	if err != nil || parsed.Host == "" {
		return effectiveProxy
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host
}

func buildIdentityBarScript(info IdentityInfo) (string, error) {
	payload, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("encode identity info: %w", err)
	}

	return `(function () {
  if (window.top !== window) return;
  if (document.documentElement && document.documentElement.dataset.facadeIdentityBar === '1') return;
  var info = ` + string(payload) + `;
  var accent = info.color;
  var collapseDelayMs = 2500;

  function ensureBar() {
    var root = document.documentElement;
    if (!root) return;
    root.dataset.facadeIdentityBar = '1';
    if (document.getElementById('facade-identity-bar')) return;

    if (!document.getElementById('facade-identity-bar-style')) {
      var style = document.createElement('style');
      style.id = 'facade-identity-bar-style';
      style.textContent = [
        '@keyframes facadeIdentityIn{',
        '0%{opacity:0}',
        '100%{opacity:1}',
        '}',
        '#facade-identity-bar{',
        'position:fixed;top:10px;left:50%;',
        'transform:translate(-50%,0);',
        'z-index:2147483646;max-width:min(92vw,560px);',
        'display:flex;align-items:center;gap:10px;',
        'padding:7px 12px;border-radius:999px;',
        'box-sizing:border-box;pointer-events:auto;cursor:pointer;user-select:none;',
        'font-family:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,\"Segoe UI\",sans-serif;',
        'color:rgba(255,255,255,.96);',
        'background:rgba(12,14,18,.72);',
        'border:1px solid rgba(255,255,255,.16);',
        'box-shadow:0 10px 30px rgba(0,0,0,.32),inset 0 1px 0 rgba(255,255,255,.14);',
        'backdrop-filter:blur(14px) saturate(1.35);',
        'overflow:hidden;',
        'animation:facadeIdentityIn .38s cubic-bezier(.22,1,.36,1) both;',
        'transition:transform .32s cubic-bezier(.22,1,.36,1),top .32s cubic-bezier(.22,1,.36,1),border-radius .32s ease,box-shadow .32s ease;',
        '}',
        '#facade-identity-bar.is-collapsed{',
        'top:0;',
        'transform:translate(-50%,calc(-100% + 5px));',
        'border-radius:0 0 12px 12px;',
        'box-shadow:0 4px 16px rgba(0,0,0,.22);',
        '}',
        '#facade-identity-bar .facade-id-name{',
        'font-size:12.5px;font-weight:600;letter-spacing:.01em;',
        'white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:180px;',
        '}',
        '#facade-identity-bar .facade-id-code{',
        'flex:0 0 auto;font:600 10.5px/1 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;',
        'letter-spacing:.04em;text-transform:uppercase;',
        'padding:4px 7px;border-radius:999px;',
        'background:color-mix(in srgb, var(--facade-accent) 22%, rgba(255,255,255,.08));',
        'border:1px solid color-mix(in srgb, var(--facade-accent) 40%, rgba(255,255,255,.12));',
        'color:rgba(255,255,255,.92);',
        '}',
        '#facade-identity-bar .facade-id-sep{',
        'width:1px;height:14px;background:rgba(255,255,255,.18);flex:0 0 auto;',
        '}',
        '#facade-identity-bar .facade-id-proxy{',
        'display:flex;align-items:center;gap:5px;min-width:0;',
        'font-size:11px;font-weight:500;color:rgba(255,255,255,.78);',
        '}',
        '#facade-identity-bar .facade-id-proxy span{',
        'white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px;',
        '}',
        '#facade-identity-bar .facade-id-proxy svg{flex:0 0 auto;opacity:.85}'
      ].join('');
      (document.head || root).appendChild(style);
    }

    var bar = document.createElement('div');
    bar.id = 'facade-identity-bar';
    bar.style.setProperty('--facade-accent', accent);
    bar.setAttribute('data-facade-identity', '1');
    bar.setAttribute('role', 'button');
    bar.setAttribute('tabindex', '0');
    bar.title = '点击展开 / 收起实例身份';

    var nameEl = document.createElement('div');
    nameEl.className = 'facade-id-name';
    nameEl.textContent = info.name;
    nameEl.title = info.name;
    bar.appendChild(nameEl);

    if (info.code) {
      var codeEl = document.createElement('div');
      codeEl.className = 'facade-id-code';
      codeEl.textContent = info.code;
      codeEl.title = info.code;
      bar.appendChild(codeEl);
    }

    var sep = document.createElement('div');
    sep.className = 'facade-id-sep';
    bar.appendChild(sep);

    var proxyEl = document.createElement('div');
    proxyEl.className = 'facade-id-proxy';
    proxyEl.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/></svg>';
    var proxyText = document.createElement('span');
    proxyText.textContent = info.proxy;
    proxyText.title = info.proxy;
    proxyEl.appendChild(proxyText);
    bar.appendChild(proxyEl);

    var collapseTimer = null;
    function clearCollapseTimer() {
      if (collapseTimer) {
        clearTimeout(collapseTimer);
        collapseTimer = null;
      }
    }
    function collapse() {
      clearCollapseTimer();
      bar.classList.add('is-collapsed');
      bar.setAttribute('aria-expanded', 'false');
    }
    function expand() {
      bar.classList.remove('is-collapsed');
      bar.setAttribute('aria-expanded', 'true');
    }
    function scheduleCollapse() {
      clearCollapseTimer();
      collapseTimer = setTimeout(collapse, collapseDelayMs);
    }
    function toggle() {
      if (bar.classList.contains('is-collapsed')) {
        expand();
        scheduleCollapse();
      } else {
        collapse();
      }
    }

    bar.addEventListener('click', function (event) {
      event.preventDefault();
      event.stopPropagation();
      toggle();
    });
    bar.addEventListener('keydown', function (event) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        toggle();
      }
    });

    var mount = function () {
      if (document.getElementById('facade-identity-bar')) return;
      (document.body || root).appendChild(bar);
      expand();
      scheduleCollapse();
    };
    if (document.body) mount();
    else document.addEventListener('DOMContentLoaded', mount, { once: true });
  }

  ensureBar();
})();
`, nil
}
