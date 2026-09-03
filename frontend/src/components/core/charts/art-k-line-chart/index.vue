<template>
  <div ref="workbenchRef" class="kline-workbench" :class="{ 'is-fullscreen': fullscreen }">
    <div class="kline-toolbar" role="toolbar" aria-label="K 线工具栏">
      <div class="kline-toolbar__group">
        <button
          v-for="item in props.intervals"
          :key="item"
          class="kline-toolbar__button"
          :class="{ 'is-active': item === props.interval }"
          type="button"
          :disabled="props.fixedInterval"
          @click="emit('intervalChange', item)"
        >
          {{ item }}
        </button>
      </div>
      <div class="kline-toolbar__group kline-toolbar__group--selects">
        <ElSelect
          :model-value="props.mainIndicator"
          size="small"
          class="kline-toolbar__select"
          :teleported="false"
          @update:model-value="(value) => emit('mainIndicatorChange', value)"
        >
          <ElOption label="主图指标" value="none" />
          <ElOption label="MA" value="ma" />
          <ElOption label="EMA" value="ema" />
          <ElOption label="BOLL" value="boll" />
        </ElSelect>
        <ElSelect
          :model-value="props.subIndicator"
          size="small"
          class="kline-toolbar__select"
          :teleported="false"
          @update:model-value="(value) => emit('subIndicatorChange', value)"
        >
          <ElOption label="成交量" value="volume" />
          <ElOption label="MACD" value="macd" />
          <ElOption label="RSI" value="rsi" />
          <ElOption label="KDJ" value="kdj" />
          <ElOption label="OBV" value="obv" />
          <ElOption label="WR" value="wr" />
        </ElSelect>
      </div>
      <div class="kline-toolbar__actions">
        <ElTooltip content="重置缩放" placement="top">
          <ElButton text size="small" :icon="Refresh" aria-label="重置缩放" @click="resetZoom" />
        </ElTooltip>
        <ElTooltip content="全屏" placement="top">
          <ElButton
            text
            size="small"
            :icon="FullScreen"
            aria-label="全屏"
            @click="toggleFullscreen"
          />
        </ElTooltip>
      </div>
    </div>
    <div
      ref="chartRef"
      class="relative w-full kline-canvas"
      :style="{ height: props.height }"
      v-loading="props.loading"
    ></div>
  </div>
</template>

