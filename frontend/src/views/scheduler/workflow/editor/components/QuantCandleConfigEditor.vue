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
      class="quant-candle-config__full"
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
  import { fetchQuantInstruments, type QuantInstrument } from '@/api/quant'
  import WorkflowSchemaFields from './WorkflowSchemaFields.vue'

  const props = defineProps<{
    schema: Record<string, any>
    uiSchema?: Record<string, any>
    config: Record<string, any>
  }>()

  const emit = defineEmits<{
    (e: 'update', key: string, value: any): void
  }>()

  const instruments = ref<QuantInstrument[]>([])
  const loading = ref(false)
  let requestID = 0

  const remainingKeys = computed(() =>
    ['intervals', 'candleCount', 'endTime'].filter((key) => props.schema?.properties?.[key])
  )

  const updateInstrument = (value: unknown) =>
    emit('update', 'instrument', String(value || '').trim().toUpperCase())
  const emitUpdate = (key: string, value: any) => emit('update', key, value)

  watch(
    () => props.config.market,
    async (value) => {
      const market = value === 'usdm' ? 'usdm' : 'spot'
      const currentRequest = ++requestID
      loading.value = true
      try {
        const response = await fetchQuantInstruments(market)
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

<style scoped lang="scss">
  .quant-candle-config__full {
    width: 100%;
  }
</style>
