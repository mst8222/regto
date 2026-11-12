/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** mo-mind 的 origin，例如 http://127.0.0.1:5174。未配置则嵌入位显示占位。 */
  readonly VITE_MO_MIND_ORIGIN?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
