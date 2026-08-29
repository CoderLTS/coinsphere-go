<template>
  <div v-if="current.kind === 'condition'" class="condition-tree condition-tree--leaf">
    <div class="condition-tree__leaf-header">
      <ElInput v-model="current.name" placeholder="条件名称" @change="emitCurrent" />
      <ElSelect v-model="current.interval" @change="emitCurrent">
        <ElOption v-for="item in intervals" :key="item" :label="item" :value="item" />
      </ElSelect>
      <ElSelect v-model="current.indicator" @change="changeIndicator">
        <ElOption
          v-for="item in indicators"
          :key="item.value"
          :label="item.label"
          :value="item.value"
        />
      </ElSelect>
    </div>
    <div class="condition-tree__params">
      <template v-if="current.indicator === 'volume_spike'">
        <ElInputNumber
          v-model="current.parameters.lookback"
          :min="1"
          :max="500"
          placeholder="前 N 根"
          @change="emitCurrent"
        />
        <ElInput v-model="current.parameters.multiplier" placeholder="倍数" @change="emitCurrent" />
      </template>
      <template v-else-if="current.indicator === 'price_change'">
        <ElInputNumber
          v-model="current.parameters.lookback"
          :min="1"
          :max="500"
          placeholder="K 线数量"
          @change="emitCurrent"
        />
        <ElSelect v-model="current.parameters.mode" @change="emitCurrent">
          <ElOption label="首尾涨幅" value="rise" /><ElOption label="首尾跌幅" value="fall" />
          <ElOption label="绝对涨跌幅" value="absolute" /><ElOption
            label="最高最低振幅"
            value="amplitude"
          />
        </ElSelect>
        <ElInput
          v-model="current.parameters.threshold"
          placeholder="阈值 %"
          @change="emitCurrent"
        />
      </template>
      <template v-else-if="current.indicator === 'macd'">
        <ElInputNumber
          v-model="current.parameters.fastPeriod"
          :min="1"
          :max="100"
          placeholder="快线周期"
          @change="emitCurrent"
        />
        <ElInputNumber
          v-model="current.parameters.slowPeriod"
          :min="2"
          :max="200"
          placeholder="慢线周期"
          @change="emitCurrent"
        />
        <ElInputNumber
          v-model="current.parameters.signalPeriod"
          :min="1"
          :max="100"
          placeholder="信号周期"
          @change="emitCurrent"
        />
        <ElSelect v-model="current.parameters.signal" @change="emitCurrent">
          <ElOption label="金叉" value="golden_cross" /><ElOption
            label="死叉"
            value="death_cross"
          />
          <ElOption label="DIF 位于零轴上方" value="dif_above_zero" /><ElOption
            label="DIF 位于零轴下方"
            value="dif_below_zero"
          />
        </ElSelect>
      </template>
      <template v-else-if="current.indicator === 'kdj'">
        <ElInputNumber
          v-model="current.parameters.period"
          :min="2"
          :max="200"
          placeholder="RSV 周期"
          @change="emitCurrent"
        />
        <ElInputNumber
          v-model="current.parameters.kSmoothing"
          :min="1"
          :max="50"
          placeholder="K 平滑"
          @change="emitCurrent"
        />
        <ElInputNumber
          v-model="current.parameters.dSmoothing"
          :min="1"
          :max="50"
          placeholder="D 平滑"
          @change="emitCurrent"
        />
        <ElSelect v-model="current.parameters.signal" @change="emitCurrent">
          <ElOption label="金叉" value="golden_cross" /><ElOption
            label="死叉"
            value="death_cross"
          />
          <ElOption label="K 高于" value="k_above" /><ElOption label="K 低于" value="k_below" />
          <ElOption label="D 高于" value="d_above" /><ElOption label="D 低于" value="d_below" />
          <ElOption label="J 高于" value="j_above" /><ElOption label="J 低于" value="j_below" />
        </ElSelect>
        <ElInput
          v-if="!['golden_cross', 'death_cross'].includes(current.parameters.signal)"
          v-model="current.parameters.threshold"
          placeholder="阈值"
          @change="emitCurrent"
        />
      </template>
      <template v-else-if="current.indicator === 'rsi'">
        <ElInputNumber
          v-model="current.parameters.period"
          :min="2"
          :max="200"
          placeholder="周期"
          @change="emitCurrent"
        />
        <ElSelect v-model="current.parameters.direction" @change="emitCurrent"
          ><ElOption label="高于" value="above" /><ElOption label="低于" value="below"
        /></ElSelect>
        <ElInput v-model="current.parameters.threshold" placeholder="阈值" @change="emitCurrent" />
      </template>
      <template v-else>
        <ElInputNumber
          v-model="current.parameters.period"
          :min="2"
          :max="500"
          placeholder="周期"
          @change="emitCurrent"
        />
        <ElInput
          v-model="current.parameters.multiplier"
          placeholder="标准差倍数"
          @change="emitCurrent"
        />
        <ElSelect v-model="current.parameters.signal" @change="emitCurrent"
          ><ElOption label="突破上轨" value="close_above_upper" /><ElOption
            label="跌破下轨"
            value="close_below_lower"
        /></ElSelect>
      </template>
    </div>
  </div>

  <div v-else class="condition-tree">
    <div class="condition-tree__header">
      <div>
        <strong>指标条件</strong>
        <div class="condition-tree__formula">{{ formula }}</div>
      </div>
      <ElSegmented v-model="current.operator" :options="operatorOptions" @change="emitCurrent" />
    </div>

    <div class="condition-tree__group">
      <div v-for="(child, index) in current.children" :key="child.id" class="condition-tree__child">
        <QuantConditionTreeEditor
          :model-value="child"
          :depth="depth + 1"
          :total-leaf-count="overallLeafCount"
          @update:model-value="updateChild(index, $event)"
        />
        <div class="condition-tree__actions">
          <ElTooltip content="上移" placement="top">
            <ElButton
              :icon="ArrowUp"
              circle
              size="small"
              :disabled="index === 0"
              @click="moveChild(index, -1)"
            />
          </ElTooltip>
          <ElTooltip content="下移" placement="top">
            <ElButton
              :icon="ArrowDown"
              circle
              size="small"
              :disabled="index === current.children.length - 1"
              @click="moveChild(index, 1)"
            />
          </ElTooltip>
          <ElTooltip content="删除" placement="top">
            <ElButton
              :icon="Delete"
              circle
              size="small"
              type="danger"
              :disabled="current.children.length <= 1"
              @click="removeChild(index)"
            />
          </ElTooltip>
        </div>
      </div>
    </div>

    <div class="condition-tree__toolbar">
      <ElTooltip content="新增指标条件" placement="top">
        <ElButton
          :icon="Plus"
          circle
          size="small"
          :disabled="overallLeafCount >= 16"
          @click="addLeaf"
        />
      </ElTooltip>
      <ElTooltip content="新增条件分组" placement="top">
        <ElButton
          :icon="FolderAdd"
          circle
          size="small"
          :disabled="depth >= 3 || overallLeafCount >= 16"
          @click="addGroup"
        />
      </ElTooltip>
      <span class="condition-tree__limit">{{ overallLeafCount }}/16 条件，最多 4 层</span>
    </div>
  </div>
