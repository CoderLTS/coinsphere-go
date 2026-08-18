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
    position: relative;
    width: 100%;
    min-height: 48px;
    pointer-events: none;
  }

  .editor-toolbar__back,
  .editor-toolbar__tools,
  .editor-toolbar__actions {
    position: absolute;
    top: 0;
    pointer-events: auto;
  }

  .editor-toolbar__back {
    left: 0;
  }

  .editor-toolbar__tools {
    left: 50%;
    display: inline-flex;
    gap: 6px;
    align-items: center;
    transform: translateX(-50%);
  }

  .editor-toolbar__actions {
    right: 0;
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
    border-radius: 2px;
    box-shadow: 0 8px 18px rgb(0 0 0 / 0.24);
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
    border-radius: 2px;
  }

  .editor-toolbar__status--warning {
    color: #111315;
    background: #eab24d;
  }

  .editor-toolbar__status--danger {
    color: #111315;
    background: #ff705b;
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
    border-radius: 2px;
  }

  .editor-toolbar__icon-btn:hover {
    color: var(--workflow-overlay-text, #111315);
    background: var(--workflow-overlay-hover, #d8d7d1);
  }

  .editor-toolbar__icon-btn--active {
    color: #111315;
    background: #c7f46b;
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
    color: #111315;
    background: #c7f46b;
    border-color: #c7f46b;
    border-radius: 2px;
    box-shadow: 0 8px 18px rgb(0 0 0 / 0.22);
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
      position: static;
      flex: 0 0 auto;
      transform: none;
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
