package httputil

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type ctxKey int

const requestIDKey ctxKey = 1

// RequestID 从上下文取请求 ID（中间件注入）。
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Middlewares 注册全局中间件链：请求 ID → 恢复 → 访问日志 → 超时。
func Middlewares(logger *slog.Logger) []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		injectRequestID,
		middleware.Recoverer,
		accessLog(logger),
	}
}

// injectRequestID 为每个请求生成/透传 X-Request-Id，并写入上下文。
func injectRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = middleware.GetReqID(r.Context())
		}
		if id == "" {
			id = "req-" + time.Now().UTC().Format("20060102T150405.000000000")
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// accessLog 记录访问日志（结构化，便于审计）。
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if logger == nil {
					return
				}
				logger.Info("http",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", ww.Status()),
					slog.Duration("dur", time.Since(start)),
					slog.String("request_id", RequestID(r.Context())),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
