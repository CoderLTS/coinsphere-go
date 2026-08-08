<!-- 通知渠道配置页面或组件：notify-channel-dialog。 -->
<template>
  <ElDialog
    v-model="dialogVisible"
    class="notify-channel-dialog"
    :title="dialogTitle"
    width="min(760px, calc(100vw - 32px))"
    align-center
    destroy-on-close
  >
    <ElForm ref="formRef" :model="formData" :rules="rules" label-width="116px">
      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('notifyChannel.form.channelType')" prop="channelType">
            <ElSelect v-model="formData.channelType" @change="handleChannelTypeChange">
              <ElOption
                v-for="item in channelTypeOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </ElSelect>
          </ElFormItem>
        </ElCol>
        <ElCol :span="12">
          <ElFormItem :label="t('notifyChannel.form.displayName')" prop="displayName">
            <ElInput v-model.trim="formData.displayName" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElRow v-if="ownerOptions.length" :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('notifyChannel.form.owner')" prop="ownerId">
            <ElSelect v-model="formData.ownerId" filterable>
              <ElOption
                v-for="item in ownerOptions"
                :key="item.id"
                :label="item.label"
                :value="item.id"
              />
            </ElSelect>
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElFormItem v-if="currentChannelType" :label="t('notifyChannel.form.typeDescription')">
        <div class="type-description">
          <strong>{{ currentChannelType.label }}</strong>
          <p>{{ currentChannelType.description }}</p>
        </div>
      </ElFormItem>

      <template v-if="formData.channelType === 'dingtalk_webhook'">
        <ElRow :gutter="16">
          <ElCol :span="24">
            <ElFormItem :label="t('notifyChannel.form.webhookBaseUrl')" prop="webhookBaseUrl">
              <ElInput v-model.trim="formData.webhookBaseUrl" />
            </ElFormItem>
          </ElCol>
        </ElRow>
        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.accessToken')" prop="accessToken">
              <ElInput
                v-model.trim="formData.accessToken"
                type="password"
                show-password
                :placeholder="secretPlaceholder"
              />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.secret')" prop="secret">
              <ElInput
                v-model.trim="formData.secret"
                type="password"
                show-password
                :placeholder="optionalSecretPlaceholder"
              />
            </ElFormItem>
          </ElCol>
        </ElRow>
      </template>

      <template v-else-if="formData.channelType === 'qq_bot'">
        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.qqAppId')" prop="qqAppId">
              <ElInput v-model.trim="formData.qqAppId" />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.qqClientSecret')" prop="qqClientSecret">
              <ElInput
                v-model.trim="formData.qqClientSecret"
                type="password"
                show-password
                :placeholder="secretPlaceholder"
              />
            </ElFormItem>
          </ElCol>
        </ElRow>
        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.qqTargetType')" prop="qqTargetType">
              <ElSegmented
                v-model="formData.qqTargetType"
                :options="[
                  { label: t('notifyChannel.form.qqGroup'), value: 'group' },
                  { label: t('notifyChannel.form.qqChannel'), value: 'channel' }
                ]"
              />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.qqTargetId')" prop="qqTargetId">
              <ElInput v-model.trim="formData.qqTargetId" />
            </ElFormItem>
          </ElCol>
        </ElRow>
      </template>

      <template v-else-if="formData.channelType === 'smtp_email'">
        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.host')" prop="host">
              <ElInput v-model.trim="formData.host" />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.port')" prop="port">
              <ElInputNumber
                v-model="formData.port"
                :min="1"
                :max="65535"
                controls-position="right"
              />
            </ElFormItem>
          </ElCol>
        </ElRow>

        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.username')" prop="username">
              <ElInput v-model.trim="formData.username" />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.password')" prop="password">
              <ElInput
                v-model.trim="formData.password"
                type="password"
                show-password
                :placeholder="secretPlaceholder"
              />
            </ElFormItem>
          </ElCol>
        </ElRow>

        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.fromEmail')" prop="fromEmail">
              <ElInput v-model.trim="formData.fromEmail" />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem :label="t('notifyChannel.form.fromName')" prop="fromName">
              <ElInput v-model.trim="formData.fromName" />
            </ElFormItem>
          </ElCol>
        </ElRow>

        <ElFormItem :label="t('notifyChannel.form.recipients')" prop="recipientsText">
          <ElInput
            v-model="formData.recipientsText"
            type="textarea"
            :rows="3"
            :placeholder="t('notifyChannel.form.recipientsPlaceholder')"
          />
        </ElFormItem>

        <ElFormItem :label="t('notifyChannel.form.useTls')">
          <ElSwitch v-model="formData.useTls" />
        </ElFormItem>
      </template>

      <ElRow :gutter="16">
        <ElCol :span="12">
          <ElFormItem :label="t('notifyChannel.form.enabled')">
            <ElSwitch v-model="formData.isEnabled" />
          </ElFormItem>
        </ElCol>
      </ElRow>

      <ElFormItem :label="t('notifyChannel.form.remark')">
        <ElInput
          v-model="formData.remark"
          type="textarea"
          :rows="3"
          :placeholder="t('notifyChannel.form.remarkPlaceholder')"
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
  import { useI18n } from 'vue-i18n'
  import type {
    NotifyChannelItem,
    NotifyChannelMeta,
    NotifyChannelUpsertPayload
  } from '@/api/config'

  interface Props {
    visible: boolean
    meta: NotifyChannelMeta | null
    channelData?: NotifyChannelItem | null
    submitting?: boolean
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', payload: NotifyChannelUpsertPayload): void
  }

  interface ChannelFormState {
    channelType: 'dingtalk_webhook' | 'qq_bot' | 'smtp_email'
    displayName: string
    isEnabled: boolean
    ownerId: number | null
    webhookBaseUrl: string
    accessToken: string
    secret: string
    qqAppId: string
    qqClientSecret: string
    qqTargetType: 'group' | 'channel'
    qqTargetId: string
    host: string
    port: number
    username: string
    password: string
    fromEmail: string
    fromName: string
    recipientsText: string
    useTls: boolean
    remark: string
  }

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    meta: null,
    channelData: null,
    submitting: false
  })

  const emit = defineEmits<Emits>()
  const { t } = useI18n()

  const formRef = ref<FormInstance>()

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })

  const isEdit = computed(() => Boolean(props.channelData?.id))
  const dialogTitle = computed(() =>
    isEdit.value ? t('notifyChannel.dialog.editTitle') : t('notifyChannel.dialog.addTitle')
  )
  const secretPlaceholder = computed(() =>
    isEdit.value
      ? t('notifyChannel.form.secretKeepHint')
      : t('notifyChannel.form.secretPlaceholder')
  )
  const optionalSecretPlaceholder = computed(() =>
    isEdit.value
      ? t('notifyChannel.form.secretOptionalKeepHint')
      : t('notifyChannel.form.secretOptionalPlaceholder')
  )

  const channelTypeOptions = computed(() =>
    (props.meta?.channelTypes || []).filter((item) => item.value !== 'in_app')
  )
  const ownerOptions = computed(() => props.meta?.owners || [])
  const currentChannelType = computed(() =>
    channelTypeOptions.value.find((item) => item.value === formData.channelType)
  )

  const formData = reactive<ChannelFormState>({
    channelType: 'dingtalk_webhook',
    displayName: '',
    isEnabled: true,
    ownerId: null,
    webhookBaseUrl: 'https://oapi.dingtalk.com/robot/send',
    accessToken: '',
    secret: '',
    qqAppId: '',
    qqClientSecret: '',
    qqTargetType: 'group',
    qqTargetId: '',
    host: '',
    port: 465,
    username: '',
    password: '',
    fromEmail: '',
    fromName: '',
    recipientsText: '',
    useTls: true,
    remark: ''
  })

  const resetForm = () => {
    Object.assign(formData, {
      channelType: 'dingtalk_webhook',
      displayName: '',
      isEnabled: true,
      ownerId: ownerOptions.value[0]?.id ?? null,
      webhookBaseUrl: 'https://oapi.dingtalk.com/robot/send',
      accessToken: '',
      secret: '',
      qqAppId: '',
      qqClientSecret: '',
      qqTargetType: 'group',
      qqTargetId: '',
      host: '',
      port: 465,
      username: '',
      password: '',
      fromEmail: '',
      fromName: '',
      recipientsText: '',
      useTls: true,
      remark: ''
    })
  }

  const loadForm = () => {
    resetForm()
    if (!props.channelData) {
      return
    }

    let config: Record<string, any> = {}
    try {
      config = JSON.parse(props.channelData.settingsJson || '{}')
    } catch {
      config = {}
    }
    const channelType = ['dingtalk_webhook', 'qq_bot', 'smtp_email'].includes(
      props.channelData.channelType
    )
      ? (props.channelData.channelType as ChannelFormState['channelType'])
      : 'dingtalk_webhook'
    Object.assign(formData, {
      channelType,
      displayName: props.channelData.displayName,
      isEnabled: props.channelData.isEnabled,
      ownerId: props.channelData.ownerId ?? ownerOptions.value[0]?.id ?? null,
      webhookBaseUrl: config.webhookBaseUrl || 'https://oapi.dingtalk.com/robot/send',
      qqAppId: config.appId || '',
      qqTargetType: config.targetType === 'channel' ? 'channel' : 'group',
      qqTargetId: config.targetId || '',
      host: config.host || '',
      port: Number(config.port || 465),
      username: config.username || '',
      fromEmail: config.fromEmail || '',
      fromName: config.fromName || '',
      recipientsText: Array.isArray(config.recipients) ? config.recipients.join(', ') : '',
      useTls: Boolean(config.useTls ?? true),
      remark: props.channelData.remark || '',
      accessToken: '',
      secret: '',
      qqClientSecret: '',
      password: ''
    })
  }

  const rules = reactive<FormRules<ChannelFormState>>({
    channelType: [
      { required: true, message: t('notifyChannel.validation.channelType'), trigger: 'change' }
    ],
    displayName: [
      { required: true, message: t('notifyChannel.validation.displayName'), trigger: 'blur' }
    ],
    webhookBaseUrl: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'dingtalk_webhook' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.webhookBaseUrl')))
        },
        trigger: 'blur'
      }
    ],
    accessToken: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'dingtalk_webhook') {
            callback()
            return
          }
          if (String(value || '').trim() || isEdit.value) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.accessToken')))
        },
        trigger: 'blur'
      }
    ],
    qqAppId: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'qq_bot' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.qqAppId')))
        },
        trigger: 'blur'
      }
    ],
    qqClientSecret: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'qq_bot' || String(value || '').trim() || isEdit.value) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.qqClientSecret')))
        },
        trigger: 'blur'
      }
    ],
    qqTargetId: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'qq_bot' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.qqTargetId')))
        },
        trigger: 'blur'
      }
    ],
    host: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'smtp_email' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.host')))
        },
        trigger: 'blur'
      }
    ],
    username: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'smtp_email' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.username')))
        },
        trigger: 'blur'
      }
    ],
    password: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'smtp_email') {
            callback()
            return
          }
          if (String(value || '').trim() || isEdit.value) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.password')))
        },
        trigger: 'blur'
      }
    ],
    fromEmail: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'smtp_email' || String(value || '').trim()) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.fromEmail')))
        },
        trigger: 'blur'
      }
    ],
    recipientsText: [
      {
        validator: (_rule, value, callback) => {
          if (formData.channelType !== 'smtp_email') {
            callback()
            return
          }
          const normalized = String(value || '')
            .replace(/\r?\n/g, ',')
            .split(',')
            .map((item) => item.trim())
            .filter(Boolean)
          if (normalized.length) {
            callback()
            return
          }
          callback(new Error(t('notifyChannel.validation.recipients')))
        },
        trigger: 'blur'
      }
    ]
  })

  const handleChannelTypeChange = () => {
    if (formData.channelType === 'dingtalk_webhook') {
      formData.webhookBaseUrl ||= 'https://oapi.dingtalk.com/robot/send'
    } else if (formData.channelType === 'qq_bot') {
      formData.qqTargetType ||= 'group'
    } else if (formData.channelType === 'smtp_email') {
      formData.port ||= 465
      formData.useTls = true
    }
    formRef.value?.clearValidate()
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
    let config: Record<string, any>
    let secrets: Record<string, string>
    if (formData.channelType === 'dingtalk_webhook') {
      config = { webhookBaseUrl: formData.webhookBaseUrl.trim() }
      secrets = {
        ...(formData.accessToken.trim() ? { accessToken: formData.accessToken.trim() } : {}),
        ...(formData.secret.trim() ? { secret: formData.secret.trim() } : {})
      }
    } else if (formData.channelType === 'qq_bot') {
      config = {
        appId: formData.qqAppId.trim(),
        targetType: formData.qqTargetType,
        targetId: formData.qqTargetId.trim()
      }
      secrets = formData.qqClientSecret.trim()
        ? { clientSecret: formData.qqClientSecret.trim() }
        : {}
    } else {
      config = {
        host: formData.host.trim(),
        port: formData.port,
        username: formData.username.trim(),
        fromEmail: formData.fromEmail.trim(),
        fromName: formData.fromName.trim(),
        recipients: formData.recipientsText
          .replace(/\r?\n/g, ',')
          .split(',')
          .map((item) => item.trim())
          .filter(Boolean),
        useTls: formData.useTls
      }
      secrets = formData.password.trim() ? { password: formData.password.trim() } : {}
    }

    emit('submit', {
      channelType: formData.channelType,
      displayName: formData.displayName.trim(),
      isEnabled: formData.isEnabled,
      ownerId: formData.ownerId,
      settingsJson: JSON.stringify(config),
      ...(!isEdit.value || Object.keys(secrets).length
        ? { secretJson: JSON.stringify(secrets) }
        : {}),
      remark: formData.remark.trim()
    })
  }
