<template>
  <div ref="editorHost" class="quant-code-editor" />
</template>

<script setup lang="ts">
  import * as monaco from 'monaco-editor/editor/editor.api.js'
  import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'

  interface Props {
    modelValue: string
    config: Record<string, any>
  }

  const props = defineProps<Props>()
  const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()
  const editorHost = ref<HTMLElement>()
  let editor: monaco.editor.IStandaloneCodeEditor | null = null
  let completionProvider: monaco.IDisposable | null = null

  globalThis.MonacoEnvironment = { getWorker: () => new EditorWorker() }

  if (!monaco.languages.getLanguages().some((language) => language.id === 'cel')) {
    monaco.languages.register({ id: 'cel' })
    monaco.languages.setMonarchTokensProvider('cel', {
      keywords: ['true', 'false', 'null', 'in'],
      tokenizer: {
        root: [
          [/[A-Za-z_][\w]*/, { cases: { '@keywords': 'keyword', '@default': 'identifier' } }],
          [/-?\d+(?:\.\d+)?/, 'number'],
          [/"(?:[^"\\]|\\.)*"/, 'string'],
          [/[{}()[\]]/, '@brackets'],
          [/[,:.?]/, 'delimiter']
        ]
      }
    })
  }

  const suggestions = () => {
    const aliases = Array.isArray(props.config.series)
      ? props.config.series.map((item: any) => String(item?.alias || '')).filter(Boolean)
      : []
    const parameters = Object.keys(
      props.config.parameters && typeof props.config.parameters === 'object'
        ? props.config.parameters
        : {}
    )
    return [
      ...[
        'decimalAdd',
        'decimalSub',
        'decimalMul',
        'decimalDiv',
        'decimalGt',
        'decimalGte',
        'decimalLt',
        'decimalLte',
        'decimalEq',
        'sma',
        'last'
      ].map((label) => ({
        label,
        insertText: `${label}($0)`,
        kind: monaco.languages.CompletionItemKind.Function,
        insertTextRules: monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet
      })),
      ...aliases.map((alias: string) => ({
        label: `ohlcv.${alias}`,
        insertText: `ohlcv.${alias}`,
        kind: monaco.languages.CompletionItemKind.Variable
      })),
      ...parameters.map((name) => ({
        label: `params.${name}`,
        insertText: `params.${name}`,
        kind: monaco.languages.CompletionItemKind.Variable
      }))
    ]
  }

  onMounted(() => {
    editor = monaco.editor.create(editorHost.value!, {
      value: props.modelValue || '',
      language: 'cel',
      theme: 'vs',
      minimap: { enabled: false },
      automaticLayout: true,
      fontSize: 13,
      lineNumbers: 'on',
      scrollBeyondLastLine: false,
      tabSize: 2
    })
    editor.onDidChangeModelContent(() => emit('update:modelValue', editor?.getValue() || ''))
    completionProvider = monaco.languages.registerCompletionItemProvider('cel', {
      provideCompletionItems: (model, position) => {
        const word = model.getWordUntilPosition(position)
        const range = new monaco.Range(
          position.lineNumber,
          word.startColumn,
          position.lineNumber,
          word.endColumn
        )
        return { suggestions: suggestions().map((item) => ({ ...item, range })) }
      }
    })
  })

  watch(
    () => props.modelValue,
    (value) => {
      if (editor && editor.getValue() !== value) editor.setValue(value || '')
    }
  )

  onBeforeUnmount(() => {
    completionProvider?.dispose()
    editor?.dispose()
  })
</script>

<style scoped>
  .quant-code-editor {
    width: 100%;
    height: 280px;
    overflow: hidden;
    border: 1px solid var(--el-border-color);
    border-radius: 4px;
  }
</style>
