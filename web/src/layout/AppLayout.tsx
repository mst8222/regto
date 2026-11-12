import { Layout, Menu, Space, Typography } from 'antd'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'

import { useAuth } from '../auth/AuthContext'
import { navItems, visibleNavItems } from './nav'

const { Header, Content } = Layout

export default function AppLayout() {
  const { subject, roles, logout } = useAuth()
  const location = useLocation()
  const navigate = useNavigate()

  // 菜单只是体验优化，**不是安全边界**：真实判定永远由后端 ACLGuard 完成
  // （SPEC-WEB-001 REQ-07 / DESIGN-WEB-001 DEC-07，前端不复制权限矩阵）。
  const items = visibleNavItems(navItems, roles).map((item) => ({
    key: item.key,
    label: <Link to={item.key}>{item.label}</Link>,
  }))

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header style={{ display: 'flex', alignItems: 'center', gap: 24 }}>
        <Typography.Text strong style={{ color: '#fff', fontSize: 16 }}>
          Regto 绳直
        </Typography.Text>
        <Menu
          theme="dark"
          mode="horizontal"
          selectedKeys={[location.pathname]}
          items={items}
          style={{ flex: 1, minWidth: 0 }}
        />
        <Space>
          <Typography.Text style={{ color: 'rgba(255,255,255,0.75)' }}>
            {subject?.account ?? ''}
          </Typography.Text>
          <Typography.Link
            style={{ color: 'rgba(255,255,255,0.75)' }}
            onClick={() => {
              // 先让服务端撤销 refresh family（清 cookie）再跳走。
              // 不等它完成就 navigate 也能"看起来登出成功"，但 cookie 还在，
              // 下次打开页面会被静默续期——最典型的"退出了又自己进去了"。
              void logout().finally(() => navigate('/login', { replace: true }))
            }}
          >
            退出
          </Typography.Link>
        </Space>
      </Header>
      <Content style={{ padding: 24 }}>
        <Outlet />
      </Content>
    </Layout>
  )
}