<script setup lang="ts">
  import { FullScreen, Refresh } from '@element-plus/icons-vue'
  import type { EChartsOption } from '@/plugins/echarts'
  import { useChartOps, useChartComponent } from '@/hooks/core/useChart'
  import type { KLineChartProps } from '@/types/component/chart'

  defineOptions({ name: 'ArtKLineChart' })

  const emit = defineEmits<{
    signalClick: [signalId: string | number]
    intervalChange: [interval: string]
    mainIndicatorChange: [indicator: 'none' | 'ma' | 'ema' | 'boll']
    subIndicatorChange: [indicator: 'volume' | 'macd' | 'rsi' | 'kdj' | 'obv' | 'wr']
    loadMore: []
    fullscreenChange: [fullscreen: boolean]
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
    dataZoomEnd: 100,
    interval: '1h',
    intervals: () => [
      '1m',
      '3m',
      '5m',
      '15m',
      '30m',
      '1h',
      '2h',
      '4h',
      '6h',
      '8h',
      '12h',
      '1d',
      '3d',
      '1w'
    ],
    mainIndicator: 'none',
    subIndicator: 'volume',
    fixedInterval: false
  })

  const workbenchRef = ref<HTMLElement | null>(null)
  const fullscreen = ref(false)

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
      () => props.selectedSignalId,
      () => props.mainIndicator,
      () => props.subIndicator,
      () => props.interval
    ],
    generateOptions: (): EChartsOption => {
      const chartTheme = useChartOps()
      const upColor = props.colors[0] || chartTheme.colors[3] || '#13deb9'
      const downColor = props.colors[1] || chartTheme.colors[5] || '#fa896b'
      const signalColor = chartTheme.themeColor || '#5d87ff'
      const times = props.data.map((item) => item.time)
      const timeLabels = props.data.map((item) => item.label || item.time)
      const candleByTime = new Map(props.data.map((item) => [item.time, item]))
      const signalsByTime = new Map<string, typeof props.signals>()
      props.signals.forEach((signal) => {
        const items = signalsByTime.get(signal.time) || []
        items.push(signal)
        signalsByTime.set(signal.time, items)
      })
      const showSubChart = props.showVolume || props.subIndicator !== 'volume'
      const visibleTracks = Number(showSubChart)
      const priceBottom = visibleTracks ? '28%' : '10%'
      const xAxes: any[] = [
        {
          type: 'category',
          data: times,
          boundaryGap: true,
          axisTick: getAxisTickStyle(),
          axisLine: getAxisLineStyle(true),
          axisLabel: {
            ...getAxisLabelStyle(true),
            show: visibleTracks === 0,
            formatter: (_value: string, index: number) => timeLabels[index] || ''
          }
        }
      ]
      const yAxes: any[] = [
        {
          type: 'value',
          scale: true,
          position: 'right',
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

      const mainKeys: Record<string, string[]> = {
        ma: ['ma7', 'ma25', 'ma99'],
        ema: ['ema7', 'ema25', 'ema99'],
        boll: ['bollUpper', 'bollMiddle', 'bollLower']
      }
      const mainColors = ['#f0b90b', '#8b5cf6', '#2563eb']
      for (const [index, key] of (mainKeys[props.mainIndicator || 'none'] || []).entries()) {
        series.push({
          name: key,
          type: 'line',
          xAxisIndex: 0,
          yAxisIndex: 0,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1, color: mainColors[index] },
          data: props.data.map((item) => item.indicators?.main?.[key] ?? null)
        })
      }

      if (props.showVolume || props.subIndicator !== 'volume') {
        grids.push({ top: '76%', right: 58, bottom: 18, left: 12, containLabel: true })
        xAxes.push({
          type: 'category',
          gridIndex: 1,
          data: times,
          boundaryGap: true,
          axisTick: { show: false },
          axisLine: getAxisLineStyle(true),
          axisLabel: {
            ...getAxisLabelStyle(true),
            show: true,
            hideOverlap: true,
            formatter: (_value: string, index: number) => timeLabels[index] || ''
          }
        })
        yAxes.push({
          type: 'value',
          gridIndex: 1,
          scale: true,
          position: 'right',
          axisLabel: {
            show: false
          },
          axisLine: { show: false },
          axisTick: { show: false },
          splitLine: { show: false }
        })
        if (props.subIndicator === 'volume') {
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
        const subKeys: Record<string, string[]> = {
          macd: ['macd', 'dif', 'dea'],
          rsi: ['rsi'],
          kdj: ['k', 'd', 'j'],
          obv: ['obv'],
          wr: ['wr']
        }
        const subColors = ['#f0b90b', '#2563eb', '#ef4444']
        for (const [index, key] of (subKeys[props.subIndicator || 'volume'] || []).entries()) {
          series.push({
            name: key,
            type: 'line',
            xAxisIndex: 1,
            yAxisIndex: 1,
            showSymbol: false,
            connectNulls: false,
            lineStyle: { width: 1, color: subColors[index] },
            data: props.data.map((item) => item.indicators?.sub?.[key] ?? null)
          })
        }
      }

      return {
        animation: false,
        grid: grids,
        axisPointer: { type: 'cross', link: [{ xAxisIndex: 'all' }] },
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
            const mainKeys: Record<string, [string, string][]> = {
              ma: [
                ['ma7', 'MA7'],
                ['ma25', 'MA25'],
                ['ma99', 'MA99']
              ],
              ema: [
                ['ema7', 'EMA7'],
                ['ema25', 'EMA25'],
                ['ema99', 'EMA99']
              ],
              boll: [
                ['bollUpper', 'BOLL 上轨'],
                ['bollMiddle', 'BOLL 中轨'],
                ['bollLower', 'BOLL 下轨']
              ]
            }
            const subKeys: Record<string, [string, string][]> = {
              macd: [
                ['dif', 'DIF'],
                ['dea', 'DEA'],
                ['macd', 'MACD']
              ],
              rsi: [['rsi', 'RSI']],
              kdj: [
                ['k', 'K'],
                ['d', 'D'],
                ['j', 'J']
              ],
              obv: [['obv', 'OBV']],
              wr: [['wr', 'WR']]
            }
            const indicatorLines = [
              ...(mainKeys[props.mainIndicator || 'none'] || []).flatMap(([key, label]) => {
                const value = item.indicators?.main?.[key]
                return value === null || value === undefined
                  ? []
                  : [`${label} ${numberText(value)}`]
              }),
              ...(subKeys[props.subIndicator || 'volume'] || []).flatMap(([key, label]) => {
                const value = item.indicators?.sub?.[key]
                return value === null || value === undefined
                  ? []
                  : [`${label} ${numberText(value)}`]
              })
            ]
            return [
              `<strong>${htmlText(item.label || item.time)}</strong>`,
              `开 ${numberText(item.open)} · 高 ${numberText(item.high)}`,
              `低 ${numberText(item.low)} · 收 ${numberText(item.close)}`,
              `量 ${numberText(item.volume)}`,
              ...indicatorLines,
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

  const resetZoom = () =>
    getChartInstance()?.dispatchAction({
      type: 'dataZoom',
      start: props.dataZoomStart,
      end: props.dataZoomEnd
    })
  const toggleFullscreen = async () => {
    if (!workbenchRef.value) return
    if (!document.fullscreenElement) await workbenchRef.value.requestFullscreen()
    else await document.exitFullscreen()
  }
  const handleFullscreenChange = () => {
    fullscreen.value = document.fullscreenElement === workbenchRef.value
    emit('fullscreenChange', fullscreen.value)
    nextTick(() => getChartInstance()?.resize())
  }
  const handleDataZoom = (event: any) => {
    const zoom = event?.batch?.[0] || event
    if (Number(zoom?.start) <= 2) emit('loadMore')
  }

  watch(
    () => [props.mainIndicator, props.subIndicator, props.showVolume],
    () => getChartInstance()?.clear(),
    { flush: 'sync' }
  )

  const bindChartClick = () => {
    const instance = getChartInstance()
    if (!instance) return
    instance.off('click', handleChartClick)
    instance.on('click', handleChartClick)
    instance.off('datazoom', handleDataZoom)
    instance.on('datazoom', handleDataZoom)
  }
  const handleChartVisible = () => nextTick(bindChartClick)

  onMounted(() => {
    nextTick(bindChartClick)
    chartRef.value?.addEventListener('chartVisible', handleChartVisible)
    document.addEventListener('fullscreenchange', handleFullscreenChange)
  })
  onBeforeUnmount(() => {
    chartRef.value?.removeEventListener('chartVisible', handleChartVisible)
    getChartInstance()?.off('click', handleChartClick)
    getChartInstance()?.off('datazoom', handleDataZoom)
    document.removeEventListener('fullscreenchange', handleFullscreenChange)
  })
</script>

<style scoped lang="scss">
  .kline-workbench {
    min-width: 0;
    background: var(--default-box-color);
  }

  .kline-workbench.is-fullscreen {
    padding: 12px;
    background: var(--default-box-color);
  }

  .kline-workbench.is-fullscreen .kline-canvas {
    height: calc(100vh - 62px) !important;
  }

  .kline-toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 14px;
    align-items: center;
    min-height: 38px;
    padding: 0 4px 8px;
    border-bottom: 1px solid var(--art-card-border);
  }

  .kline-toolbar__group,
  .kline-toolbar__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    align-items: center;
  }

  .kline-toolbar__group--selects {
    gap: 6px;
    margin-left: auto;
  }

  .kline-toolbar__button {
    min-width: 32px;
    height: 26px;
    padding: 0 6px;
    font-size: 11px;
    color: var(--art-gray-600);
    cursor: pointer;
    background: transparent;
    border: 0;
    border-radius: 3px;
  }

  .kline-toolbar__button:hover:not(:disabled),
  .kline-toolbar__button.is-active {
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .kline-toolbar__button:disabled {
    cursor: default;
  }

  .kline-toolbar__select {
    width: 92px;
  }

  .kline-toolbar__actions {
    margin-left: 0;
  }

  .kline-canvas {
    min-height: 300px;
  }

  @media (max-width: 640px) {
    .kline-toolbar__group--selects {
      order: 3;
      width: 100%;
      margin-left: 0;
    }

    .kline-toolbar__actions {
      margin-left: auto;
    }
  }
</style>
