// 报表 → 呈现方式的映射表（SPEC-WEB-001 REQ-08 / AC-08）。
//
// 为什么是映射表而不是 6 个组件：后端统一返回 { columns, rows }，
// 六张报表的差别只在「哪列是类目、哪几列是数值、画什么图」。
// 写成 6 个组件等于把同一段表格/图表逻辑复制 6 遍，加第七张报表要再复制一遍。
//
// 关键设计：**未知报表回落表格**。后端加了新报表而前端没同步时，
// 用户仍然看得到数据（表格形式），而不是白屏或报错。这是"配置化"的意义所在。

export type ChartKind = 'pie' | 'bar' | 'line' | 'stacked-bar' | 'table'

export type ReportView = {
  chart: ChartKind
  /** 类目列（X 轴 / 饼图扇区名）。 */
  categoryKey: string
  /** 数值列。pie 只取第一列。 */
  valueKeys: string[]
}

const REGISTRY: Record<string, ReportView> = {
  // 状态分布类：占比关系，饼图最直观。
  story_status: { chart: 'pie', categoryKey: 'status', valueKeys: ['count'] },
  case_coverage: { chart: 'pie', categoryKey: 'status', valueKeys: ['count'] },
  // 时间趋势类：看走势，折线。
  story_burndown: { chart: 'line', categoryKey: 'period', valueKeys: ['opened', 'closed'] },
  // 多结果并列且总量有意义 → 堆叠柱（既看趋势也看构成）。
  test_execution_trend: {
    chart: 'stacked-bar',
    categoryKey: 'period',
    valueKeys: ['pass', 'fail', 'blocked', 'skip', 'na'],
  },
  // 每行是一个主体的贡献量 → 柱状比大小。
  ai_contribution: { chart: 'bar', categoryKey: 'actor_id', valueKeys: ['count'] },
  // 列多且含 dimension 区分（release/story 两种维度混排），铺表比强行出图清楚。
  release_quality: { chart: 'table', categoryKey: 'id', valueKeys: [] },
}

/** 未知报表的兜底视图：用全部列铺表格。 */
export function fallbackView(columns: string[]): ReportView {
  return { chart: 'table', categoryKey: columns[0] ?? '', valueKeys: [] }
}

/** viewFor 查映射表；未登记的报表回落表格而不是报错。 */
export function viewFor(id: string, columns: string[] = []): ReportView {
  return REGISTRY[id] ?? fallbackView(columns)
}

/** 已登记的报表 id（供测试穷举：新增报表必须同步登记，否则会静默变成表格）。 */
export const knownReportIds = Object.keys(REGISTRY)

export type ReportRow = Record<string, unknown>

/** toNumber 把后端可能的 string/number/null 收敛成 number（空值算 0）。 */
export function toNumber(v: unknown): number {
  if (typeof v === 'number') return Number.isFinite(v) ? v : 0
  if (typeof v === 'string') {
    const n = Number(v)
    return Number.isFinite(n) ? n : 0
  }
  return 0
}

/** 生成 ECharts option。纯函数，便于单测（不需要 DOM）。 */
export function toChartOption(view: ReportView, rows: ReportRow[]): Record<string, unknown> {
  const categories = rows.map((r) => String(r[view.categoryKey] ?? ''))

  if (view.chart === 'pie') {
    const key = view.valueKeys[0]
    return {
      tooltip: { trigger: 'item' },
      legend: { bottom: 0 },
      series: [
        {
          type: 'pie',
          radius: ['40%', '70%'],
          data: rows.map((r) => ({ name: String(r[view.categoryKey] ?? ''), value: toNumber(r[key]) })),
        },
      ],
    }
  }

  const series = view.valueKeys.map((k) => ({
    name: k,
    type: view.chart === 'line' ? 'line' : 'bar',
    // 堆叠柱才加 stack；折线与普通柱各自独立，加了会把多个系列叠成一根。
    ...(view.chart === 'stacked-bar' ? { stack: 'total' } : {}),
    data: rows.map((r) => toNumber(r[k])),
  }))

  return {
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0 },
    grid: { left: 48, right: 24, top: 24, bottom: 48 },
    xAxis: { type: 'category', data: categories },
    yAxis: { type: 'value' },
    series,
  }
}
