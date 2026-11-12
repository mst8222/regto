import { useState } from 'react'
import { Button, Card, Form, Input, Typography } from 'antd'

import { useAuth } from '../auth/AuthContext'
import { ErrorAlert } from '../components/ErrorAlert'

type FormValues = { account: string; password: string }

export default function Login() {
  const { login } = useAuth()
  // 存原始 Error 对象而不是字符串：request_id 与 code 要交给 ErrorAlert 呈现，
  // 提前拼成字符串就把排障信息压扁了（REQ-06）。
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)

  const onFinish = async (values: FormValues) => {
    setLoading(true)
    setError(null)
    try {
      await login(values.account, values.password)
    } catch (err) {
      setError(err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: '100vh', padding: 24 }}>
      <Card style={{ width: 380 }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>
          Regto 绳直
        </Typography.Title>
        <Typography.Paragraph type="secondary">AI 项目协作开发管理</Typography.Paragraph>
        {error ? <ErrorAlert error={error} /> : null}
        <Form<FormValues> layout="vertical" onFinish={onFinish} requiredMark={false}>
          <Form.Item
            name="account"
            label="账号"
            rules={[{ required: true, message: '请输入账号' }]}
          >
            <Input autoComplete="username" autoFocus />
          </Form.Item>
          <Form.Item
            name="password"
            label="密码"
            rules={[{ required: true, message: '请输入密码' }]}
          >
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  )
}
