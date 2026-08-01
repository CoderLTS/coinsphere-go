<!-- 新闻数据管理页面或组件：news-dialog。 -->
<template>
  <ElDialog
    v-model="dialogVisible"
    :title="dialogType === 'add' ? t('data.news.dialog.addTitle') : t('data.news.dialog.editTitle')"
    width="720px"
    align-center
  >
    <ElForm ref="formRef" :model="formData" :rules="rules" label-width="96px">
      <ElFormItem :label="t('data.news.form.title')" prop="title">
        <ElInput
          v-model.trim="formData.title"
          :placeholder="t('data.news.form.titlePlaceholder')"
        />
      </ElFormItem>
      <ElFormItem :label="t('data.news.form.publishTime')" prop="publishedAt">
        <ElDatePicker
          v-model="formData.publishedAt"
          type="datetime"
          value-format="YYYY-MM-DD HH:mm:ss"
          format="YYYY-MM-DD HH:mm:ss"
          :placeholder="t('data.news.form.publishTimePlaceholder')"
          class="date-picker"
        />
      </ElFormItem>
      <ElFormItem :label="t('data.news.form.sourceUrl')" prop="sourceUrl">
        <ElInput
          v-model.trim="formData.sourceUrl"
          :placeholder="t('data.news.form.sourceUrlPlaceholder')"
        />
      </ElFormItem>
      <ElFormItem :label="t('data.news.form.link')" prop="originalUrl">
        <ElInput
          v-model.trim="formData.originalUrl"
          :placeholder="t('data.news.form.linkPlaceholder')"
        />
      </ElFormItem>
      <ElFormItem :label="t('data.news.form.picture')" prop="imageUrl">
        <ElInput
          v-model.trim="formData.imageUrl"
          :placeholder="t('data.news.form.picturePlaceholder')"
        />
      </ElFormItem>
      <div v-if="formData.imageUrl" class="picture-preview">
        <ElImage
          :src="formData.imageUrl"
          fit="cover"
          :preview-src-list="[formData.imageUrl]"
          preview-teleported
        />
      </div>
      <ElFormItem :label="t('data.news.form.content')" prop="content">
        <ElInput
          v-model.trim="formData.content"
          type="textarea"
          :rows="8"
          :placeholder="t('data.news.form.contentPlaceholder')"
        />
      </ElFormItem>
    </ElForm>

    <template #footer>
      <div class="dialog-footer">
        <ElButton @click="dialogVisible = false">{{ t('common.cancel') }}</ElButton>
        <ElButton type="primary" :loading="submitting" @click="handleSubmit">
          {{ t('common.confirm') }}
        </ElButton>
      </div>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import type { FormInstance, FormRules } from 'element-plus'
  import { useI18n } from 'vue-i18n'

  export interface NewsFormPayload {
    title: string
    content: string
    sourceUrl: string
    originalUrl: string
    imageUrl: string
    publishedAt: string
  }

  interface Props {
    visible: boolean
    type: 'add' | 'edit'
    newsData?: Partial<Api.Data.NewsListItem> | null
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', payload: NewsFormPayload): void
  }

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    type: 'add',
    newsData: null
  })

  const emit = defineEmits<Emits>()
  const { t } = useI18n()

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })
  const dialogType = computed(() => props.type)
  const submitting = ref(false)
  const formRef = ref<FormInstance>()

  const formData = reactive<NewsFormPayload>({
    title: '',
    content: '',
    sourceUrl: '',
    originalUrl: '',
    imageUrl: '',
    publishedAt: ''
  })

  const rules = reactive<FormRules>({
    title: [{ required: true, message: t('data.news.validation.title'), trigger: 'blur' }],
    content: [{ required: true, message: t('data.news.validation.content'), trigger: 'blur' }],
    publishedAt: [
      { required: true, message: t('data.news.validation.publishTime'), trigger: 'change' }
    ]
  })

  const createCurrentDateTime = () => {
    const now = new Date()
    const pad = (value: number) => String(value).padStart(2, '0')
    return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`
  }

  const resetFormData = () => {
    Object.assign(formData, {
      title: '',
      content: '',
      sourceUrl: '',
      originalUrl: '',
      imageUrl: '',
      publishedAt: createCurrentDateTime()
    })
  }

  const loadFormData = () => {
    if (dialogType.value === 'add' || !props.newsData) {
      resetFormData()
      return
    }

    Object.assign(formData, {
      title: props.newsData.title || '',
      content: props.newsData.content || '',
      sourceUrl: props.newsData.sourceUrl || '',
      originalUrl: props.newsData.originalUrl || '',
      imageUrl: props.newsData.imageUrl || '',
      publishedAt: props.newsData.publishedAt || createCurrentDateTime()
    })
  }

  watch(
    () => [props.visible, props.type, props.newsData],
    ([visible]) => {
      if (!visible) {
        return
      }
      loadFormData()
      nextTick(() => {
        formRef.value?.clearValidate()
      })
    },
    { immediate: true }
  )

  const handleSubmit = async () => {
    if (!formRef.value) {
      return
    }

    await formRef.value.validate()
    submitting.value = true
    try {
      emit('submit', {
        title: formData.title,
        content: formData.content,
        sourceUrl: formData.sourceUrl,
        originalUrl: formData.originalUrl,
        imageUrl: formData.imageUrl,
        publishedAt: formData.publishedAt
      })
    } finally {
      submitting.value = false
    }
  }
</script>

<style scoped lang="scss">
  .date-picker {
    width: 100%;
  }

  .picture-preview {
    margin: -6px 0 18px 96px;

    :deep(.el-image) {
      width: 120px;
      height: 84px;
      border-radius: 16px;
      border: 1px solid var(--art-gray-200);
      overflow: hidden;
    }
  }
</style>
