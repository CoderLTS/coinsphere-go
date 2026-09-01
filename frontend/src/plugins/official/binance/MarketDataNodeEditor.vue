<template>
  <WorkflowSchemaFields
    :schema="schema"
    :ui-schema="uiSchema"
    :config="config"
    :keys="['market']"
    @update="emitUpdate"
  />

  <ElFormItem :label="String(schema?.properties?.instrument?.title || 'Instrument')">
    <ElSelect
      :model-value="config.instrument"
      class="market-data-editor__full"
      filterable
      allow-create
      default-first-option
      clearable
      :loading="loading"
      @update:model-value="updateInstrument"
    >
      <ElOption
        v-for="item in instruments"
        :key="item.symbol"
        :label="`${item.symbol} · ${item.baseAsset}/${item.quoteAsset}`"
        :value="item.symbol"
      />
    </ElSelect>
  </ElFormItem>

  <WorkflowSchemaFields
    :schema="schema"
    :ui-schema="uiSchema"
    :config="config"
    :keys="remainingKeys"
    @update="emitUpdate"
  />
</template>

<script setup lang="ts">
  import { fetchBinanceInstruments, type BinanceInstrument } from './api'
  import WorkflowSchemaFields from '@/views/scheduler/workflow/editor/components/WorkflowSchemaFields.vue'

  const props = defineProps<{
    schema: Record<string, any>
    uiSchema?: Record<string, any>
    config: Record<string, any>
  }>()

  const emit = defineEmits<{
    (event: 'update', key: string, value: any): void
  }>()

  const instruments = ref<BinanceInstrument[]>([])
  const loading = ref(false)
  let requestID = 0

  const remainingKeys = computed(() =>
    ['proxyId', 'intervals', 'candleCount', 'endTime'].filter(
      (key) => props.schema?.properties?.[key]
    )
  )

  const updateInstrument = (value: unknown) =>
    emit(
      'update',
      'instrument',
      String(value || '')
        .trim()
        .toUpperCase()
    )
  const emitUpdate = (key: string, value: any) => emit('update', key, value)

  watch(
    () => props.config.market,
    async (value) => {
      const market = value === 'usdm' ? 'usdm' : 'spot'
      const currentRequest = ++requestID
      loading.value = true
      try {
        const response = await fetchBinanceInstruments(market)
        if (currentRequest === requestID) instruments.value = response.items
      } catch {
        if (currentRequest === requestID) instruments.value = []
      } finally {
        if (currentRequest === requestID) loading.value = false
      }
    },
    { immediate: true }
  )
</script>

<style scoped>
  .market-data-editor__full {
    width: 100%;
  }
</style>
