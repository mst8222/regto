import { lazy, Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Spin } from 'antd'

import { AuthProvider, useAuth } from './auth/AuthContext'
import AppLayout from './layout/AppLayout'

// 路由级懒加载：AntD 与 ECharts 不进首屏 bundle（SPEC-WEB-001 NFR-01 首屏 ≤400KB）。
const Login = lazy(() => import('./pages/Login'))
const Stories = lazy(() => import('./pages/Stories'))
const Approvals = lazy(() => import('./pages/Approvals'))
const Reports = lazy(() => import('./pages/Reports'))
const Mind = lazy(() => import('./pages/Mind'))

function Fallback() {
  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: '60vh' }}>
      <Spin />
    </div>
  )
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { booting, token } = useAuth()
  // 首次静默续期没结束就跳登录页，会让"刷新页面"变成"被登出"——
  // 这是 httpOnly cookie 方案最典型的实现坑。
  if (booting) {
    return <Fallback />
  }
  if (!token) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

function Shell() {
  const { booting, token } = useAuth()
  return (
    <Suspense fallback={<Fallback />}>
      <Routes>
        <Route
          path="/login"
          element={booting ? <Fallback /> : token ? <Navigate to="/stories" replace /> : <Login />}
        />
        <Route
          element={
            <RequireAuth>
              <AppLayout />
            </RequireAuth>
          }
        >
          <Route index element={<Navigate to="/stories" replace />} />
          <Route path="/stories" element={<Stories />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/mind" element={<Mind />} />
        </Route>
        <Route path="*" element={<Navigate to="/stories" replace />} />
      </Routes>
    </Suspense>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Shell />
      </AuthProvider>
    </BrowserRouter>
  )
}
