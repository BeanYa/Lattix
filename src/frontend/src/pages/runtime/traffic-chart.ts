import type { ChartOption } from '@/components/echarts'
import { humanizeBytes } from '@/lib/format'
import type { TrafficStatGranularity, TrafficStatPoint } from '@/lib/types'
import type { ChartThemeColors } from '@/pages/costs-shared'

// x 轴标签按粒度格式化（Intl zh-CN，浏览器本地时区）：
// 5m/15m→HH:mm；1h→M/d HH:mm（24h 窗口跨日）；day→M/d；month→yyyy/M。
const LABEL_FORMATS: Record<TrafficStatGranularity, Intl.DateTimeFormat> = {
  '5m': new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
  '15m': new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
  '1h': new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }),
  day: new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric' }),
  month: new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'numeric' }),
}

// 模块级 formatter：同参构建的 option 保持引用相等，图表 option 可确定性比较。
function formatBytesValue(value: unknown): string {
  return humanizeBytes(Number(value))
}

const DATA_ZOOM_THRESHOLD = 60

/**
 * buildTrafficBarOption 由 (points, granularity, colors) 确定性构建流量时序堆叠柱图：
 * 双系列 上传/下载 同栈堆叠，x 标签按粒度格式化，点数 > 60 开启 dataZoom，
 * tooltip/yAxis 以字节单位（B/KB/MB/GB/TB，复用 humanizeBytes）格式化。
 */
export function buildTrafficBarOption(
  points: TrafficStatPoint[],
  g: TrafficStatGranularity,
  colors: ChartThemeColors,
): ChartOption {
  const formatter = LABEL_FORMATS[g]
  const zoomed = points.length > DATA_ZOOM_THRESHOLD
  return {
    color: [colors.palette[0], colors.palette[1]],
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      valueFormatter: formatBytesValue,
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      textStyle: { color: colors.textColor },
      data: ['上传', '下载'],
    },
    grid: { left: 8, right: 16, top: 24, bottom: zoomed ? 56 : 28, containLabel: true },
    xAxis: {
      type: 'category',
      data: points.map((point) => formatter.format(new Date(point.ts))),
      axisLabel: { color: colors.textColor },
      axisLine: { lineStyle: { color: colors.axisColor } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: colors.textColor, formatter: formatBytesValue },
      splitLine: { lineStyle: { color: colors.gridColor } },
    },
    dataZoom: zoomed
      ? [
          { type: 'inside' },
          { type: 'slider', bottom: 24, height: 18, borderColor: colors.axisColor },
        ]
      : [],
    series: [
      {
        name: '上传',
        type: 'bar',
        stack: 'traffic',
        emphasis: { focus: 'series' },
        barMaxWidth: 36,
        data: points.map((point) => point.up),
      },
      {
        name: '下载',
        type: 'bar',
        stack: 'traffic',
        emphasis: { focus: 'series' },
        barMaxWidth: 36,
        data: points.map((point) => point.down),
      },
    ],
  }
}
