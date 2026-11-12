// ECharts 渲染壳（SPEC-WEB-001 REQ-08 / NFR-01）。
//
// 按需引入而不是 `import * as echarts from 'echarts'`：全量包 gzip 后 ~330KB，
// 会把首屏闸门（400KB）吃掉大半。这里只注册用到的三种图与四个组件，
// 且本组件只被懒加载的报表页引用，不进首屏 chunk。

import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  BarChart,
  LineChart,
  PieChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  TitleComponent,
  CanvasRenderer,
])

/** ReportChart 把 option 渲染到固定高度的画布，并跟随容器缩放。 */
export function ReportChart({
  option,
  height = 360,
}: {
  option: Record<string, unknown>
  height?: number
}) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    if (!el.current) return
    chart.current = echarts.init(el.current)
    const onResize = () => chart.current?.resize()
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      chart.current?.dispose()
      chart.current = null
    }
  }, [])

  useEffect(() => {
    // 用 notMerge：切换报表时旧的 series 若残留会叠加成错图。
    chart.current?.setOption(option, true)
  }, [option])

  return <div ref={el} style={{ width: '100%', height }} />
}
