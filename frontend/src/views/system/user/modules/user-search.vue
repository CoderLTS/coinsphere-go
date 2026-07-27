<!-- 用户管理页面或组件：user-search。 -->
<template>
  <ArtSearchBar
    ref="searchBarRef"
    v-model="formData"
    :items="formItems"
    :rules="rules"
    @reset="handleReset"
    @search="handleSearch"
  >
  </ArtSearchBar>
</template>

<script setup lang="ts">
  interface Props {
    modelValue: Api.System.UserSearchParams
  }
  interface Emits {
    (e: 'update:modelValue', value: Api.System.UserSearchParams): void
    (e: 'search', params: Api.System.UserSearchParams): void
    (e: 'reset'): void
  }
  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  // 表单数据双向绑定
  const searchBarRef = ref()
  const formData = computed({
    get: () => props.modelValue,
    set: (val) => emit('update:modelValue', val)
  })

  const statusOptions = [
    { label: '启用', value: true },
    { label: '停用', value: false }
  ]

  const rules = {}

  // 表单配置
  const formItems = computed(() => [
    {
      label: '用户名',
      key: 'username',
      type: 'input',
      placeholder: '请输入用户名',
      clearable: true
    },
    {
      label: '手机号',
      key: 'phone',
      type: 'input',
      props: { placeholder: '请输入手机号', maxlength: '11' }
    },
    {
      label: '邮箱',
      key: 'email',
      type: 'input',
      props: { placeholder: '请输入邮箱' }
    },
    {
      label: '状态',
      key: 'isActive',
      type: 'select',
      props: {
        placeholder: '请选择状态',
        options: statusOptions
      }
    },
    {
      label: '性别',
      key: 'gender',
      type: 'radiogroup',
      props: {
        options: [
          { label: '男', value: 'male' },
          { label: '女', value: 'female' }
        ]
      }
    }
  ])

  // 事件
  function handleReset() {
    emit('reset')
  }

  async function handleSearch(params: Api.System.UserSearchParams) {
    await searchBarRef.value.validate()
    emit('search', params)
  }
</script>
