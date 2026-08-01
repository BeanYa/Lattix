import { useEffect, useRef } from 'react'

import * as echarts from 'echarts/core'
import { BarChart, PieChart } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  BarChart,
  PieChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  CanvasRenderer,
])

export type ChartOption = echarts.EChartsCoreOption

export function Chart({ option, className }: { option: ChartOption; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const element = ref.current
    if (!element) return
    const chart = echarts.init(element)
    const observer = new ResizeObserver(() => chart.resize())
    observer.observe(element)
    return () => {
      observer.disconnect()
      chart.dispose()
    }
  }, [])

  useEffect(() => {
    const element = ref.current
    if (!element) return
    echarts.getInstanceByDom(element)?.setOption(option, { notMerge: true })
  }, [option])

  return <div ref={ref} className={className} />
}
