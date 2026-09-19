<!-- 工作流编辑器页面或组件：WorkflowEditorToolbar。 -->
<template>
  <div class="editor-toolbar">
    <div class="editor-toolbar__back">
      <ElTooltip content="返回" placement="bottom">
        <ElButton plain class="editor-toolbar__icon-btn" @click="$emit('back')">
          <ElIcon><ArrowLeft /></ElIcon>
        </ElButton>
      </ElTooltip>
    </div>

    <div class="editor-toolbar__tools editor-toolbar__surface">
      <div class="editor-toolbar__tools-group editor-toolbar__tools-group--left">
        <ElTooltip :content="materialsVisible ? '隐藏节点面板' : '显示节点面板'" placement="bottom">
          <ElButton
            :class="[
              'editor-toolbar__icon-btn',
              { 'editor-toolbar__icon-btn--active': materialsVisible }
            ]"
            @click="$emit('toggle-materials')"
          >
            <ElIcon><Grid /></ElIcon>
          </ElButton>
        </ElTooltip>

        <div class="editor-toolbar__zoom-group">
          <ElTooltip content="撤销" placement="bottom">
            <ElButton class="editor-toolbar__icon-btn" @click="$emit('undo')">
              <ElIcon><RefreshLeft /></ElIcon>
            </ElButton>
          </ElTooltip>

          <ElTooltip content="前进" placement="bottom">
            <ElButton class="editor-toolbar__icon-btn" @click="$emit('redo')">
              <ElIcon><RefreshRight /></ElIcon>
            </ElButton>
          </ElTooltip>

          <ElTooltip content="缩小" placement="bottom">
            <ElButton class="editor-toolbar__icon-btn" @click="$emit('zoom-out')">
              <ElIcon><Minus /></ElIcon>
            </ElButton>
          </ElTooltip>

          <span class="editor-toolbar__zoom">{{ zoomText }}</span>

          <ElTooltip content="放大" placement="bottom">
            <ElButton class="editor-toolbar__icon-btn" @click="$emit('zoom-in')">
              <ElIcon><Plus /></ElIcon>
            </ElButton>
          </ElTooltip>
        </div>

        <ElTooltip content="居中" placement="bottom">
          <ElButton class="editor-toolbar__icon-btn" @click="$emit('center-content')">
            <ElIcon><Aim /></ElIcon>
          </ElButton>
        </ElTooltip>

        <ElTooltip content="适配画布" placement="bottom">
          <ElButton class="editor-toolbar__icon-btn" @click="$emit('fit-view')">
            <ElIcon><FullScreen /></ElIcon>
          </ElButton>
        </ElTooltip>
      </div>

      <span class="editor-toolbar__divider" aria-hidden="true"></span>

      <div class="editor-toolbar__tools-group editor-toolbar__tools-group--right">
        <ElTooltip content="基础信息" placement="bottom">
          <ElButton class="editor-toolbar__icon-btn" @click="$emit('open-meta')">
            <ElIcon><Document /></ElIcon>
          </ElButton>
        </ElTooltip>

        <ElTooltip :content="jsonVisible ? '隐藏 JSON 定义' : '显示 JSON 定义'" placement="bottom">
          <ElButton
            :class="[
              'editor-toolbar__icon-btn',
              { 'editor-toolbar__icon-btn--active': jsonVisible }
            ]"
            @click="$emit('toggle-json')"
          >
            <ElIcon><DocumentCopy /></ElIcon>
          </ElButton>
        </ElTooltip>

        <ElTooltip content="校验" placement="bottom">
          <ElButton
            class="editor-toolbar__icon-btn"
            :loading="validating"
            @click="$emit('validate')"
          >
            <ElIcon v-if="!validating"><CircleCheck /></ElIcon>
          </ElButton>
        </ElTooltip>
      </div>
    </div>

    <div class="editor-toolbar__actions">
      <div class="editor-toolbar__meta editor-toolbar__surface">
        <span class="editor-toolbar__page">{{ title }}</span>
        <span :class="['editor-toolbar__status', `editor-toolbar__status--${statusType}`]">
          {{ statusText }}
        </span>
      </div>

      <ElButton
        type="primary"
        class="editor-toolbar__save"
        :loading="saving"
        @click="$emit('save')"
      >
        保存定义
      </ElButton>
    </div>
  </div>
</template>

