import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'

import {
  api,
  refreshAccessToken,
  setAccessToken,
  setUnauthorizedHandler,
  type MeResponse,
} from '../api/client'

// access token 只活在这个 Provider 的内存里，绝不落 localStorage/sessionStorage
// （SPEC-WEB-001 REQ-04 / DESIGN-WEB-001 DEC-04）：刷新页面即失，由 httpOnly 的
// refresh cookie 静默换回。
//
// roles 只用于菜单粗筛（REQ-07）。**任何允许/拒绝的判断都在服务端**：
// 前端把菜单藏起来只是体验优化，接口该 403 还是 403。
type AuthState = {
  /** 首次静默续期是否还在进行中——为 true 时不能把用户踢去登录页。 */
  booting: boolean
  token: string | null
  subject: MeResponse['subject'] | null
  roles: string[]
  login: (account: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [booting, setBooting] = useState(true)
  const [token, setToken] = useState<string | null>(null)
  const [subject, setSubject] = useState<MeResponse['subject'] | null>(null)
  const [roles, setRoles] = useState<string[]>([])

  // loadMe 取主体与角色：token 变了就重新取，保证菜单随角色变化。
  const loadMe = useCallback(async () => {
    const me = await api.me()
    setSubject(me.subject)
    setRoles(me.roles)
    return me
  }, [])

  const clearSession = useCallback(() => {
    setAccessToken(null)
    setToken(null)
    setSubject(null)
    setRoles([])
  }, [])

  // 启动时静默续期：刷新页面后 access 已丢，但 refresh cookie 还在。
  // 这条路径失败是**正常情况**（没登录过 / cookie 过期），不当错误处理。
  useEffect(() => {
    let alive = true
    void (async () => {
      const fresh = await refreshAccessToken()
      if (!alive) return
      if (fresh) {
        try {
          await loadMe()
          setToken(fresh)
        } catch {
          // me 失败（例如账号被停用）→ 当作未登录，不留半截会话
          clearSession()
        }
      }
      setBooting(false)
    })()
    return () => {
      alive = false
    }
  }, [clearSession, loadMe])

  // 401 静默续期：apiFetch 在拿到 401 时回调这里，成功则重放原请求。
  useEffect(() => {
    setUnauthorizedHandler(async () => {
      const fresh = await refreshAccessToken()
      if (!fresh) {
        clearSession()
        return false
      }
      setToken(fresh)
      return true
    })
    return () => setUnauthorizedHandler(null)
  }, [clearSession])

  const login = useCallback(
    async (account: string, password: string) => {
      const pair = await api.login({ account, password })
      // 只取 access；pair.refresh_token 虽然还在响应体里（兼容 SDK），
      // 前端**不使用**它——一旦存下来，REQ-04 的初衷就白费了。
      setAccessToken(pair.access_token)
      setToken(pair.access_token)
      await loadMe()
    },
    [loadMe],
  )

  const logout = useCallback(async () => {
    try {
      // 服务端撤销 refresh family 并清 cookie；失败也要清本地（本地清空是兜底）
      await api.logout()
    } catch {
      // 会话已失效时登出接口返回 204，走到这里说明是网络/服务端问题。
      // 用户点的"退出"必须生效，不能因为服务端不可用就把人留在登录态。
    } finally {
      clearSession()
    }
  }, [clearSession])

  const value = useMemo<AuthState>(
    () => ({ booting, token, subject, roles, login, logout }),
    [booting, token, subject, roles, login, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth 必须在 AuthProvider 内使用')
  }
  return ctx
}
