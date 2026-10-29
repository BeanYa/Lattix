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

export interface ChartSize {
  width: number
  height: number
}

/** 静态 option，或按容器实测尺寸构建 option（尺寸变化时自动重建）。 */
export type ChartOptionInput = ChartOption | ((size: ChartSize) => ChartOption)

export function Chart({ option, className }: { option: ChartOptionInput; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const optionRef = useRef(option)
  optionRef.current = option
  const sizeRef = useRef<ChartSize>({ width: 0, height: 0 })

  useEffect(() => {
    const element = ref.current
    if (!element) return
    const chart = echarts.init(element)
    sizeRef.current = { width: element.clientWidth, height: element.clientHeight }
    const observer = new ResizeObserver(() => {
      const size = { width: element.clientWidth, height: element.clientHeight }
      sizeRef.current = size
      chart.resize()
      // 尺寸感知 option 的布局以像素计算，真实尺寸变化后必须整体重建
      if (typeof optionRef.current === 'function') {
        chart.setOption(optionRef.current(size), { notMerge: true })
      }
    })
    observer.observe(element)
    return () => {
      observer.disconnect()
      chart.dispose()
    }
  }, [])

  useEffect(() => {
    const element = ref.current
    if (!element) return
    echarts
      .getInstanceByDom(element)
      ?.setOption(typeof option === 'function' ? option(sizeRef.current) : option, {
        notMerge: true,
      })
  }, [option])

  return <div ref={ref} className={className} />
}