</template>

<script setup lang="ts">
  import { computed } from 'vue'
  import { ArrowDown, ArrowUp, Delete, FolderAdd, Plus } from '@element-plus/icons-vue'

  interface ConditionNode {
    id: string
    kind: 'group' | 'condition'
    operator?: 'AND' | 'OR'
    children?: ConditionNode[]
    name?: string
    interval?: string
    indicator?: string
    parameters?: Record<string, any>
  }

  const props = withDefaults(
    defineProps<{ modelValue: ConditionNode | null; depth?: number; totalLeafCount?: number }>(),
    {
      depth: 1,
      totalLeafCount: 0
    }
  )
  const emit = defineEmits<{ (e: 'update:modelValue', value: ConditionNode): void }>()
  const operatorOptions = [
    { label: '全部', value: 'AND' },
    { label: '任一', value: 'OR' }
  ]
  const nextId = (prefix: string) => `${prefix}_${crypto.randomUUID()}`
  const defaultLeaf = (): ConditionNode => ({
    id: nextId('condition'),
    kind: 'condition',
    name: '放量',
    interval: '5m',
    indicator: 'volume_spike',
    parameters: { lookback: 20, multiplier: '2' }
  })
  const defaultGroup = (): ConditionNode => ({
    id: nextId('group'),
    kind: 'group',
    operator: 'AND',
    children: [defaultLeaf()]
  })
  const fallback = defaultGroup()
  const current = computed<any>(() => {
    const value = props.modelValue
    const result = value || fallback
    if (result.kind === 'condition' && !result.parameters) result.parameters = {}
    return result
  })

  const clone = (value: ConditionNode): ConditionNode => JSON.parse(JSON.stringify(value))
  const update = (value: ConditionNode) => emit('update:modelValue', value)
  const emitCurrent = () => update(clone(current.value))
  const changeIndicator = (indicator: string) => {
    const next = clone(current.value)
    next.indicator = indicator
    next.parameters = defaultParameters(indicator)
    update(next)
  }
  const updateChild = (index: number, value: ConditionNode) => {
    const next = clone(current.value)
    next.children![index] = value
    update(next)
  }
  const moveChild = (index: number, offset: number) => {
    const next = clone(current.value)
    const target = index + offset
    if (target < 0 || target >= next.children!.length) return
    ;[next.children![index], next.children![target]] = [
      next.children![target],
      next.children![index]
    ]
    update(next)
  }
  const removeChild = (index: number) => {
    const next = clone(current.value)
    if (next.children!.length <= 1) return
    next.children!.splice(index, 1)
    update(next)
  }
  const addLeaf = () => {
    const next = clone(current.value)
    next.children!.push(defaultLeaf())
    update(next)
  }
  const addGroup = () => {
    const next = clone(current.value)
    next.children!.push(defaultGroup())
    update(next)
  }
  const leafCountOf = (node: ConditionNode): number =>
    node.kind === 'condition'
      ? 1
      : (node.children || []).reduce((total, child) => total + leafCountOf(child), 0)
  const leafCount = computed(() => leafCountOf(current.value))
  const overallLeafCount = computed(() => props.totalLeafCount || leafCount.value)
  const formulaOf = (node: ConditionNode): string => {
    if (node.kind === 'condition') return String(node.name || '指标条件')
    const parts = (node.children || []).map((child) =>
      child.kind === 'group' ? `(${formulaOf(child)})` : formulaOf(child)
    )
    return parts.join(` ${node.operator || 'AND'} `)
  }
  const formula = computed(() => formulaOf(current.value))
  const intervals = [
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
  ]
  const indicators = [
    { value: 'volume_spike', label: '放量' },
    { value: 'price_change', label: '价格波动' },
    { value: 'macd', label: 'MACD' },
    { value: 'kdj', label: 'KDJ' },
    { value: 'rsi', label: 'RSI' },
    { value: 'bollinger', label: '布林带' }
  ]
  const defaultParameters = (indicator: string): Record<string, any> => {
    switch (indicator) {
      case 'price_change':
        return { lookback: 5, mode: 'absolute', threshold: '5' }
      case 'macd':
        return { fastPeriod: 12, slowPeriod: 26, signalPeriod: 9, signal: 'golden_cross' }
      case 'kdj':
        return { period: 9, kSmoothing: 3, dSmoothing: 3, signal: 'golden_cross', threshold: '80' }
      case 'rsi':
        return { period: 14, direction: 'below', threshold: '30' }
      case 'bollinger':
        return { period: 20, multiplier: '2', signal: 'close_above_upper' }
      default:
        return { lookback: 20, multiplier: '2' }
    }
  }
