<template>
  <ElFormItem v-for="(property, key) in properties" :key="key" :label="String(property.title || key)" :required="schema.required?.includes(key)">
    <div class="schema-fields">
      <template v-if="property.type === 'object' && property.properties">
        <WorkflowSchemaFields :schema="property" :config="objectValue(key)" @update="(child, value) => updateObject(key, child, value)" />
      </template>
      <template v-else-if="property.type === 'object'">
        <div v-for="(value, child) in objectValue(key)" :key="child" class="schema-fields__row">
          <div class="schema-fields__head"><ElInput :model-value="String(child)" aria-label="字段名称" @change="next => renameKey(key, child, next)" /><ElButton text type="danger" @click="removeKey(key, child)">删除</ElButton></div>
          <WorkflowSchemaFields :schema="{ properties: { value: objectItemSchema(property, value) } }" :config="{ value }" @update="(_, next) => updateObject(key, child, next)" />
        </div>
        <ElButton @click="addKey(key, property)">添加字段</ElButton>
      </template>
      <template v-else-if="property.type === 'array' && (property.items?.type === 'object' || property.items?.properties)">
        <div v-for="(value, index) in arrayValue(key)" :key="index" class="schema-fields__row">
          <WorkflowSchemaFields :schema="property.items" :config="value" @update="(child, next) => updateArray(key, index, { ...value, [child]: next })" />
          <ElButton text type="danger" @click="removeItem(key, index)">删除此项</ElButton>
        </div>
        <ElButton @click="emit('update', key, [...arrayValue(key), schemaDefaults(property.items)])">添加{{ property.title || '一项' }}</ElButton>
      </template>
      <ElSelect v-else-if="property['x-coinsphere-proxy']" :model-value="config[key]" @update:model-value="value => emit('update', key, value)"><ElOption :value="0" label="直连" /><ElOption v-for="proxy in proxies" :key="proxy.id" :value="proxy.id" :label="proxy.name" /></ElSelect>
      <WorkflowSchemaField v-else :field="buildSchemaField(key, property)" :value="config[key]" @update="value => updateField(key, value)" />
      <small v-if="property.description">{{ property.description }}</small>
    </div>
  </ElFormItem>
</template>
<script setup lang="ts">
import WorkflowSchemaField from './WorkflowSchemaField.vue'
import { buildSchemaField } from './workflow-schema-field'
import { schemaDefaults } from '../canvas'
import { fetchGetOutboundProxies } from '@/api/system'
const props = defineProps<{ schema: Record<string, any>; config: Record<string, any>; uiSchema?: Record<string, any>; keys?: string[] }>()
const emit = defineEmits<{ (event: 'update', key: string, value: any): void }>()
const variantProperties = (config: Record<string, any>) => Object.assign({}, ...(props.schema.allOf || []).filter((branch: any) => branch.if && Object.entries(branch.if.properties || {}).every(([key, test]) => config[key] === (test as any).const)).map((branch: any) => branch.then?.properties || {}))
function updateField(key: string, value: any) {
  const before = variantProperties(props.config), after = variantProperties({ ...props.config, [key]: value })
  emit('update', key, value)
  for (const [field, schema] of Object.entries(after)) {
    if (JSON.stringify(before[field]) !== JSON.stringify(schema)) emit('update', field, (schema as any).default ?? schemaDefaults(schema as any))
  }
}
const properties = computed<Record<string, any>>(() => {
  const order = (props.uiSchema?.['ui:order'] || []) as string[]
  const rank = (key: string) => order.includes(key) ? order.indexOf(key) : order.length
  return Object.fromEntries(Object.entries({ ...props.schema.properties, ...variantProperties(props.config) }).filter(([key, raw]) => {
    if (props.keys && !props.keys.includes(key)) return false
    const field = raw as Record<string, any>
    const condition = props.uiSchema?.[key]?.['ui:condition']
    if (condition && props.config[condition.field] !== condition.equals) return false
    return Object.entries(field['x-visible-when'] || {}).every(([name, expected]) => Array.isArray(expected) ? expected.includes(props.config[name]) : props.config[name] === expected)
  }).sort(([a], [b]) => rank(a) - rank(b)))
})
const proxies = ref<Array<{ id: number; name: string }>>([])
watch(() => Boolean(Object.values(props.schema.properties || {}).some(raw => (raw as any)['x-coinsphere-proxy'])), async needed => { if (needed) proxies.value = (await fetchGetOutboundProxies()).filter(p => p.isEnabled) }, { immediate: true })
const objectValue = (key: string): Record<string, any> => props.config[key] && typeof props.config[key] === 'object' && !Array.isArray(props.config[key]) ? props.config[key] : {}
const arrayValue = (key: string): any[] => Array.isArray(props.config[key]) ? props.config[key] : []
const objectItemSchema = (schema: Record<string, any>, value: unknown) => typeof schema.additionalProperties === 'object' ? schema.additionalProperties : { type: typeof value === 'boolean' ? 'boolean' : typeof value === 'number' ? 'number' : 'string' }
function updateObject(key: string, child: string, value: any) { emit('update', key, { ...objectValue(key), [child]: value }) }
function renameKey(key: string, child: string, next: string) { next = next.trim(); if (!next || (next !== child && next in objectValue(key))) return; emit('update', key, Object.fromEntries(Object.entries(objectValue(key)).map(([k, v]) => [k === child ? next : k, v]))) }
function removeKey(key: string, child: string) { emit('update', key, Object.fromEntries(Object.entries(objectValue(key)).filter(([k]) => k !== child))) }
function addKey(key: string, property: Record<string, any>) { let child = 'field'; for (let i = 1; child in objectValue(key); i++) child = `field${i}`; const schema = property.additionalProperties; updateObject(key, child, schema?.default ?? (schema?.type === 'object' ? schemaDefaults(schema) : '')) }
function updateArray(key: string, index: number, next: any) { emit('update', key, arrayValue(key).map((value, i) => i === index ? next : value)) }
function removeItem(key: string, index: number) { emit('update', key, arrayValue(key).filter((_, i) => i !== index)) }
</script>
<style scoped>
.schema-fields { display: grid; gap: 8px; width: 100%; }
.schema-fields__row { border-left: 2px solid var(--el-border-color); padding-left: 12px; }
.schema-fields__head { display: flex; gap: 8px; margin-bottom: 10px; }
.schema-fields small { color: var(--el-text-color-secondary); line-height: 1.5; }
</style>
