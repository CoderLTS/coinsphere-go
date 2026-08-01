<!-- AI 模型配置页面或组件：ai-model-dialog。 -->
<template>
  <ElDialog
    v-model="dialogVisible"
    :title="dialogTitle"
    width="760px"
    align-center
    destroy-on-close
  >
    <ElForm ref="formRef" :model="formData" :rules="rules" label-width="108px">
      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.providerType')" prop="provider">
            <ElSelect v-model="formData.provider" @change="handleProviderTypeChange">
              <ElOption
                v-for="item in providerTypeOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </ElSelect>
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.preset')">
            <ElSelect
              v-model="selectedPresetKey"
              clearable
              :placeholder="t('aiConfig.form.presetPlaceholder')"
              @change="handlePresetChange"
            >
              <ElOption
                v-for="item in currentPresets"
                :key="buildPresetKey(item)"
                :label="`${item.providerName} / ${item.displayName}`"
                :value="buildPresetKey(item)"
              />
            </ElSelect>
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.providerLabel')" prop="providerName">
            <ElInput v-model.trim="formData.providerName" />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.displayName')" prop="displayName">
            <ElInput v-model.trim="formData.displayName" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.modelName')" prop="modelIdentifier">
            <ElInput v-model.trim="formData.modelIdentifier" />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.baseUrl')" prop="baseUrl">
            <ElInput v-model.trim="formData.baseUrl" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.apiKey')" prop="apiKey">
            <ElInput
              v-model.trim="formData.apiKey"
              type="password"
              show-password
              :placeholder="apiKeyPlaceholder"
            />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
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

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.priority')" prop="priority">
            <ElInputNumber
              v-model="formData.priority"
              :min="1"
              :max="9999"
              controls-position="right"
            />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('aiConfig.form.enabled')">
            <ElSwitch v-model="formData.isEnabled" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <template v-if="formData.provider === 'openai_compatible'">
        <ElFormItem :label="t('aiConfig.form.headersJson')" prop="requestHeadersJson">
          <ElInput
            v-model="formData.requestHeadersJson"
            type="textarea"
            :rows="4"
            :placeholder="t('aiConfig.form.headersJsonPlaceholder')"
          />
        </ElFormItem>
        <ElFormItem :label="t('aiConfig.form.extraBodyJson')" prop="requestBodyJson">
          <ElInput
            v-model="formData.requestBodyJson"
            type="textarea"
            :rows="4"
            :placeholder="t('aiConfig.form.extraBodyJsonPlaceholder')"
          />
        </ElFormItem>
      </template>

      <ElFormItem :label="t('aiConfig.form.remark')" prop="remark">
        <ElInput
          v-model="formData.remark"
          type="textarea"
          :rows="3"
          :placeholder="t('aiConfig.form.remarkPlaceholder')"
        />
      </ElFormItem>
    </ElForm>

    <template #footer>
      <div class="dialog-footer">
        <ElButton @click="dialogVisible = false">{{ t('common.cancel') }}</ElButton>
        <ElButton type="primary" :loading="props.submitting" @click="handleSubmit">
          {{ t('common.confirm') }}
        </ElButton>
      </div>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import type { FormInstance, FormRules } from 'element-plus'
  import type { AiModelConfigItem, AiModelUpsertPayload, AiProviderMeta } from '@/api/config'
  import { useI18n } from 'vue-i18n'

  interface Props {
    visible: boolean
    meta: AiProviderMeta | null
    modelData?: AiModelConfigItem | null
    submitting?: boolean
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', payload: AiModelUpsertPayload): void
  }

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    meta: null,
    modelData: null,
    submitting: false
  })

  const emit = defineEmits<Emits>()
  const { t } = useI18n()

  const formRef = ref<FormInstance>()
  const selectedPresetKey = ref('')

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })

  const isEdit = computed(() => Boolean(props.modelData?.id))
  const dialogTitle = computed(() =>
    isEdit.value ? t('aiConfig.dialog.editTitle') : t('aiConfig.dialog.addTitle')
  )
  const apiKeyPlaceholder = computed(() =>
    isEdit.value ? t('aiConfig.form.apiKeyKeepHint') : t('aiConfig.form.apiKeyPlaceholder')
  )
  const providerTypeOptions = computed(() => props.meta?.providerOptions || [])
  const currentPresets = computed(() =>
    (props.meta?.presets || []).filter((item) => item.provider === formData.provider)
  )

  const formData = reactive<AiModelUpsertPayload>({
    provider: 'openai_compatible',
    providerName: '',
    displayName: '',
    modelIdentifier: '',
    baseUrl: '',
    apiKey: '',
    isEnabled: true,
    priority: 100,
    requestHeadersJson: '{}',
    requestBodyJson: '{}',
    timeoutMs: 60000,
    remark: ''
  })

  const validateJson = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
    try {
      const parsed = JSON.parse(value || '{}')
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        callback()
        return
      }
      callback(new Error(t('aiConfig.validation.jsonObject')))
    } catch {
      callback(new Error(t('aiConfig.validation.jsonInvalid')))
    }
  }

  const rules = reactive<FormRules>({
    provider: [
      { required: true, message: t('aiConfig.validation.providerType'), trigger: 'change' }
    ],
    providerName: [
      { required: true, message: t('aiConfig.validation.providerLabel'), trigger: 'blur' }
    ],
    displayName: [
      { required: true, message: t('aiConfig.validation.displayName'), trigger: 'blur' }
    ],
    modelIdentifier: [
      { required: true, message: t('aiConfig.validation.modelName'), trigger: 'blur' }
    ],
    baseUrl: [{ required: true, message: t('aiConfig.validation.baseUrl'), trigger: 'blur' }],
    apiKey: [
      {
        validator: (_rule, value, callback) => {
          if (isEdit.value || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('aiConfig.validation.apiKey')))
        },
        trigger: 'blur'
      }
    ],
    requestHeadersJson: [{ validator: validateJson, trigger: 'blur' }],
    requestBodyJson: [{ validator: validateJson, trigger: 'blur' }]
  })

  const resetForm = () => {
    Object.assign(formData, {
      provider: 'openai_compatible',
      providerName: '',
      displayName: '',
      modelIdentifier: '',
      baseUrl: '',
      apiKey: '',
      isEnabled: true,
      priority: 100,
      requestHeadersJson: '{}',
      requestBodyJson: '{}',
      timeoutMs: 60000,
      remark: ''
    })
    selectedPresetKey.value = ''
  }

  const loadForm = () => {
    resetForm()
    if (!props.modelData) {
      return
    }
    Object.assign(formData, {
      provider: props.modelData.provider,
      providerName: props.modelData.providerName,
      displayName: props.modelData.displayName,
      modelIdentifier: props.modelData.modelIdentifier,
      baseUrl: props.modelData.baseUrl,
      apiKey: '',
      isEnabled: props.modelData.isEnabled,
      priority: props.modelData.priority,
      requestHeadersJson: props.modelData.requestHeadersJson || '{}',
      requestBodyJson: props.modelData.requestBodyJson || '{}',
      timeoutMs: props.modelData.timeoutMs,
      remark: props.modelData.remark || ''
    })
  }

  const buildPresetKey = (item: AiProviderMeta['presets'][number]) =>
    `${item.provider}::${item.providerName}::${item.modelIdentifier}`

  const handleProviderTypeChange = () => {
    selectedPresetKey.value = ''
    if (formData.provider !== 'openai_compatible') {
      formData.requestHeadersJson = '{}'
      formData.requestBodyJson = '{}'
    }
  }

  const handlePresetChange = (value?: string) => {
    if (!value) {
      return
    }
    const preset = currentPresets.value.find((item) => buildPresetKey(item) === value)
    if (!preset) {
      return
    }
    Object.assign(formData, {
      providerName: preset.providerName,
      displayName: preset.displayName,
      modelIdentifier: preset.modelIdentifier,
      baseUrl: preset.baseUrl,
      requestHeadersJson: preset.requestHeadersJson || '{}',
      requestBodyJson: preset.requestBodyJson || '{}'
    })
  }

  watch(
    () => props.visible,
    (visible) => {
      if (!visible) {
        return
      }
      loadForm()
      nextTick(() => {
        formRef.value?.clearValidate()
      })
    }
  )

  const handleSubmit = async () => {
    if (!formRef.value || props.submitting) {
      return
    }
    await formRef.value.validate()
    emit('submit', {
      provider: formData.provider,
      providerName: formData.providerName,
      displayName: formData.displayName,
      modelIdentifier: formData.modelIdentifier,
      baseUrl: formData.baseUrl,
      apiKey: formData.apiKey?.trim() || undefined,
      isEnabled: formData.isEnabled,
      priority: formData.priority,
      requestHeadersJson: formData.requestHeadersJson || '{}',
      requestBodyJson: formData.requestBodyJson || '{}',
      timeoutMs: formData.timeoutMs,
      remark: formData.remark || ''
    })
  }
</script>
