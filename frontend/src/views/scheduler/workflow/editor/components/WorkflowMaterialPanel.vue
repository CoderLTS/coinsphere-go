<!-- 工作流编辑器页面或组件：WorkflowMaterialPanel。 -->
<template>
  <section class="material-panel" :class="{ 'material-panel--collapsed': collapsed }">
    <div class="material-panel__header">
      <div>
        <strong>节点物料</strong>
        <span>拖拽物料到画布，或点击添加到画布中央</span>
      </div>
      <ElButton text @click="$emit('toggle')">
        {{ collapsed ? '展开' : '收起' }}
      </ElButton>
    </div>

    <ElScrollbar v-if="!collapsed" class="material-panel__scroll">
      <div v-for="group in groups" :key="group.key" class="material-group">
        <div class="material-group__title">{{ group.title }}</div>

        <button
          v-for="item in group.items"
          :key="item.typeCode"
          type="button"
          class="material-card"
          :class="{ 'material-card--disabled': disabledCodes.includes(item.typeCode) }"
          :draggable="!disabledCodes.includes(item.typeCode)"
          @click="$emit('add', item.typeCode)"
          @dragstart="handleDragStart($event, item.typeCode)"
        >
          <div
            class="material-card__badge"
            :style="{ background: `${item.color}18`, color: item.color }"
          >
            {{ item.iconText }}
          </div>
          <div class="material-card__content">
            <strong>{{ item.title }}</strong>
            <span>{{ item.description }}</span>
          </div>
        </button>
      </div>
    </ElScrollbar>
  </section>
</template>

<script setup lang="ts">
  import type { WorkflowMaterialGroup } from '../types'

  interface Props {
    groups: WorkflowMaterialGroup[]
    disabledCodes?: string[]
    collapsed?: boolean
  }

  interface Emits {
    (e: 'add', typeCode: string): void
    (e: 'toggle'): void
  }

  withDefaults(defineProps<Props>(), {
    disabledCodes: () => [],
    collapsed: false
  })

  defineEmits<Emits>()

  const handleDragStart = (event: DragEvent, typeCode: string) => {
    event.dataTransfer?.setData('application/coinsphere-workflow-material', typeCode)
    event.dataTransfer?.setData('text/plain', typeCode)
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move'
    }
  }
</script>

<style scoped lang="scss">
  .material-panel {
    display: flex;
    flex-direction: column;
    width: 320px;
    max-height: calc(100vh - 168px);
    background: rgb(255 255 255 / 0.94);
    backdrop-filter: blur(14px);
    border: 1px solid rgb(203 213 225 / 0.75);
    border-radius: 20px;
    box-shadow: 0 18px 36px rgb(15 23 42 / 0.08);
    transition:
      width 0.24s ease,
      transform 0.24s ease;
  }

  .material-panel--collapsed {
    width: 128px;
  }

  .material-panel__header {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    justify-content: space-between;
    padding: 14px 16px 12px;
    border-bottom: 1px solid rgb(226 232 240 / 0.85);

    strong {
      display: block;
      font-size: 14px;
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

  .material-panel__scroll {
    min-height: 0;
  }

  .material-group {
    padding: 14px 12px 4px;
  }

  .material-group__title {
    padding-left: 4px;
    margin-bottom: 10px;
    font-size: 12px;
    font-weight: 600;
    color: #475569;
  }

  .material-card {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    width: 100%;
    padding: 12px;
    text-align: left;
    cursor: pointer;
    background: #fff;
    border: 1px solid rgb(226 232 240 / 0.88);
    border-radius: 16px;
    transition:
      border-color 0.2s ease,
      box-shadow 0.2s ease,
      transform 0.2s ease;

    & + & {
      margin-top: 10px;
    }

    &:hover {
      border-color: rgb(59 130 246 / 0.42);
      box-shadow: 0 12px 22px rgb(15 23 42 / 0.08);
      transform: translateY(-1px);
    }
  }

  .material-card--disabled {
    cursor: not-allowed;
    box-shadow: none;
    opacity: 0.45;
    transform: none;
  }

  .material-card__badge {
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    font-size: 14px;
    font-weight: 700;
    border-radius: 12px;
  }

  .material-card__content {
    min-width: 0;

    strong {
      display: block;
      font-size: 13px;
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
</style>
