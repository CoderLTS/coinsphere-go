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
            :class="['editor-toolbar__icon-btn', { 'editor-toolbar__icon-btn--active': materialsVisible }]"
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
            :class="['editor-toolbar__icon-btn', { 'editor-toolbar__icon-btn--active': jsonVisible }]"
            @click="$emit('toggle-json')"
          >
            <ElIcon><DocumentCopy /></ElIcon>
          </ElButton>
        </ElTooltip>

        <ElTooltip content="校验" placement="bottom">
          <ElButton class="editor-toolbar__icon-btn" :loading="validating" @click="$emit('validate')">
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

      <ElButton type="primary" class="editor-toolbar__save" :loading="saving" @click="$emit('save')">
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
    min-height: 52px;
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
    transform: translateX(-50%);
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }

  .editor-toolbar__actions {
    right: 0;
    display: inline-flex;
    align-items: center;
    gap: 12px;
  }

  .editor-toolbar__surface {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 5px 6px;
    border: 1px solid rgba(206, 212, 226, 0.9);
    border-radius: 16px;
    background: rgba(255, 255, 255, 0.96);
    box-shadow: 0 10px 24px rgba(15, 23, 42, 0.08);
    backdrop-filter: blur(12px);
  }

  .editor-toolbar__tools-group {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  .editor-toolbar__zoom-group {
    display: inline-flex;
    align-items: center;
    gap: 2px;
  }

  .editor-toolbar__meta {
    gap: 10px;
    padding: 6px 12px;
  }

  .editor-toolbar__page {
    font-size: 13px;
    font-weight: 600;
    color: #334155;
    white-space: nowrap;
  }

  .editor-toolbar__status {
    display: inline-flex;
    align-items: center;
    height: 28px;
    padding: 0 12px;
    border-radius: 999px;
    background: #f5f5fb;
    color: #667085;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }

  .editor-toolbar__status--warning {
    background: #fff3e8;
    color: #c2410c;
  }

  .editor-toolbar__status--danger {
    background: #fef2f2;
    color: #dc2626;
  }

  .editor-toolbar__icon-btn {
    width: 34px;
    height: 34px;
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 10px;
    border-color: transparent;
    background: transparent;
    color: #334155;
  }

  .editor-toolbar__icon-btn:hover {
    background: #f3f4fb;
    color: #1f3fb7;
  }

  .editor-toolbar__icon-btn--active {
    background: #eef4ff;
    color: #1f3fb7;
  }

  .editor-toolbar__divider {
    width: 1px;
    height: 18px;
    background: rgba(203, 213, 225, 0.92);
  }

  .editor-toolbar__zoom {
    min-width: 48px;
    text-align: center;
    font-size: 12px;
    font-weight: 700;
    color: #0f172a;
  }

  .editor-toolbar__save {
    height: 44px;
    padding: 0 18px;
    border-radius: 14px;
    box-shadow: 0 12px 24px rgba(99, 102, 241, 0.22);
  }
</style>
