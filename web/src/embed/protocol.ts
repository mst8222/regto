// 嵌入位的配置与握手协议（SPEC-WEB-001 REQ-09 / DESIGN-WEB-001 DEC-09、DEC-14）。
//
// 这一层刻意做成纯函数：嵌入位的安全属性（token 不进 URL、postMessage 必须指定
// 精确 origin）都是可以用单测钉死的，藏在组件里就只能靠 e2e 碰运气。

/** 握手消息类型。两侧约定，改这里等于改协议。 */
export const EMBED_PROTOCOL = {
  /** mo-mind → Regto：iframe 已就绪，可以收令牌了。 */
  ready: 'mo-mind:ready',
  /** Regto → mo-mind：下发短 TTL 令牌。 */
  auth: 'regto:auth',
} as const

/** moMindOrigin 返回配置好的 mo-mind origin；未配置返回空串（嵌入位降级为占位）。 */
export function moMindOrigin(): string {
  return (import.meta.env.VITE_MO_MIND_ORIGIN ?? '').trim().replace(/\/+$/, '')
}

/** embedConfigured 报告是否配置了 mo-mind。 */
export function embedConfigured(): boolean {
  return moMindOrigin() !== ''
}

/**
 * buildIframeSrc 构造 iframe 的 src。
 *
 * 硬约束：src **绝不能携带 token**。URL 会进入 referer 头、访问日志、
 * 浏览器历史与分享链接，等于把凭据写进日志里——这是嵌入位最容易踩的坑。
 */
export function buildIframeSrc(origin: string, path = '/'): string {
  const base = origin.replace(/\/+$/, '')
  const p = path.startsWith('/') ? path : `/${path}`
  return `${base}${p}`
}

/** 判断来自 iframe 的消息是否可信任（origin 必须与配置完全一致）。 */
export function isTrustedOrigin(eventOrigin: string, expected: string): boolean {
  if (expected === '') return false
  return eventOrigin === expected
}

/**
 * authPayload 构造下发令牌的消息体。
 *
 * token 只出现在这个对象里，经 postMessage 传给已校验的 origin；
 * 调用方必须传 targetOrigin，绝不能用 '*'——'*' 会把令牌广播给任何
 * 加载进 iframe 的页面，包括被劫持后的内容。
 */
export function authPayload(token: string, expiresIn: number) {
  return {
    type: EMBED_PROTOCOL.auth,
    token,
    expires_in: expiresIn,
  }
}
