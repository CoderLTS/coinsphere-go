<!-- 平台级模型配置编辑框。 -->
<template>
  <ElDialog
    v-model="dialogVisible"
    :title="dialogTitle"
    width="min(680px, 94vw)"
    align-center
    destroy-on-close
  >
    <ElForm ref="formRef" :model="formData" :rules="rules" label-width="108px">
      <ElFormItem v-if="!isEdit" :label="t('aiConfig.form.preset')">
        <ElSelect
          v-model="selectedPresetKey"
          clearable
          :placeholder="t('aiConfig.form.presetPlaceholder')"
          @change="handlePresetChange"
        >
          <ElOption
            v-for="preset in modelPresets"
            :key="preset.key"
            :label="`${preset.provider} / ${preset.displayName}`"
            :value="preset.key"
          />
        </ElSelect>
      </ElFormItem>

      <ElRow :gutter="16">
        <ElCol :xs="24" :sm="12">
          <ElFormItem :label="t('aiConfig.form.displayName')" prop="displayName">
            <ElInput v-model.trim="formData.displayName" maxlength="120" />
          </ElFormItem>
        </ElCol>
        <ElCol :xs="24" :sm="12">
          <ElFormItem :label="t('aiConfig.form.modelName')" prop="modelName">
            <ElInput v-model.trim="formData.modelName" maxlength="255" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElFormItem :label="t('aiConfig.form.baseUrl')" prop="baseUrl">
        <ElInput v-model.trim="formData.baseUrl" placeholder="https://api.example.com/v1" />
      </ElFormItem>

      <ElFormItem :label="t('aiConfig.form.apiKey')" prop="apiKey">
        <ElInput
          v-model.trim="formData.apiKey"
          type="password"
          show-password
          :placeholder="apiKeyPlaceholder"
        />
      </ElFormItem>

      <ElRow :gutter="16">
        <ElCol :xs="24" :sm="12">
          <ElFormItem :label="t('aiConfig.form.priority')" prop="priority">
            <ElInputNumber
              v-model="formData.priority"
              :min="1"
              :max="9999"
              controls-position="right"
            />
          </ElFormItem>
        </ElCol>
        <ElCol :xs="24" :sm="12">
          <ElFormItem :label="t('aiConfig.form.timeoutMs')" prop="timeoutMs">
            <ElInputNumber
              v-model="formData.timeoutMs"
              :min="1000"
              :max="300000"
              controls-position="right"
            />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElFormItem :label="t('aiConfig.form.enabled')">
        <ElSwitch v-model="formData.isEnabled" />
      </ElFormItem>
    </ElForm>

    <template #footer>
      <ElButton @click="dialogVisible = false">{{ t('common.cancel') }}</ElButton>
      <ElButton type="primary" :loading="props.submitting" @click="handleSubmit">
        {{ t('common.confirm') }}
      </ElButton>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import type { FormInstance, FormRules } from 'element-plus'
  import { useI18n } from 'vue-i18n'
  import type { AiModelConfigItem, AiModelUpsertPayload } from '@/api/config'

  interface Props {
    visible: boolean
    modelData?: AiModelConfigItem | null
    submitting?: boolean
  }

  interface ModelPreset {
    key: string
    provider: string
    displayName: string
    modelName: string
    baseUrl: string
  }

  const modelPresets: ModelPreset[] = [
    {
      key: 'openai',
      provider: 'OpenAI',
      displayName: 'OpenAI GPT-4o-mini',
      modelName: 'gpt-4o-mini',
      baseUrl: 'https://api.openai.com/v1'
    },
    {
      key: 'deepseek',
      provider: 'DeepSeek',
      displayName: 'DeepSeek Chat',
      modelName: 'deepseek-chat',
      baseUrl: 'https://api.deepseek.com/v1'
    },
    {
      key: 'dashscope',
      provider: '阿里百炼',
      displayName: '通义千问 / DeepSeek',
      modelName: 'deepseek-v3.2-exp',
      baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1'
    },
    {
      key: 'zhipu',
      provider: '智谱 AI',
      displayName: 'GLM-4-Flash',
      modelName: 'glm-4-flash',
      baseUrl: 'https://open.bigmodel.cn/api/paas/v4'
    },
    {
      key: 'moonshot',
      provider: 'Moonshot',
      displayName: 'Kimi K2',
      modelName: 'kimi-k2-0711-preview',
      baseUrl: 'https://api.moonshot.cn/v1'
    },
    {
      key: 'openrouter',
      provider: 'OpenRouter',
      displayName: 'OpenRouter 自定义模型',
      modelName: 'openai/gpt-4o-mini',
      baseUrl: 'https://openrouter.ai/api/v1'
    },
    {
      key: 'siliconflow',
      provider: 'SiliconFlow',
      displayName: 'SiliconFlow 自定义模型',
      modelName: 'Qwen/Qwen2.5-72B-Instruct',
      baseUrl: 'https://api.siliconflow.cn/v1'
    },
    {
      key: 'custom',
      provider: 'OpenAI Compatible',
      displayName: '自定义兼容模型',
      modelName: '',
      baseUrl: ''
    }
  ]

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    modelData: null,
    submitting: false
  })
  const emit = defineEmits<{
    (event: 'update:visible', value: boolean): void
    (event: 'submit', payload: AiModelUpsertPayload): void
  }>()
  const { t } = useI18n()
  const formRef = ref<FormInstance>()
  const selectedPresetKey = ref('')

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })
  const isEdit = computed(() => Boolean(props.modelData))
  const dialogTitle = computed(() =>
    isEdit.value ? t('aiConfig.dialog.editTitle') : t('aiConfig.dialog.addTitle')
  )
  const apiKeyPlaceholder = computed(() =>
    isEdit.value
      ? t('aiConfig.form.apiKeyKeepHint', { masked: props.modelData?.apiKeyMasked || '' })
      : t('aiConfig.form.apiKeyPlaceholder')
  )

  const defaults = (): AiModelUpsertPayload => ({
    displayName: '',
    baseUrl: '',
    modelName: '',
    apiKey: '',
    isEnabled: true,
    priority: 100,
    timeoutMs: 60000
  })
  const formData = reactive<AiModelUpsertPayload>(defaults())
  const rules: FormRules = {
    displayName: [
      { required: true, message: t('aiConfig.validation.displayName'), trigger: 'blur' }
    ],
    modelName: [{ required: true, message: t('aiConfig.validation.modelName'), trigger: 'blur' }],
    baseUrl: [{ required: true, message: t('aiConfig.validation.baseUrl'), trigger: 'blur' }]
  }

  const loadForm = () => {
    Object.assign(formData, defaults())
    selectedPresetKey.value = ''
    if (props.modelData) {
      Object.assign(formData, {
        displayName: props.modelData.displayName,
        baseUrl: props.modelData.baseUrl,
        modelName: props.modelData.modelName,
        isEnabled: props.modelData.isEnabled,
        priority: props.modelData.priority,
        timeoutMs: props.modelData.timeoutMs
      })
    }
    nextTick(() => formRef.value?.clearValidate())
  }

  const handlePresetChange = (key?: string) => {
    const preset = modelPresets.find((item) => item.key === key)
    if (!preset) return
    Object.assign(formData, {
      displayName: preset.displayName,
      modelName: preset.modelName,
      baseUrl: preset.baseUrl
    })
  }

  watch(
    () => props.visible,
    (visible) => visible && loadForm()
  )

  const handleSubmit = async () => {
    if (!formRef.value || props.submitting) return
    await formRef.value.validate()
    emit('submit', {
      ...formData,
      apiKey: formData.apiKey?.trim() || undefined
    })
  }
</script>
