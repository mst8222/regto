import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发期同源：Vite dev server 反代 /api 到后端，后端**不加 CORS**
// （SPEC-WEB-001 REQ-03 / DESIGN-WEB-001 DEC-03）。
// 加 CORS 要给 Bearer JWT + X-Regto-API-Key 体系新增攻击面，且与
// 「认证默认全保护、PublicPaths 白名单显式放行」的既有纪律冲突。
const BACKEND = process.env.REGTO_BACKEND_URL ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: BACKEND, changeOrigin: true },
      '/healthz': { target: BACKEND, changeOrigin: true },
      '/readyz': { target: BACKEND, changeOrigin: true },
    },
  },
  // preview 也要反代：e2e 跑的是 vite preview（生产产物形态），
  // 没有代理的话 /api 会打到静态服务器拿回 404 的 HTML，
  // 表现为"页面白屏且控制台一片 MIME 报错"，很误导。
  preview: {
    port: 4173,
    strictPort: true,
    proxy: {
      '/api': { target: BACKEND, changeOrigin: true },
      '/healthz': { target: BACKEND, changeOrigin: true },
      '/readyz': { target: BACKEND, changeOrigin: true },
    },
  },
  build: {
    // 产物落 web/dist，再由 make web-build 同步进 internal/adapter/http/webui/dist
    // （go:embed 无法跨目录引用，见 internal/adapter/http/webui/webui.go 的说明）。
    outDir: 'dist',
    sourcemap: false,
    // 首屏体积闸门的粗粒度保护：单个 chunk 超过 600KB 视为拆分失败
    // （NFR-01 要求首屏 gzip ≤ 400KB，未压缩阈值按 ≈1.5x 估算）。
    chunkSizeWarningLimit: 600,
  },
})
