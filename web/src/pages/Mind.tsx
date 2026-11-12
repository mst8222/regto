// 嵌入页：mo-mind 经 iframe 挂载（SPEC-WEB-001 REQ-09 / AC-09）。
//
// 三条硬约束（都由 web/src/embed/protocol.ts 的纯函数 + 单测钉死）：
//   1. token **绝不进 URL**（referer / 访问日志 / 浏览器历史）；
//   2. postMessage 必须指定**精确** targetOrigin，不能用 '*'；
//   3. 未配置 mo-mind 时显示占位，不请求令牌、不报错（OSS-001 R3：缺对方零降级）。

import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Card, Empty, Space, Spin, Typography } from 'antd'

import { api } from '../api/client'
import { ErrorAlert } from '../components/ErrorAlert'
import {
  EMBED_PROTOCOL,
  authPayload,
  buildIframeSrc,
  embedConfigured,
  isTrustedOrigin,
  moMindOrigin,
} from '../embed/protocol'

export default function Mind() {
  const origin = moMindOrigin()
  const configured = embedConfigured()
  const frame = useRef<HTMLIFrameElement>(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState<unknown>(null)

  // 收 ready 再发令牌：iframe 还没加载完就 postMessage 会被直接丢弃，
  // 表现为"嵌入位一直转圈"，是这类集成最常见的假故障。
  useEffect(() => {
    if (!configured) return
    const onMessage = (ev: MessageEvent) => {
      if (!isTrustedOrigin(ev.origin, origin)) return
      if ((ev.data as { type?: string })?.type === EMBED_PROTOCOL.ready) {
        setReady(true)
      }
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [configured, origin])

  const sendToken = useCallback(async () => {
    try {
      const res = await api.embedToken()
      const target = frame.current?.contentWindow
      if (!target) return
      // targetOrigin 必须是精确 origin：'*' 会把令牌广播给 iframe 里的任何内容。
      target.postMessage(authPayload(res.access_token, res.expires_in), origin)
    } catch (e) {
      setError(e)
    }
  }, [origin])

  useEffect(() => {
    if (ready) void sendToken()
  }, [ready, sendToken])

  if (!configured) {
    return (
      <Card title="协作画布">
        <Empty
          description={
            <Space direction="vertical" size={4}>
              <Typography.Text>未配置 mo-mind 嵌入位</Typography.Text>
              <Typography.Text type="secondary">
                设置 VITE_MO_MIND_ORIGIN 后重启前端即可启用；Regto 本身不依赖它。
              </Typography.Text>
            </Space>
          }
        />
      </Card>
    )
  }

  return (
    <Card title="协作画布" style={{ height: '100%' }}>
      {error ? <ErrorAlert error={error} onClose={() => setError(null)} /> : null}
      {!ready ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="正在与 mo-mind 握手…"
          description={
            <Space>
              <Spin size="small" />
              <Typography.Text type="secondary">
                等待 {origin} 就绪信号后下发短 TTL 令牌
              </Typography.Text>
            </Space>
          }
        />
      ) : null}
      {/*
        src 里只有 origin + 路径，**没有任何凭据**。
        sandbox 给到脚本与同源表单能力即可，不给 allow-top-navigation
        ——嵌入页不该有能力把整个 Regto 导航走。
      */}
      <iframe
        ref={frame}
        title="mo-mind"
        src={buildIframeSrc(origin)}
        style={{ width: '100%', height: '70vh', border: '1px solid #303030', borderRadius: 6 }}
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      />
    </Card>
  )
}
