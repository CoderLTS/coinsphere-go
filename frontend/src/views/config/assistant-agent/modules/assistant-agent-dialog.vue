<!-- 助手代理配置页面或组件：assistant-agent-dialog。 -->
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
          <ElFormItem :label="t('assistantAgent.form.code')" prop="code">
            <ElInput v-model.trim="formData.code" :disabled="isEdit" />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('assistantAgent.form.displayName')" prop="displayName">
            <ElInput v-model.trim="formData.displayName" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('assistantAgent.form.dataSourceType')" prop="dataSourceType">
            <ElSelect v-model="formData.dataSourceType">
              <ElOption
                v-for="item in dataSourceOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </ElSelect>
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('assistantAgent.form.avatar')" prop="avatar">
            <div class="avatar-editor">
              <ElImage
                class="avatar-editor__preview"
                :src="formData.avatar || defaultAssistantAvatar"
                :preview-src-list="[formData.avatar || defaultAssistantAvatar]"
                preview-teleported
                fit="cover"
              />
              <div class="avatar-editor__actions">
                <ElUpload
                  :auto-upload="false"
                  :show-file-list="false"
                  accept="image/jpeg,image/png,image/webp,image/gif"
                  :disabled="uploadingAvatar"
                  @change="handleAvatarChange"
                >
                  <ElButton :loading="uploadingAvatar">{{
                    t('assistantAgent.form.uploadAvatar')
                  }}</ElButton>
                </ElUpload>
                <ElButton text :disabled="uploadingAvatar || !formData.avatar" @click="clearAvatar">
                  {{ t('assistantAgent.form.restoreDefaultAvatar') }}
                </ElButton>
                <div class="avatar-editor__tip">{{ t('assistantAgent.form.avatarUploadTip') }}</div>
              </div>
            </div>
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('assistantAgent.form.sort')" prop="sort">
            <ElInputNumber v-model="formData.sort" :min="0" :max="9999" controls-position="right" />
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('assistantAgent.form.enabled')">
            <ElSwitch v-model="formData.isEnabled" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElFormItem :label="t('assistantAgent.form.description')" prop="description">
        <ElInput
          v-model="formData.description"
          type="textarea"
          :rows="3"
          :placeholder="t('assistantAgent.form.descriptionPlaceholder')"
        />
      </ElFormItem>

      <ElFormItem :label="t('assistantAgent.form.welcomeMessage')" prop="welcomeMessage">
        <ElInput
          v-model="formData.welcomeMessage"
          type="textarea"
          :rows="3"
          :placeholder="t('assistantAgent.form.welcomeMessagePlaceholder')"
        />
      </ElFormItem>

      <ElFormItem :label="t('assistantAgent.form.systemPrompt')" prop="systemPrompt">
        <ElInput
          v-model="formData.systemPrompt"
          type="textarea"
          :rows="7"
          :placeholder="t('assistantAgent.form.systemPromptPlaceholder')"
        />
      </ElFormItem>

      <ElFormItem :label="t('assistantAgent.form.starterPrompts')" prop="starterPromptsText">
        <ElInput
          v-model="starterPromptsText"
          type="textarea"
          :rows="5"
          :placeholder="t('assistantAgent.form.starterPromptsPlaceholder')"
        />
      </ElFormItem>
    </ElForm>

    <template #footer>
      <div class="dialog-footer">
        <ElButton @click="dialogVisible = false">{{ t('common.cancel') }}</ElButton>
        <ElButton type="primary" :loading="props.submitting" @click="handleSubmit">{{
          t('common.confirm')
        }}</ElButton>
      </div>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import { ElMessage, type FormInstance, type FormRules, type UploadFile } from 'element-plus'
  import defaultAssistantAvatar from '@/assets/images/avatar/avatar10.webp'
  import type {
    AssistantAgentItem,
    AssistantAgentMeta,
    AssistantAgentUpsertPayload
  } from '@/api/config'
  import { fetchUploadAvatarAsset } from '@/api/system'
  import { useI18n } from 'vue-i18n'

  interface Props {
    visible: boolean
    meta: AssistantAgentMeta | null
    agentData?: AssistantAgentItem | null
    submitting?: boolean
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', payload: AssistantAgentUpsertPayload): void
  }

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    meta: null,
    agentData: null,
    submitting: false
  })

  const emit = defineEmits<Emits>()
  const { t } = useI18n()

  const formRef = ref<FormInstance>()
  const starterPromptsText = ref('')
  const uploadingAvatar = ref(false)

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })

  const isEdit = computed(() => Boolean(props.agentData?.id))
  const dialogTitle = computed(() =>
    isEdit.value ? t('assistantAgent.dialog.editTitle') : t('assistantAgent.dialog.addTitle')
  )
  const dataSourceOptions = computed(() => props.meta?.dataSourceOptions || [])

  const formData = reactive<AssistantAgentUpsertPayload>({
    code: '',
    displayName: '',
    avatar: '',
    description: '',
    systemPrompt: '',
    welcomeMessage: '',
    starterPrompts: [],
    dataSourceType: 'system_context',
    isEnabled: true,
    sort: 100
  })

  const rules = reactive<FormRules>({
    code: [{ required: true, message: t('assistantAgent.validation.code'), trigger: 'blur' }],
    displayName: [
      { required: true, message: t('assistantAgent.validation.displayName'), trigger: 'blur' }
    ],
    dataSourceType: [
      { required: true, message: t('assistantAgent.validation.dataSourceType'), trigger: 'change' }
    ],
    systemPrompt: [
      { required: true, message: t('assistantAgent.validation.systemPrompt'), trigger: 'blur' }
    ]
  })

  const resetForm = () => {
    Object.assign(formData, {
      code: '',
      displayName: '',
      avatar: '',
      description: '',
      systemPrompt: '',
      welcomeMessage: '',
      starterPrompts: [],
      dataSourceType: 'system_context',
      isEnabled: true,
      sort: 100
    })
    starterPromptsText.value = ''
  }

  const loadForm = () => {
    resetForm()
    if (!props.agentData) return
    Object.assign(formData, {
      code: props.agentData.code,
      displayName: props.agentData.displayName,
      avatar: props.agentData.avatar,
      description: props.agentData.description,
      systemPrompt: props.agentData.systemPrompt,
      welcomeMessage: props.agentData.welcomeMessage,
      starterPrompts: [...props.agentData.starterPrompts],
      dataSourceType: props.agentData.dataSourceType,
      isEnabled: props.agentData.isEnabled,
      sort: props.agentData.sort
    })
    starterPromptsText.value = props.agentData.starterPrompts.join('\n')
  }

  watch(
    () => props.visible,
    (visible) => {
      if (!visible) return
      loadForm()
      nextTick(() => {
        formRef.value?.clearValidate()
      })
    }
  )

  const handleAvatarChange = async (uploadFile: UploadFile) => {
    if (!uploadFile.raw) return

    const allowedTypes = ['image/jpeg', 'image/png', 'image/webp', 'image/gif']
    if (!allowedTypes.includes(uploadFile.raw.type)) {
      ElMessage.error(t('assistantAgent.form.avatarFormatError'))
      return
    }
    if (uploadFile.raw.size > 2 * 1024 * 1024) {
      ElMessage.error(t('assistantAgent.form.avatarSizeError'))
      return
    }

    uploadingAvatar.value = true
    try {
      const data = await fetchUploadAvatarAsset(uploadFile.raw)
      formData.avatar = data.url
    } finally {
      uploadingAvatar.value = false
    }
  }

  const clearAvatar = () => {
    formData.avatar = ''
  }

  const handleSubmit = async () => {
    if (!formRef.value || props.submitting) return
    await formRef.value.validate()
    emit('submit', {
      ...formData,
      code: formData.code.trim(),
      displayName: formData.displayName.trim(),
      avatar: formData.avatar.trim(),
      description: formData.description.trim(),
      welcomeMessage: formData.welcomeMessage.trim(),
      systemPrompt: formData.systemPrompt.trim(),
      starterPrompts: starterPromptsText.value
        .split(/\r?\n/)
        .map((item) => item.trim())
        .filter(Boolean)
    })
  }
</script>

<style scoped lang="scss">
  .avatar-editor {
    display: flex;
    align-items: center;
    gap: 14px;
  }

  .avatar-editor__preview {
    width: 72px;
    height: 72px;
    overflow: hidden;
    border: 1px solid rgba(148, 163, 184, 0.24);
    border-radius: 18px;
    background: rgba(248, 250, 252, 0.92);
    flex-shrink: 0;
  }

  .avatar-editor__actions {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }

  .avatar-editor__tip {
    font-size: 12px;
    color: var(--el-text-color-secondary);
    line-height: 1.5;
  }
</style>