<script setup lang="ts">
  import {
    Aim,
    ArrowLeft,
    CircleCheck,
    Document,
    DocumentCopy,
    FullScreen,
    Grid,
    Minus,
    Plus,
    RefreshLeft,
    RefreshRight
  } from '@element-plus/icons-vue'
  import { ElButton, ElIcon, ElTooltip } from 'element-plus'

  interface Props {
    mode: 'create' | 'edit'
    saving?: boolean
    validating?: boolean
    statusText: string
    statusType?: 'default' | 'warning' | 'danger'
    zoomText?: string
    materialsVisible?: boolean
    jsonVisible?: boolean
  }

  interface Emits {
    (e: 'back'): void
    (e: 'save'): void
    (e: 'validate'): void
    (e: 'fit-view'): void
    (e: 'open-meta'): void
    (e: 'undo'): void
    (e: 'redo'): void
    (e: 'zoom-in'): void
    (e: 'zoom-out'): void
    (e: 'center-content'): void
    (e: 'toggle-materials'): void
    (e: 'toggle-json'): void
  }

  const props = withDefaults(defineProps<Props>(), {
    saving: false,
    validating: false,
    statusType: 'default',
    zoomText: '100%',
    materialsVisible: true,
    jsonVisible: false
  })

  defineEmits<Emits>()

  const title = computed(() => {
    if (props.mode === 'create') return '新建工作流定义'
    return '编辑工作流定义'
  })
</script>

<style scoped lang="scss">
  .editor-toolbar {
    display: grid;
    flex: 0 0 auto;
    grid-template-columns: 36px minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    width: 100%;
    min-height: 44px;
  }

  .editor-toolbar__back,
  .editor-toolbar__tools,
  .editor-toolbar__actions {
    min-width: 0;
  }

  .editor-toolbar__tools {
    display: inline-flex;
    gap: 6px;
    align-items: center;
    justify-self: center;
  }

  .editor-toolbar__actions {
    display: inline-flex;
    gap: 12px;
    align-items: center;
  }

  .editor-toolbar__surface {
    display: inline-flex;
    gap: 6px;
    align-items: center;
    padding: 5px 6px;
    background: var(--workflow-overlay-bg, #f4f3ee);
    border: 1px solid var(--workflow-overlay-border, #34383a);
    border-radius: 8px;
    box-shadow: 0 8px 20px rgb(31 35 48 / 0.1);
  }

  .editor-toolbar__tools-group {
    display: inline-flex;
    gap: 4px;
    align-items: center;
  }

  .editor-toolbar__zoom-group {
    display: inline-flex;
    gap: 2px;
    align-items: center;
  }

  .editor-toolbar__meta {
    gap: 10px;
    padding: 6px 12px;
  }

  .editor-toolbar__page {
    font-size: 13px;
    font-weight: 600;
    color: var(--workflow-overlay-text, #24282a);
    white-space: nowrap;
  }

  .editor-toolbar__status {
    display: inline-flex;
    align-items: center;
    height: 28px;
    padding: 0 12px;
    font-size: 12px;
    font-weight: 600;
    color: var(--workflow-overlay-muted, #5d6467);
    white-space: nowrap;
    background: var(--workflow-overlay-soft-2, #deddd7);
    border-radius: 6px;
  }

  .editor-toolbar__status--warning {
    color: var(--el-color-warning);
    background: var(--el-color-warning-light-9);
  }

  .editor-toolbar__status--danger {
    color: var(--el-color-danger);
    background: var(--el-color-danger-light-9);
  }

  .editor-toolbar__icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    padding: 0;
    color: var(--workflow-overlay-text, #24282a);
    background: transparent;
    border-color: transparent;
    border-radius: 6px;
  }

  .editor-toolbar__icon-btn:hover {
    color: var(--workflow-overlay-text, #111315);
    background: var(--workflow-overlay-hover, #d8d7d1);
  }

  .editor-toolbar__icon-btn--active {
    color: var(--theme-color);
    background: var(--el-color-primary-light-9);
  }

  .editor-toolbar__divider {
    width: 1px;
    height: 18px;
    background: var(--workflow-overlay-border-soft, #a6aaab);
  }

  .editor-toolbar__zoom {
    min-width: 48px;
    font-size: 12px;
    font-weight: 700;
    color: var(--workflow-overlay-text, #111315);
    text-align: center;
  }

  .editor-toolbar__save {
    height: 44px;
    padding: 0 18px;
    color: #fff;
    background: var(--theme-color);
    border-color: var(--theme-color);
    border-radius: 7px;
    box-shadow: 0 8px 18px color-mix(in srgb, var(--theme-color) 20%, transparent);
  }

  @media (max-width: 980px) {
    .editor-toolbar {
      display: flex;
      gap: 6px;
      align-items: flex-start;
      overflow-x: auto;
      scrollbar-width: none;
    }

    .editor-toolbar::-webkit-scrollbar {
      display: none;
    }

    .editor-toolbar__back,
    .editor-toolbar__tools,
    .editor-toolbar__actions {
      flex: 0 0 auto;
    }

    .editor-toolbar__actions {
      margin-left: auto;
    }

    .editor-toolbar__meta,
    .editor-toolbar__divider {
      display: none;
    }
  }
</style>
