// 统一错误呈现（SPEC-WEB-001 REQ-06 / AC-06）。
//
// 为什么必须有一个统一组件：problem+json 的三个关键字段（code / detail / request_id）
// 只要有一处忘记展示，排障链路就断——用户只能描述"刚才报错了"，而 request_id
// 是回查留痕的唯一线索。散在各页面里各写一遍迟早会漏，收敛成一个组件就不会。

import { Alert, Button, Space, Typography } from 'antd'
import { Link } from 'react-router-dom'

import { describeApiError, type ErrorAdvice } from '../api/errors'

/** ErrorAlert 把任意抛出物渲染成带排障信息的告警条。 */
export function ErrorAlert({
  error,
  onClose,
}: {
  error: unknown
  onClose?: () => void
}) {
  const advice: ErrorAdvice = describeApiError(error)
  return (
    <Alert
      type="error"
      showIcon
      closable={onClose !== undefined}
      onClose={onClose}
      style={{ marginBottom: 16 }}
      message={
        <Space wrap>
          <span>{advice.title}</span>
          <Typography.Text type="secondary" code>
            {advice.code}
          </Typography.Text>
        </Space>
      }
      description={
        <Space direction="vertical" size={4}>
          <Typography.Text>{advice.detail}</Typography.Text>
          {advice.requestId ? (
            <Typography.Text type="secondary" copyable={{ text: advice.requestId }}>
              留痕 request_id：{advice.requestId}
            </Typography.Text>
          ) : null}
          {advice.action ? (
            <Link to={advice.action.href}>
              <Button type="primary" size="small">
                {advice.action.label}
              </Button>
            </Link>
          ) : null}
        </Space>
      }
    />
  )
}
