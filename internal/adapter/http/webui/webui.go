// Package webui 以 go:embed 提供 M6 Web UI 的静态资源与 SPA fallback
// （SPEC-WEB-001 REQ-02 / DESIGN-WEB-001 DEC-02）。
//
// # 为什么 dist 在这个包里而不直接嵌 web/dist
//
// go:embed 只能引用**本包目录及其子目录**，无法跨目录写 `//go:embed ../../web/dist`。
// 所以构建产物由 `make web-build` 从 web/dist 同步到本包的 dist/ 下，本目录只提交
// .gitkeep——否则干净克隆上 embed 找不到目录，`go build` 直接失败（AC-01 的硬约束）。
package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// dist 与占位文件必须存在，否则 go:embed all:dist 编译失败。
const indexFile = "index.html"

// FS 返回已构建产物的只读视图（dist 子树）。
func FS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// IsBackendPath 报告某路径属于后端（API 与探针），静态 handler 不得接管。
//
// 这些路径必须继续返回 problem+json / 探针响应：若被 SPA fallback 吞成 index.html，
// 客户端的「接口 404」会变成「HTML 解析失败」，错误归因从此查不出来（AC-02）。
func IsBackendPath(p string) bool {
	switch p {
	case "/healthz", "/readyz":
		return true
	}
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// Handler 返回 SPA 静态资源 handler。
//
// 规则：
//   - 命中真实文件 → 原样返回（/assets/** 走一年 immutable 缓存）；
//   - 未命中且**无扩展名** → 视为前端路由，回落 index.html（/stories/123）；
//   - 未命中但**有扩展名** → 404。绝不回落 index.html：那会让缺失的 .js/.css
//     拿到 200 的 HTML，浏览器报 MIME 错误而不是"资源不存在"。
func Handler() http.Handler {
	sub, err := FS()
	if err != nil {
		return brokenFSHandler(err)
	}
	return NewHandler(sub)
}

// NewHandler 在给定文件系统上构建 SPA handler（供测试注入 fstest.MapFS）。
func NewHandler(sub fs.FS) http.Handler {
	if _, err := fs.Stat(sub, indexFile); err != nil {
		// 干净克隆（只有 .gitkeep）属于这种情形：能编译、不能提供页面。
		// 明确报"未构建"比返回空白 200 更容易排查。
		return notBuiltHandler()
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsBackendPath(r.URL.Path) {
			// 理论上 router 已分流，这里只做防御：绝不能把 API 请求变成 HTML。
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")

		if name != "" {
			if f, statErr := sub.Open(name); statErr == nil {
				_ = f.Close()
				if strings.HasPrefix(clean, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if name == "" || path.Ext(clean) == "" {
			serveIndex(w, r, sub)
			return
		}
		http.NotFound(w, r)
	})
}

// serveIndex 输出入口 HTML。不发长缓存：发版后旧 index 引用旧 assets 会白屏。
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	raw, err := fs.ReadFile(sub, indexFile)
	if err != nil {
		http.Error(w, "webui: 读取 index.html 失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(raw)
}

func notBuiltHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "webui: 前端未构建，请先执行 `make web-build`", http.StatusServiceUnavailable)
	})
}

func brokenFSHandler(err error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, fmt.Sprintf("webui: 静态资源不可用: %v", err), http.StatusInternalServerError)
	})
}