</script>

<style scoped>
  .condition-tree {
    padding: 10px;
    border: 1px solid var(--workflow-overlay-border-soft, #c8c7c1);
    background: var(--workflow-overlay-soft, #e7e6e0);
  }

  .condition-tree__header,
  .condition-tree__leaf-header,
  .condition-tree__params,
  .condition-tree__toolbar,
  .condition-tree__actions {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .condition-tree__header {
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .condition-tree__leaf-header,
  .condition-tree__params {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 6px;
  }

  .condition-tree__params {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    margin-top: 6px;
  }

  .condition-tree__formula {
    margin-top: 3px;
    color: var(--workflow-overlay-muted, #6d7477);
    font-size: 12px;
    word-break: break-word;
  }

  .condition-tree__child {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 6px;
    align-items: start;
    margin: 6px 0;
  }

  .condition-tree__actions {
    padding-top: 8px;
    flex-direction: column;
  }

  .condition-tree__toolbar {
    padding-top: 6px;
    border-top: 1px solid var(--workflow-overlay-border-subtle, #d7d5ce);
  }

  .condition-tree__limit {
    color: var(--workflow-overlay-muted, #6d7477);
    font-size: 12px;
  }

  @media (width <= 640px) {
    .condition-tree__leaf-header,
    .condition-tree__params {
      grid-template-columns: 1fr;
    }
  }
</style>
