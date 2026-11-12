import { useQuery } from '@tanstack/react-query'
import { Card, Table, Tag, Typography } from 'antd'

import { api, type StoryListItem } from '../api/client'
import { ErrorAlert } from '../components/ErrorAlert'

// 状态色沿用项目约定：需求状态是业务语义，不用「涨红跌绿」那套。
const statusColor: Record<string, string> = {
  draft: 'default',
  reviewing: 'gold',
  active: 'blue',
  changing: 'orange',
  closed: 'green',
}

export default function Stories() {
  const { data, error, isLoading } = useQuery({
    queryKey: ['stories', { page: 1, per_page: 20 }],
    queryFn: () => api.listStories({ page: 1, per_page: 20 }),
  })

  // 统一走 ErrorAlert：含 code / detail / 可复制的 request_id，
  // AI 治理类 403 还会给出去处（REQ-06）。
  if (error) {
    return <ErrorAlert error={error} />
  }

  return (
    <Card
      title="需求"
      extra={
        <Typography.Text type="secondary">
          共 {data?.page.total ?? 0} 条
        </Typography.Text>
      }
    >
      <Table<StoryListItem>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={{ current: 1, pageSize: 20, total: data?.page.total ?? 0, showSizeChanger: false }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 80 },
          { title: '标题', dataIndex: 'title' },
          {
            title: '状态',
            dataIndex: 'status',
            width: 120,
            render: (v: string) => <Tag color={statusColor[v] ?? 'default'}>{v}</Tag>,
          },
          { title: '阶段', dataIndex: 'stage', width: 120 },
          { title: '优先级', dataIndex: 'pri', width: 90 },
        ]}
      />
    </Card>
  )
}
