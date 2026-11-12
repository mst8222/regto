// Package log 提供进程统一的结构化日志入口（slog，local 用文本、prod/CI 用 JSON）。
package log

import (
	"log/slog"
	"os"
	"strings"
)

// New 按环境构建 logger：prod/ci 输出 JSON，local 输出可读文本。
func New(env, level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if env == "prod" || env == "ci" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h).With(slog.String("env", env))
}
