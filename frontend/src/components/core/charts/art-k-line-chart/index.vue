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

  const props = withDefaults(defineProps<KLineChartProps>(), {
    height: '36rem',
    loading: false,
    isEmpty: false,
    colors: () => ['#5eaa74', '#ff705b'],
    data: () => [],
    signals: () => [],
    showVolume: true,
    showTarget: true,
    showDataZoom: true,
    dataZoomStart: 25,
    dataZoomEnd: 100
  })

  const numberText = (value: number | null | undefined) => {
    if (value === null || value === undefined || Number.isNaN(value)) return '--'
    return value.toLocaleString('zh-CN', { maximumFractionDigits: 8 })
  }

  const actionLabel: Record<string, string> = {
    buy: 'BUY',
    sell: 'SELL',
    flat: 'FLAT',
    hold: 'HOLD'
  }

  const {
    chartRef,
    getAxisLineStyle,
    getAxisLabelStyle,
    getAxisTickStyle,
    getSplitLineStyle,
    getTooltipStyle
  } = useChartComponent({
    props,
    checkEmpty: () => !props.data.length,
    watchSources: [
      () => props.data,
      () => props.signals,
      () => props.colors,
      () => props.showVolume,
      () => props.showTarget,
      () => props.showDataZoom,
      () => props.dataZoomStart,
      () => props.dataZoomEnd
    ],
    generateOptions: (): EChartsOption => {
      const upColor = props.colors[0] || useChartOps().colors[3] || '#5eaa74'
      const downColor = props.colors[1] || '#ff705b'
      const times = props.data.map((item) => item.time)
      const signalByTime = new Map(props.signals.map((item) => [item.time, item]))
      const closeByTime = new Map(props.data.map((item) => [item.time, item.close]))
      let lastTarget: number | null = null
      const targetData = props.data.map((item) => {
        if (item.target !== null && item.target !== undefined) lastTarget = item.target
        return lastTarget
      })
      const visibleTracks = Number(props.showVolume) + Number(props.showTarget)
      const priceBottom = visibleTracks === 2 ? '42%' : visibleTracks === 1 ? '27%' : '12%'
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
            symbolSize: 42,
            data: props.signals
              .filter((item) => item.action !== 'hold' && closeByTime.has(item.time))
              .map((item) => ({
                name: actionLabel[item.action],
                coord: [item.time, closeByTime.get(item.time)],
                symbol: item.action === 'flat' ? 'diamond' : 'triangle',
                symbolRotate: item.action === 'sell' ? 180 : 0,
                symbolOffset: item.action === 'sell' ? [0, -22] : [0, 22],
                itemStyle: {
                  color:
                    item.action === 'buy'
                      ? '#c7f46b'
                      : item.action === 'sell'
                        ? '#ff705b'
                        : '#eab24d',
                  borderColor: '#17191b',
                  borderWidth: 1
                },
                label: {
                  show: true,
                  formatter: actionLabel[item.action],
                  color: '#17191b',
                  fontSize: 9,
                  fontWeight: 700
                }
              }))
          }
        }
      ]

      let nextGridIndex = 1
      if (props.showVolume) {
        grids.push({ top: '64%', right: 58, height: '11%', left: 12, containLabel: true })
        xAxes.push({
          type: 'category',
          gridIndex: nextGridIndex,
          data: times,
          boundaryGap: true,
          axisTick: { show: false },
          axisLine: getAxisLineStyle(true),
          axisLabel: { show: false }
        })
        yAxes.push({
          type: 'value',
          gridIndex: nextGridIndex,
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
          xAxisIndex: nextGridIndex,
          yAxisIndex: nextGridIndex,
          barMaxWidth: 8,
          data: props.data.map((item) => ({
            value: item.volume || 0,
            itemStyle: { color: item.close >= item.open ? `${upColor}a6` : `${downColor}a6` }
          }))
        })
        nextGridIndex += 1
      }

      if (props.showTarget) {
        grids.push({ top: '80%', right: 58, height: '10%', left: 12, containLabel: true })
        xAxes.push({
          type: 'category',
          gridIndex: nextGridIndex,
          data: times,
          boundaryGap: true,
          axisTick: getAxisTickStyle(),
          axisLine: getAxisLineStyle(true),
          axisLabel: getAxisLabelStyle(true)
        })
        yAxes.push({
          type: 'value',
          gridIndex: nextGridIndex,
          min: -1,
          max: 1,
          interval: 1,
          axisLabel: getAxisLabelStyle(true),
          splitLine: getSplitLineStyle(true)
        })
        series.push({
          name: '目标仓位',
          type: 'line',
          xAxisIndex: nextGridIndex,
          yAxisIndex: nextGridIndex,
          data: targetData,
          step: 'end',
          showSymbol: false,
          connectNulls: false,
          lineStyle: { color: '#9e8cff', width: 2 },
          itemStyle: { color: '#9e8cff' },
          areaStyle: { color: 'rgba(158, 140, 255, 0.08)' }
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
            const signal = signalByTime.get(item.time)
            return [
              `<strong>${item.time}</strong>`,
              `开 ${numberText(item.open)} · 高 ${numberText(item.high)}`,
              `低 ${numberText(item.low)} · 收 ${numberText(item.close)}`,
              `量 ${numberText(item.volume)}`,
              signal
                ? `${actionLabel[signal.action]} · 目标 ${numberText(signal.target)}`
                : `目标 ${numberText(targetData[index])}`
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
                borderColor: '#c9c9c2',
                fillerColor: 'rgba(158, 140, 255, 0.12)',
                handleStyle: { color: '#9e8cff', borderColor: '#17191b' }
              }
            ]
          : undefined
      } as EChartsOption
    }
  })
</script>
