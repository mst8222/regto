import type { components } from './generated/schema'

// 前后端共享的类型全部来自 OpenAPI 契约生成（SPEC-WEB-001 REQ-05）：
// 手写一份「和契约差不多」的类型等于第二真源，改契约时不会有任何报错。
export type StoryList = components['schemas']['StoryList']
export type StoryListItem = components['schemas']['StoryListItem']
export type Story = components['schemas']['Story']
export type TokenPair = components['schemas']['TokenPair']
export type LoginRequest = components['schemas']['LoginRequest']
export type MeResponse = components['schemas']['MeResponse']
export type PendingApproval = components['schemas']['PendingApproval']
export type ApprovalDecision = components['schemas']['ApprovalDecision']
export type ApprovalComment = components['schemas']['ApprovalDecisionRequest']
export type ReportMeta = components['schemas']['ReportMeta']
export type ReportResponse = components['schemas']['ReportResponse']
export type EmbedTokenResponse = components['schemas']['EmbedTokenResponse']

const API_BASE = '/api/v1'

// ApiError 是 problem+json 的结构化视图（RFC 7807）。
// request_id 必须保留：它是回查留痕（traces）的唯一线索，丢了就只能靠时间猜。
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId?: string
  readonly meta?: Record<string, unknown>

  constructor(
    status: number,
    code: string,
    detail: string,
    requestId?: string,
    meta?: Record<string, unknown>,
  ) {
    super(detail)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
    this.meta = meta
  }
}

// access token 仅存内存（SPEC-WEB-001 REQ-04 / DESIGN-WEB-001 DEC-04）：
// 放 localStorage 等于把长期凭据交给任何一次 XSS；刷新页面由 refresh cookie 静默换回。
let accessToken: string | null = null

export function setAccessToken(token: string | null): void {
  accessToken = token
}

export function getAccessToken(): string | null {
  return accessToken
}

// onUnauthorized 由认证层注入（M6-B）：401 时静默 refresh 一次并重放原请求。
// 返回 true 表示续期成功、可以重放。
type UnauthorizedHandler = () => Promise<boolean>
let onUnauthorized: UnauthorizedHandler | null = null

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  onUnauthorized = handler
}

async function toApiError(res: Response): Promise<ApiError> {
  const contentType = res.headers.get('Content-Type') ?? ''
  if (contentType.includes('application/problem+json')) {
    const body = (await res.json()) as {
      code?: string
      detail?: string
      title?: string
      request_id?: string
      meta?: Record<string, unknown>
    }
    return new ApiError(
      res.status,
      body.code ?? 'UNKNOWN',
      body.detail ?? body.title ?? res.statusText,
      body.request_id,
      body.meta,
    )
  }
  return new ApiError(res.status, 'UNKNOWN', `${res.status} ${res.statusText}`)
}

/** apiFetch 是唯一出网口：注入凭据、解包 problem+json、401 续期重放。 */
export async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
  allowRetry = true,
): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (accessToken) {
    headers.set('Authorization', `Bearer ${accessToken}`)
  }

  const res = await fetch(`${API_BASE}${path}`, { ...init, headers })

  if (res.status === 401 && allowRetry && onUnauthorized) {
    if (await onUnauthorized()) {
      return apiFetch<T>(path, init, false)
    }
  }
  if (!res.ok) {
    throw await toApiError(res)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

function buildQuery(params: Record<string, string | number | boolean | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') {
      q.set(k, String(v))
    }
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

export const api = {
  login: (body: LoginRequest) =>
    apiFetch<TokenPair>('/auth/login', { method: 'POST', body: JSON.stringify(body) }),

  // 不带 body：refresh token 在 httpOnly cookie 里，JS 读不到也不该读（REQ-04）。
  // allowRetry=false：续期本身失败时不能再触发一轮续期，否则 401 → refresh → 401 无限递归。
  refresh: () => apiFetch<TokenPair>('/auth/refresh', { method: 'POST' }, false),

  me: () => apiFetch<MeResponse>('/auth/me'),

  logout: () => apiFetch<void>('/auth/logout', { method: 'POST' }, false),

  listStories: (params: { page?: number; per_page?: number; keyword?: string } = {}) =>
    apiFetch<StoryList>(`/stories${buildQuery(params)}`),

  // ---- 审批队列（SPEC-WEB-001 REQ-11 / DESIGN-WEB-001 DEC-13）----
  // 直接复用后端既有三件套，不另造查询：否则前端看到的队列会与守卫生效判定
  // 的集合不一致，变成第二个真源。
  listApprovals: () => apiFetch<PendingApproval[]>('/agent/approvals'),

  approve: (traceId: string, comment?: string) =>
    apiFetch<ApprovalDecision>(`/agent/approvals/${encodeURIComponent(traceId)}/approve`, {
      method: 'POST',
      body: JSON.stringify({ comment: comment ?? '' }),
    }),

  reject: (traceId: string, comment?: string) =>
    apiFetch<ApprovalDecision>(`/agent/approvals/${encodeURIComponent(traceId)}/reject`, {
      method: 'POST',
      body: JSON.stringify({ comment: comment ?? '' }),
    }),

  // ---- 报表（SPEC-WEB-001 REQ-08）----
  listReports: () => apiFetch<{ data: ReportMeta[] }>('/reports'),

  getReport: (id: string, productId?: number) =>
    apiFetch<ReportResponse>(`/reports/${encodeURIComponent(id)}${buildQuery({ product_id: productId })}`),

  // ---- 嵌入位（SPEC-WEB-001 REQ-09 / DEC-14）----
  // 签的是自己，权限不变；短 TTL 与 postMessage 传输才是这里的保护。
  embedToken: () => apiFetch<EmbedTokenResponse>('/auth/embed-token', { method: 'POST' }),
}

// refreshAccessToken 是**单飞**（single-flight）的续期入口。
//
// 为什么必须串行：refresh token 是一次性轮换的，同一页面并发 3 个请求同时 401 时，
// 若各自发起续期，第 2、3 次就是「重放旧令牌」——服务端按泄露处理，连坐撤销整个
// family，用户被莫名踢下线。共享同一个 in-flight Promise 是唯一正确的做法。
let refreshInFlight: Promise<string | null> | null = null

export function refreshAccessToken(): Promise<string | null> {
  if (refreshInFlight) {
    return refreshInFlight
  }
  refreshInFlight = (async () => {
    try {
      const pair = await api.refresh()
      setAccessToken(pair.access_token)
      return pair.access_token
    } catch {
      setAccessToken(null)
      return null
    } finally {
      refreshInFlight = null
    }
  })()
  return refreshInFlight
}
