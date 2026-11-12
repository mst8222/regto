// 审批队列页（SPEC-WEB-001 REQ-11 / DESIGN-WEB-001 DEC-13）。
//
// 数据源是后端既有的 /api/v1/agent/approvals 三件套，**不另造查询**：
// 前端自己判定"哪些留痕待审"就得复制一遍 AI 策略档位（三档 + 能力白名单），
// 那正是 REQ-07 禁止的"前端复制判定逻辑"，而且必然与守卫实际拦截的集合漂移。

import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Empty,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'

import { ApiError, api, type PendingApproval } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { ErrorAlert } from '../components/ErrorAlert'
import { canDecideApprovals } from '../layout/nav'

// occurred_at_ms 是毫秒时间戳；后端给的 UTC，前端按本地时区呈现。
// 留痕是审计材料，时间必须能被人直接读，不能甩一个裸时间戳。
function formatTime(ms: number): string {
  if (!ms) return '—'
  return new Date(ms).toLocaleString('zh-CN', { hour12: false })
}

const ACTION_COLOR: Record<string, string> = {
  approve: 'green',
  reject: 'red',
}

export default function Approvals() {
  const { roles } = useAuth()
  const canDecide = useMemo(() => canDecideApprovals(roles), [roles])

  const [items, setItems] = useState<PendingApproval[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [acting, setActing] = useState<string | null>(null)

  // 待决策的留痕 + 意见弹窗
  const [pending, setPending] = useState<{ item: PendingApproval; approve: boolean } | null>(null)
  const [comment, setComment] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setItems(await api.listApprovals())
      setLoadError(null)
    } catch (e) {
      setLoadError(e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const submit = useCallback(async () => {
    if (!pending) return
    const { item, approve } = pending
    setActing(item.trace_id)
    try {
      const call = approve ? api.approve : api.reject
      await call(item.trace_id, comment)
      message.success(approve ? '已批准' : '已驳回')
      setPending(null)
      setComment('')
      await load()
    } catch (e) {
      // 409 = 该留痕已被别人决策过。此时正确的动作是刷新队列，
      // 而不是让用户对着一条已经不存在的记录反复点。
      if (e instanceof ApiError && (e.status === 409 || e.status === 404)) {
        message.warning(e.message)
        setPending(null)
        await load()
      } else {
        setLoadError(e)
      }
    } finally {
      setActing(null)
    }
  }, [comment, load, pending])

  const columns = useMemo<ColumnsType<PendingApproval>>(
    () => [
      {
        title: '留痕 ID',
        dataIndex: 'trace_id',
        width: 220,
        render: (v: string) => (
          <Typography.Text copyable={{ text: v }} code>
            {v}
          </Typography.Text>
        ),
      },
      { title: '发起者', dataIndex: 'actor_id', width: 140 },
      {
        title: '动作',
        dataIndex: 'action',
        width: 120,
        render: (v: string) => <Tag color={ACTION_COLOR[v] ?? 'blue'}>{v}</Tag>,
      },
      {
        title: '对象',
        width: 180,
        render: (_, r) => (
          <span>
            {r.object_type}
            {r.object_id ? ` / ${r.object_id}` : ''}
          </span>
        ),
      },
      { title: '摘要', dataIndex: 'summary' },
      {
        title: '时间',
        dataIndex: 'occurred_at_ms',
        width: 180,
        render: (v: number) => formatTime(v),
      },
      ...(canDecide
        ? [
            {
              title: '操作',
              width: 140,
              render: (_: unknown, r: PendingApproval) => (
                <Space>
                  <Button
                    type="link"
                    size="small"
                    loading={acting === r.trace_id}
                    onClick={() => {
                      setComment('')
                      setPending({ item: r, approve: true })
                    }}
                  >
                    批准
                  </Button>
                  <Button
                    type="link"
                    size="small"
                    danger
                    loading={acting === r.trace_id}
                    onClick={() => {
                      setComment('')
                      setPending({ item: r, approve: false })
                    }}
                  >
                    驳回
                  </Button>
                </Space>
              ),
            },
          ]
        : []),
    ],
    [acting, canDecide],
  )

  return (
    <Card title="审批队列">
      {loadError ? <ErrorAlert error={loadError} onClose={() => setLoadError(null)} /> : null}

      {!canDecide ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="当前角色可查看队列，审批操作需管理员权限"
          description="菜单与按钮的显隐只是体验优化；是否真的有权批准，由服务端判定。"
        />
      ) : null}

      <Table<PendingApproval>
        rowKey="trace_id"
        loading={loading}
        columns={columns}
        dataSource={items}
        pagination={false}
        locale={{
          emptyText: <Empty description={loadError ? '加载失败' : '没有待审批的留痕'} />,
        }}
      />

      <Modal
        open={pending !== null}
        title={pending?.approve ? '批准该操作' : '驳回该操作'}
        okText={pending?.approve ? '批准' : '驳回'}
        cancelText="取消"
        okButtonProps={{ danger: pending?.approve === false }}
        confirmLoading={acting !== null}
        onOk={() => void submit()}
        onCancel={() => setPending(null)}
      >
        <Space direction="vertical" size={8} style={{ width: '100%' }}>
          <Typography.Text type="secondary">
            {pending ? `${pending.item.actor_id} · ${pending.item.action} · ${pending.item.object_type}` : ''}
          </Typography.Text>
          <Typography.Paragraph ellipsis={{ rows: 3 }}>
            {pending?.item.summary}
          </Typography.Paragraph>
          <Input.TextArea
            rows={3}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="审批意见（可选，会写入留痕）"
          />
        </Space>
      </Modal>
    </Card>
  )
}
