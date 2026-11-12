package httputil

import (
	"net/url"
	"strings"
)

// loopbackHosts 是本地回环的主机名集合。
var loopbackHosts = map[string]struct{}{
	"localhost": {}, "127.0.0.1": {}, "::1": {}, "[::1]": {},
}

// IsLoopbackOrigin 报告 origin 是否指向本机（任意端口）。
//
// 本地开发必然前后端异端口（vite 5173 / preview 4173 → 后端 8080），
// 且经代理后后端看到的 Host 与浏览器发的 Origin **永远不同**——严格同源判定
// 会把整个开发流程打断在续期这一步，表现为"一刷新就掉登录态"，极难排查。
func IsLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	_, ok := loopbackHosts[u.Hostname()]
	return ok
}

// NonProdEnv 报告是否为非生产环境。回环放宽只在这里生效。
func NonProdEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "test", "ci", "":
		return true
	}
	return false
}

// AllowOrigin 判定浏览器来源是否放行（SPEC-WEB-001 REQ-04 的 Origin 白名单）。
//
// 判定顺序：无 Origin（非浏览器/同源 GET）→ 与 Host 同源 → 回环放宽 → 显式白名单。
func AllowOrigin(origin, host string, extra []string, env string) bool {
	origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
	if origin == "" {
		return true
	}
	if origin == "http://"+host || origin == "https://"+host {
		return true
	}
	// 非生产 + 回环地址 → 放行。
	//
	// 这不是放宽 CSRF 防护：cookie 本身已是 SameSite=Lax（跨站 POST 不带 cookie），
	// Origin 检查属于纵深防御，它的价值在**生产跨站**场景；为了它而让本地开发
	// 连登录都续不了，是拿可用性换了几乎为零的收益。生产环境此分支不生效。
	if NonProdEnv(env) && IsLoopbackOrigin(origin) {
		return true
	}
	for _, a := range extra {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(a), "/"), origin) {
			return true
		}
	}
	return false
}