</script>

<style scoped lang="scss">
  .type-description {
    padding: 12px 14px;
    background: rgb(148 163 184 / 0.08);
    border-radius: 14px;

    strong {
      display: block;
      color: var(--el-text-color-primary);
    }

    p {
      margin: 6px 0 0;
      line-height: 1.7;
      color: var(--el-text-color-secondary);
    }
  }

  @media (max-width: 640px) {
    :global(.notify-channel-dialog) {
      display: flex;
      flex-direction: column;
      width: calc(100vw - 24px) !important;
      max-height: calc(100dvh - 24px);
      margin: 12px auto;
    }

    :global(.notify-channel-dialog .el-dialog__body) {
      min-height: 0;
      padding-right: 16px;
      padding-left: 16px;
      overflow-y: auto;
    }

    :global(.notify-channel-dialog .el-dialog__header),
    :global(.notify-channel-dialog .el-dialog__footer) {
      flex: 0 0 auto;
    }

    :global(.notify-channel-dialog .el-col-12) {
      flex: 0 0 100%;
      max-width: 100%;
    }

    :global(.notify-channel-dialog .el-form-item) {
      display: block;
    }

    :global(.notify-channel-dialog .el-form-item__label) {
      width: auto !important;
      height: auto;
      margin-bottom: 8px;
      line-height: 20px;
    }

    :global(.notify-channel-dialog .el-input-number),
    :global(.notify-channel-dialog .el-segmented),
    :global(.notify-channel-dialog .el-select) {
      width: 100%;
    }
  }
</style>
