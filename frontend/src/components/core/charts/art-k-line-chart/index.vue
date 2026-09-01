<template>
  <div
    ref="chartRef"
    class="relative w-full"
    :style="{ height: props.height }"
    v-loading="props.loading"
  ></div>
</template>

<script setup lang="ts">
  import type { EChartsOption } from '@/plugins/echarts'
  import { useChartOps, useChartComponent } from '@/hooks/core/useChart'
  import type { KLineChartProps } from '@/types/component/chart'

  defineOptions({ name: 'ArtKLineChart' })

  const emit = defineEmits<{
    signalClick: [signalId: string | number]
  }>()

  const props = withDefaults(defineProps<KLineChartProps>(), {
    height: '36rem',
    loading: false,
    isEmpty: false,
    colors: () => ['#13deb9', '#fa896b'],
    data: () => [],
    signals: () => [],
    showVolume: true,
    showDataZoom: true,
    dataZoomStart: 25,
    dataZoomEnd: 100
  })

  const numberText = (value: number | null | undefined) => {
    if (value === null || value === undefined || Number.isNaN(value)) return '--'
    return value.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
  }

  const htmlText = (value: string) =>
    value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')

  const {
    chartRef,
    isDark,
    getAxisLineStyle,
    getAxisLabelStyle,
    getAxisTickStyle,
    getSplitLineStyle,
    getTooltipStyle,
    getChartInstance
  } = useChartComponent({
    props,
    checkEmpty: () => !props.data.length,
    watchSources: [
      () => props.data,
      () => props.signals,
      () => props.colors,
      () => props.showVolume,
      () => props.showDataZoom,
      () => props.dataZoomStart,
      () => props.dataZoomEnd,
      () => props.selectedSignalId
    ],
    generateOptions: (): EChartsOption => {
      const chartTheme = useChartOps()
      const upColor = props.colors[0] || chartTheme.colors[3] || '#13deb9'
      const downColor = props.colors[1] || chartTheme.colors[5] || '#fa896b'
      const signalColor = chartTheme.themeColor || '#5d87ff'
      const times = props.data.map((item) => item.time)
      const candleByTime = new Map(props.data.map((item) => [item.time, item]))
      const signalsByTime = new Map<string, typeof props.signals>()
      props.signals.forEach((signal) => {
        const items = signalsByTime.get(signal.time) || []
        items.push(signal)
        signalsByTime.set(signal.time, items)
      })
      const visibleTracks = Number(props.showVolume)
      const priceBottom = visibleTracks ? '27%' : '12%'
      const xAxes: any[] = [
        {
          type: 'category',
          data: times,
          boundaryGap: true,
          axisTick: getAxisTickStyle(),
          axisLine: getAxisLineStyle(true),
          axisLabel: { ...getAxisLabelStyle(true), show: visibleTracks === 0 }
        }
      ]
      const yAxes: any[] = [
        {
          type: 'value',
          scale: true,
          axisLabel: getAxisLabelStyle(true),
          axisLine: getAxisLineStyle(true),
          splitLine: getSplitLineStyle(true)
        }
      ]
      const grids: any[] = [
        { top: 18, right: 58, bottom: priceBottom, left: 12, containLabel: true }
      ]
      const series: any[] = [
        {
          name: 'K 线',
          type: 'candlestick',
          xAxisIndex: 0,
          yAxisIndex: 0,
          data: props.data.map((item) => [item.open, item.close, item.low, item.high]),
          itemStyle: {
            color: upColor,
            color0: downColor,
            borderColor: upColor,
            borderColor0: downColor,
            borderWidth: 1
          },
          markPoint: {
            data: Array.from(signalsByTime.entries())
              .filter(([time]) => candleByTime.has(time))
              .flatMap<any>(([time, items]) => {
                const candle = candleByTime.get(time)!
                if (items.every((signal) => !signal.action)) {
                  return [
                    {
                      name: '信号',
                      value: items.length,
                      coord: [time, candle.close],
                      symbol: 'pin',
                      symbolSize: 40,
                      symbolOffset: [0, -22],
                      itemStyle: {
                        color: signalColor,
                        borderColor: isDark.value ? '#161618' : '#ffffff',
                        borderWidth: 1
                      },
                      label: {
                        show: true,
                        formatter: String(items.length),
                        color: '#ffffff',
                        fontSize: 10,
                        fontWeight: 700
                      }
                    }
                  ]
                }
                return items.map((signal, signalIndex) => {
                  const action = signal.action
                  const selected =
                    signal.id !== undefined &&
                    props.selectedSignalId !== undefined &&
                    String(signal.id) === String(props.selectedSignalId)
                  const buy = action === 'buy'
                  const sell = action === 'sell'
                  return {
                    name: signal.name,
                    value: action ? '' : items.length,
                    coord: [time, buy ? candle.low : sell ? candle.high : candle.close],
                    symbol: action ? (action === 'hold' ? 'circle' : 'triangle') : 'pin',
                    symbolRotate: sell ? 180 : 0,
                    symbolSize: selected ? 44 : action === 'hold' ? 20 : action ? 32 : 40,
                    symbolOffset: [(signalIndex - (items.length - 1) / 2) * 18, buy ? 18 : -18],
                    signalId: signal.id,
                    itemStyle: {
                      color: buy ? '#13deb9' : sell ? '#fa896b' : signalColor,
                      borderColor: selected ? signalColor : isDark.value ? '#161618' : '#ffffff',
                      borderWidth: selected ? 3 : 1
                    },
                    label: {
                      show: !action,
                      formatter: String(items.length),
                      color: '#ffffff',
                      fontSize: 10,
                      fontWeight: 700
                    }
                  }
                })
              })
          }
        }
      ]

      if (props.showVolume) {
        grids.push({ top: '72%', right: 58, height: '13%', left: 12, containLabel: true })
        xAxes.push({
          type: 'category',
          gridIndex: 1,
          data: times,
          boundaryGap: true,
          axisTick: { show: false },
          axisLine: getAxisLineStyle(true),
          axisLabel: { show: false }
        })
        yAxes.push({
          type: 'value',
          gridIndex: 1,
          scale: true,
          axisLabel: {
            ...getAxisLabelStyle(true),
            formatter: (value: number) => numberText(value)
          },
          splitLine: { show: false }
        })
        series.push({
          name: '成交量',
          type: 'bar',
          xAxisIndex: 1,
          yAxisIndex: 1,
          barMaxWidth: 8,
          data: props.data.map((item) => ({
            value: item.volume || 0,
            itemStyle: { color: item.close >= item.open ? `${upColor}a6` : `${downColor}a6` }
          }))
        })
      }

      return {
        animation: false,
        grid: grids,
        axisPointer: { link: [{ xAxisIndex: 'all' }] },
        tooltip: getTooltipStyle('axis', {
          confine: true,
          formatter: (params: any[]) => {
            const index = Number(params?.[0]?.dataIndex ?? -1)
            const item = props.data[index]
            if (!item) return ''
            const signalLines = (signalsByTime.get(item.time) || []).flatMap((signal) => [
              `<strong>${htmlText(signal.name)}</strong>`,
              htmlText(signal.summary)
            ])
            return [
              `<strong>${item.time}</strong>`,
              `开 ${numberText(item.open)} · 高 ${numberText(item.high)}`,
              `低 ${numberText(item.low)} · 收 ${numberText(item.close)}`,
              `量 ${numberText(item.volume)}`,
              ...(signalLines.length ? ['<strong>信号</strong>', ...signalLines] : [])
            ].join('<br>')
          }
        }),
        xAxis: xAxes,
        yAxis: yAxes,
        series,
        dataZoom: props.showDataZoom
          ? [
              {
                type: 'inside',
                xAxisIndex: xAxes.map((_, index) => index),
                start: props.dataZoomStart,
                end: props.dataZoomEnd
              },
              {
                type: 'slider',
                xAxisIndex: xAxes.map((_, index) => index),
                bottom: 2,
                height: 18,
                start: props.dataZoomStart,
                end: props.dataZoomEnd,
                borderColor: isDark.value ? '#353b48' : '#dfe4ec',
                backgroundColor: isDark.value ? '#202226' : '#f2f4f5',
                fillerColor: `${signalColor}1f`,
                handleStyle: {
                  color: signalColor,
                  borderColor: isDark.value ? '#161618' : '#ffffff'
                }
              }
            ]
          : undefined
      } as EChartsOption
    }
  })

  const handleChartClick = (params: any) => {
    const signalId = params?.data?.signalId
    if (params?.componentType === 'markPoint' && signalId !== undefined) {
      emit('signalClick', signalId)
    }
  }

  const bindChartClick = () => {
    const instance = getChartInstance()
    if (!instance) return
    instance.off('click', handleChartClick)
    instance.on('click', handleChartClick)
  }

  onMounted(() => nextTick(bindChartClick))
  onBeforeUnmount(() => getChartInstance()?.off('click', handleChartClick))
</script>
