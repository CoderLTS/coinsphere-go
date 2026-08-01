<!-- 工作流编辑器页面或组件：WorkflowMetaPopover。 -->
<template>
  <div v-if="visible" class="meta-popover">
    <div class="meta-popover__header">
      <div>
        <strong>基础信息</strong>
        <span
          >这里只维护工作流定义本身。工作流标识由系统自动生成并保持稳定，开始入口类型回到画布中的开始节点配置。</span
        >
      </div>
      <ElButton text @click="$emit('close')">关闭</ElButton>
    </div>

    <ElForm label-position="top" class="meta-popover__form">
      <ElFormItem label="工作流标识">
        <ElInput
          :model-value="localModel.code"
          disabled
          :placeholder="mode === 'create' ? '保存后由系统自动生成稳定标识' : ''"
        />
        <div class="meta-popover__hint"
          >用于版本归组、Webhook 地址和运行态定位，创建后保持稳定。</div
        >
      </ElFormItem>

      <ElFormItem label="工作流名称">
        <ElInput
          v-model.trim="localModel.displayName"
          placeholder="请输入工作流名称"
          @blur="emitChange"
        />
      </ElFormItem>

      <ElFormItem class="meta-popover__description" label="工作流说明">
        <ElInput
          v-model.trim="localModel.description"
          type="textarea"
          :rows="3"
          placeholder="描述这条工作流的业务职责"
          @blur="emitChange"
        />
      </ElFormItem>
    </ElForm>

    <div class="meta-popover__footer">
      <ElButton @click="$emit('close')">取消</ElButton>
      <ElButton type="primary" @click="$emit('submit', cloneModel(localModel))">应用</ElButton>
    </div>
  </div>
</template>

<script setup lang="ts">
  import type { WorkflowEditorMetaForm, WorkflowEditorMode } from '../types'

  interface Props {
    visible: boolean
    model: WorkflowEditorMetaForm
    mode: WorkflowEditorMode
  }

  interface Emits {
    (e: 'update:model', value: WorkflowEditorMetaForm): void
    (e: 'submit', value: WorkflowEditorMetaForm): void
    (e: 'close'): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  const cloneModel = (value: WorkflowEditorMetaForm): WorkflowEditorMetaForm => ({
    code: value.code,
    displayName: value.displayName,
    description: value.description
  })

  const localModel = reactive<WorkflowEditorMetaForm>(cloneModel(props.model))

  watch(
    () => [props.visible, props.model],
    () => {
      Object.assign(localModel, cloneModel(props.model))
    },
    { deep: true }
  )

  const emitChange = () => {
    localModel.code = localModel.code.trim()
    localModel.displayName = localModel.displayName.trim()
    localModel.description = localModel.description.trim()
    emit('update:model', cloneModel(localModel))
  }
</script>

<style scoped lang="scss">
  .meta-popover {
    width: min(560px, calc(100vw - 48px));
    padding: 18px 18px 16px;
    background: rgb(255 255 255 / 0.97);
    backdrop-filter: blur(18px);
    border: 1px solid rgb(203 213 225 / 0.82);
    border-radius: 22px;
    box-shadow: 0 24px 40px rgb(15 23 42 / 0.12);
  }

  .meta-popover__header {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 12px;

    strong {
      display: block;
      font-size: 16px;
      color: #0f172a;
    }

    span {
      display: block;
      margin-top: 4px;
      font-size: 12px;
      line-height: 1.6;
      color: #64748b;
    }
  }

  .meta-popover__form {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0 16px;
  }

  .meta-popover__description {
    grid-column: 1 / -1;
  }

  .meta-popover__hint {
    margin-top: 6px;
    font-size: 12px;
    line-height: 1.5;
    color: #64748b;
  }

  .meta-popover__footer {
    display: flex;
    gap: 10px;
    justify-content: flex-end;
    margin-top: 8px;
  }
</style>
