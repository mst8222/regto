// 报表页（SPEC-WEB-001 REQ-08 / AC-08）。
//
// 六张报表共用一个组件：按 id 查映射表决定画什么图，不为每张报表写一套。
// 报表清单来自后端 /api/v1/reports（不是前端硬编码），所以后端加第七张报表时
// 前端会直接列出来——未登记的 id 回落表格，看得到数据而不是白屏。

import { useEffect, useMemo, useState } from 'react'
import { Card, Col, Empty, Row, Segmented, Space, Table, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'

import { api, type ReportMeta, type ReportResponse } from '../api/client'
import { ErrorAlert } from '../components/ErrorAlert'
import { ReportChart } from '../reports/ReportChart'
import { toChartOption, viewFor, type ReportRow } from '../reports/registry'

const CHART_LABEL: Record<string, string> = {
  pie: '饼图',
  bar: '柱状图',
  line: '折线图',
  'stacked-bar': '堆叠柱状图',
  table: '表格',
}

export default function Reports() {
  const [list, setList] = useState<ReportMeta[]>([])
  const [listError, setListError] = useState<unknown>(null)
  const [activeId, setActiveId] = useState<string>('')
  const [report, setReport] = useState<ReportResponse | null>(null)
  const [reportError, setReportError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)
  // 用户手动切「图表 / 表格」的覆盖值；null = 跟随映射表默认值。
  const [override, setOverride] = useState<'chart' | 'table' | null>(null)

  useEffect(() => {
    void (async () => {
      try {
        const res = await api.listReports()
        setList(res.data)
        setListError(null)
        if (res.data.length > 0) {
          setActiveId((prev) => prev || res.data[0].id)
        }
      } catch (e) {
        setListError(e)
      }
    })()
  }, [])

  useEffect(() => {
    if (!activeId) return
    void (async () => {
      setLoading(true)
      try {
        setReport(await api.getReport(activeId))
        setReportError(null)
      } catch (e) {
        setReport(null)
        setReportError(e)
      } finally {
        setLoading(false)
      }
    })()
  }, [activeId])

  const view = useMemo(
    () => viewFor(activeId, report?.columns ?? []),
    [activeId, report?.columns],
  )

  // 切换报表时清掉手动覆盖：上一张报表选的"表格"不该带下一张报表上。
  useEffect(() => {
    setOverride(null)
  }, [activeId])

  // effectiveChart：用户没手动切时跟随映射表。
  const effectiveChart = override === 'table' ? 'table' : view.chart

  const columns = useMemo<ColumnsType<ReportRow>>(
    () =>
      (report?.columns ?? []).map((c) => ({
        title: c,
        dataIndex: c,
        key: c,
        ellipsis: true,
      })),
    [report?.columns],
  )

  const rows = useMemo(() => (report?.rows ?? []) as ReportRow[], [report?.rows])
  const option = useMemo(
    () => (view.chart === 'table' ? {} : toChartOption(view, rows)),
    [rows, view],
  )

  return (
    <Row gutter={16}>
      <Col xs={24} md={6}>
        <Card title="报表" size="small">
          {listError ? <ErrorAlert error={listError} /> : null}
          {list.length === 0 && !listError ? <Empty description="暂无可用报表" /> : null}
          <Space direction="vertical" style={{ width: '100%' }}>
            {list.map((m) => (
              <Card
                key={m.id}
                size="small"
                hoverable
                onClick={() => setActiveId(m.id)}
                style={{
                  borderColor: m.id === activeId ? '#1677ff' : undefined,
                  cursor: 'pointer',
                }}
              >
                <Space direction="vertical" size={2}>
                  <Typography.Text strong>{m.title}</Typography.Text>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {m.category}
                    {m.scoped ? ' · 可按产品过滤' : ''}
                  </Typography.Text>
                </Space>
              </Card>
            ))}
          </Space>
        </Card>
      </Col>

      <Col xs={24} md={18}>
        <Card
          title={report?.title ?? '报表'}
          loading={loading}
          extra={
            <Space>
              <Typography.Text type="secondary">
                {CHART_LABEL[effectiveChart] ?? effectiveChart}
              </Typography.Text>
              <Segmented
                size="small"
                value={effectiveChart === 'table' ? 'table' : 'chart'}
                onChange={(v) => setOverride(v as 'chart' | 'table')}
                options={[
                  { label: '图表', value: 'chart' },
                  { label: '表格', value: 'table' },
                ]}
              />
            </Space>
          }
        >
          {reportError ? <ErrorAlert error={reportError} /> : null}

          {!reportError && rows.length === 0 && !loading ? (
            <Empty description="该报表当前没有数据" />
          ) : null}

          {rows.length > 0 && effectiveChart !== 'table' ? (
            <ReportChart option={option} />
          ) : null}

          {rows.length > 0 && effectiveChart === 'table' ? (
            <Table<ReportRow>
              rowKey={(_, i) => String(i)}
              size="small"
              columns={columns}
              dataSource={rows}
              pagination={rows.length > 20 ? { pageSize: 20 } : false}
              scroll={{ x: 'max-content' }}
            />
          ) : null}
        </Card>
      </Col>
    </Row>
  )
}
