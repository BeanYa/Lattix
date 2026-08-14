package sub

import (
	_ "embed"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"lattix/backend/internal/store"
)

// qrJS 是内嵌的极简二维码库 qrcode-generator（Kazuhiko Arase，MIT 许可，
// https://github.com/kazuhikoarase/qrcode-generator ，v1.4.4 原样内嵌，许可头见文件开头）。
// 落地页自包含渲染二维码，不依赖 frontend 构建产物与任何 CDN/外网资源（§9）。
//
//go:embed qrcode.js
var qrJS string

// serveLanding 渲染订阅落地页（浏览器访问 GET /sub/{token}，§9）：token 即鉴权（无效 404，上游已处理）。
// 内容：已用流量、有效期、节点数量、订阅地址与链接集合地址（带复制按钮）、
// 订阅地址二维码、mihomo 系一键导入链接；已到期用户显示"已到期"，被停用用户显示"已停用"（§16）。
func (s *Server) serveLanding(w http.ResponseWriter, r *http.Request, user *store.User, nodes []store.Node) {
	base := s.base(r)
	subURL := fmt.Sprintf("%s/sub/%s", base, user.SubToken)
	linksURL := subURL + "/links"

	t, _ := s.st.UserTraffic(r.Context(), user.UUID)

	expiryText := "长期"
	statusBadge := `<span class="badge ok">正常</span>`
	if user.Disabled || user.Expired {
		statusBadge = ""
		if user.Disabled {
			statusBadge += `<span class="badge expired">已停用</span>`
		}
		if user.Expired {
			statusBadge += `<span class="badge expired">已到期</span>`
		}
	}
	if user.ExpiresAt != nil {
		expiryText = user.ExpiresAt.Local().Format("2006-01-02 15:04")
		if user.Expired {
			expiryText += "（已到期）"
		}
	}

	importName := url.QueryEscape("Lattix-" + user.Name)
	encSub := url.QueryEscape(subURL)
	clashImport := fmt.Sprintf("clash://install-config?url=%s&name=%s", encSub, importName)
	mihomoImport := fmt.Sprintf("mihomo://install-config?url=%s&name=%s", encSub, importName)

	notice := ""
	if user.Disabled {
		notice += `<p class="notice">订阅已被管理员停用，节点不可用。如需恢复请联系管理员。</p>`
	}
	if user.Expired {
		notice += `<p class="notice">订阅已到期，节点已停用。如需恢复请联系管理员延长有效期。</p>`
	}

	var b strings.Builder
	b.WriteString(landingHead)
	b.WriteString(`<div class="card"><div class="head"><div class="brand">` + lattixMarkSVG + `<h1>Lattix 订阅</h1></div>` + statusBadge + `</div>`)
	b.WriteString(`<p class="user">` + html.EscapeString(user.Name) + `</p>`)
	b.WriteString(notice)
	b.WriteString(`<div class="stats">`)
	b.WriteString(`<div class="stat"><span class="k">已用流量</span><span class="v">↑ ` + humanizeBytes(t.Up) + `　↓ ` + humanizeBytes(t.Down) + `</span></div>`)
	b.WriteString(`<div class="stat"><span class="k">有效期</span><span class="v">` + expiryText + `</span></div>`)
	fmt.Fprintf(&b, `<div class="stat"><span class="k">节点数量</span><span class="v">%d</span></div>`, len(nodes))
	b.WriteString(`</div>`)

	b.WriteString(`<div class="row"><span class="k">订阅地址（mihomo YAML）</span><div class="url"><code>` + html.EscapeString(subURL) + `</code><button onclick="copyText(this)">复制</button></div></div>`)
	b.WriteString(`<div class="row"><span class="k">链接集合地址</span><div class="url"><code>` + html.EscapeString(linksURL) + `</code><button onclick="copyText(this)">复制</button></div></div>`)

	b.WriteString(`<div class="qrwrap"><div id="qr"></div><p class="hint">扫码导入订阅</p></div>`)

	b.WriteString(`<div class="import"><a class="btn" href="` + clashImport + `">导入 Clash 系客户端</a>`)
	b.WriteString(`<a class="btn" href="` + mihomoImport + `">导入 mihomo party</a></div>`)

	b.WriteString(`</div><p class="foot">Lattix · 订阅每天自动更新（profile-update-interval: 24）</p>`)

	// 二维码：内容为 YAML 订阅地址，客户端本地渲染（不经过任何第三方服务）。
	b.WriteString(`<script>` + qrJS + `</script><script>
try {
  var qr = qrcode(0, 'M');
  qr.addData(` + jsString(subURL) + `);
  qr.make();
  document.getElementById('qr').innerHTML = qr.createSvgTag({cellSize: 4, margin: 8, scalable: true});
} catch (e) {
  document.getElementById('qr').textContent = '二维码生成失败，请手动复制订阅地址';
}
function copyText(btn) {
  var text = btn.parentElement.querySelector('code').textContent;
  var done = function () { btn.textContent = '已复制'; setTimeout(function () { btn.textContent = '复制'; }, 1500); };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done, function () { legacyCopy(text); done(); });
  } else { legacyCopy(text); done(); }
}
function legacyCopy(text) {
  var ta = document.createElement('textarea');
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  try { document.execCommand('copy'); } catch (e) {}
  document.body.removeChild(ta);
}
</script></body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(b.String()))
}

const lattixMarkSVG = `<svg class="logo" viewBox="0 0 64 64" aria-hidden="true">
<g fill="none" stroke="#6437f2" stroke-linecap="round" stroke-linejoin="round" stroke-width="7">
<path d="M11 11v42h42"/><path d="m11 53 42-42"/>
<path d="M11 11h9c3 0 5 2 5 5v3c0 3 1 5 4 8l3 5 5 3c3 3 5 4 8 4h3c3 0 5 2 5 5v9"/>
</g>
<g fill="none" stroke="currentColor" stroke-width="5">
<circle cx="11" cy="11" r="7"/><circle cx="53" cy="11" r="7"/><circle cx="11" cy="53" r="7"/>
</g>
<circle cx="53" cy="53" r="7" fill="none" stroke="#06d5e8" stroke-width="5"/>
</svg>`

// jsString 输出安全的 JS 字符串字面量（订阅地址只含 URL 安全字符，防御性转义引号与反斜杠）。
func jsString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `<`, `\<`, `>`, `\>`, `&`, `\u0026`)
	return `"` + r.Replace(s) + `"`
}

// humanizeBytes 格式化字节数（与前端 humanizeBytes 同款口径）。
func humanizeBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// landingHead 是落地页的 HTML 头部与内联样式（自包含，无任何外网资源）。
const landingHead = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Lattix 订阅</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif;
  background: #0f1420; color: #e6e9f0; min-height: 100vh; display: flex; flex-direction: column;
  align-items: center; justify-content: center; padding: 24px; }
.card { background: #1a2132; border: 1px solid #2a3350; border-radius: 14px;
  padding: 24px; width: 100%; max-width: 560px; }
.head { display: flex; align-items: center; justify-content: space-between; }
.brand { display: flex; align-items: center; gap: 10px; }
.logo { width: 34px; height: 34px; flex: none; color: #e6e9f0; }
h1 { font-size: 20px; }
.user { color: #9aa4c0; margin-top: 4px; font-size: 14px; }
.badge { font-size: 12px; padding: 3px 10px; border-radius: 999px; }
.badge.ok { background: #123f2b; color: #4ade80; }
.badge.expired { background: #4a1d1d; color: #f87171; }
.notice { margin-top: 12px; background: #4a1d1d; color: #fca5a5; border-radius: 8px;
  padding: 10px 12px; font-size: 13px; }
.stats { margin-top: 16px; display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px; }
.stat { background: #131a2b; border-radius: 10px; padding: 12px; }
.stat .k { display: block; font-size: 12px; color: #9aa4c0; margin-bottom: 6px; }
.stat .v { font-size: 14px; word-break: break-all; }
.row { margin-top: 16px; }
.row .k { font-size: 12px; color: #9aa4c0; display: block; margin-bottom: 6px; }
.url { display: flex; gap: 8px; align-items: center; background: #131a2b; border-radius: 10px;
  padding: 8px 8px 8px 12px; }
.url code { flex: 1; font-size: 12px; word-break: break-all; color: #cdd6ee; }
.url button, .btn { background: #3b82f6; color: #fff; border: 0; border-radius: 8px;
  padding: 8px 14px; font-size: 13px; cursor: pointer; white-space: nowrap; text-decoration: none; }
.url button:hover, .btn:hover { background: #2563eb; }
.qrwrap { margin-top: 20px; display: flex; flex-direction: column; align-items: center; }
#qr { background: #fff; border-radius: 12px; padding: 8px; line-height: 0; min-height: 40px; min-width: 40px; }
#qr svg { width: 200px; height: 200px; }
.hint { font-size: 12px; color: #9aa4c0; margin-top: 8px; }
.import { margin-top: 20px; display: flex; gap: 10px; flex-wrap: wrap; }
.import .btn { flex: 1; text-align: center; }
.foot { margin-top: 16px; font-size: 12px; color: #6b7595; }
</style></head><body>
`
