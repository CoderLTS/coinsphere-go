<template><div ref="host" class="workflow-code-field" /></template>

<script setup lang="ts">
  import * as monaco from 'monaco-editor/editor/editor.api.js'
  import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'
  import 'monaco-editor/languages/definitions/python/register.js'

  const props = withDefaults(defineProps<{ modelValue: string; language?: string }>(), {
    language: 'plaintext'
  })
  const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()
  const host = ref<HTMLElement>()
  let editor: monaco.editor.IStandaloneCodeEditor | null = null

  globalThis.MonacoEnvironment = { getWorker: () => new EditorWorker() }

  onMounted(() => {
    editor = monaco.editor.create(host.value!, {
      value: props.modelValue || '',
      language: props.language,
      minimap: { enabled: false },
      automaticLayout: true,
      fontSize: 13,
      lineNumbersMinChars: 3,
      scrollBeyondLastLine: false,
      tabSize: 4
    })
    editor.onDidChangeModelContent(() => emit('update:modelValue', editor?.getValue() || ''))
  })

  watch(
    () => props.modelValue,
    (value) => {
      if (editor && editor.getValue() !== value) editor.setValue(value || '')
    }
  )

  onBeforeUnmount(() => editor?.dispose())
</script>

<style scoped>
  .workflow-code-field {
    width: 100%;
    height: 220px;
    overflow: hidden;
    border: 1px solid var(--el-border-color);
    border-radius: 4px;
  }
</style>
