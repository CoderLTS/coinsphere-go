<!-- 用户管理页面或组件：user-dialog。 -->
<template>
  <ElDialog
    v-model="dialogVisible"
    :title="dialogType === 'add' ? '新增用户' : '编辑用户'"
    width="560px"
    align-center
  >
    <ElForm ref="formRef" :model="formData" :rules="rules" label-width="88px">
      <ElFormItem label="用户名" prop="username">
        <ElInput v-model.trim="formData.username" placeholder="请输入用户名" />
      </ElFormItem>
      <ElFormItem label="昵称" prop="nickname">
        <ElInput v-model.trim="formData.nickname" placeholder="请输入昵称" />
      </ElFormItem>
      <ElFormItem label="头像" prop="avatar">
        <div class="avatar-editor">
          <ElImage
            class="avatar-editor__preview"
            :src="formData.avatar || defaultAvatar"
            :preview-src-list="[formData.avatar || defaultAvatar]"
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
              <ElButton :loading="uploadingAvatar">上传头像</ElButton>
            </ElUpload>
            <ElButton text :disabled="uploadingAvatar || !formData.avatar" @click="clearAvatar">
              恢复默认
            </ElButton>
            <div class="avatar-editor__tip">支持 jpg/png/webp/gif，大小不超过 2MB</div>
          </div>
        </div>
      </ElFormItem>
      <ElFormItem label="邮箱" prop="email">
        <ElInput v-model.trim="formData.email" placeholder="请输入邮箱" />
      </ElFormItem>
      <ElFormItem label="手机号" prop="phone">
        <ElInput v-model.trim="formData.phone" placeholder="请输入手机号" />
      </ElFormItem>
      <ElFormItem label="性别" prop="gender">
        <ElSelect v-model="formData.gender">
          <ElOption label="男" value="male" />
          <ElOption label="女" value="female" />
          <ElOption label="未知" value="unknown" />
        </ElSelect>
      </ElFormItem>
      <ElFormItem label="状态" prop="isActive">
        <ElRadioGroup v-model="formData.isActive">
          <ElRadio :value="true" :label="true">启用</ElRadio>
          <ElRadio :value="false" :label="false">停用</ElRadio>
        </ElRadioGroup>
      </ElFormItem>
      <ElFormItem label="角色" prop="roleCodes">
        <ElSelect v-model="formData.roleCodes" multiple filterable placeholder="请选择角色">
          <ElOption
            v-for="role in roleList"
            :key="role.code"
            :label="`${role.displayName} (${role.code})`"
            :value="role.code"
          />
        </ElSelect>
      </ElFormItem>
      <ElFormItem :label="dialogType === 'add' ? '密码' : '新密码'" prop="password">
        <ElInput
          v-model.trim="formData.password"
          type="password"
          show-password
          autocomplete="off"
          :placeholder="dialogType === 'add' ? '请输入密码' : '留空则保持原密码不变'"
        />
      </ElFormItem>
    </ElForm>

    <template #footer>
      <div class="dialog-footer">
        <ElButton @click="dialogVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="submitting" @click="handleSubmit">提交</ElButton>
      </div>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import type { FormInstance, FormRules, UploadFile } from 'element-plus'
  import { ElMessage } from 'element-plus'
  import defaultAvatar from '@imgs/user/avatar.webp'
  import { fetchGetRoleList, fetchUploadUserAvatar } from '@/api/system'

  export interface UserFormPayload {
    username: string
    nickname: string
    fullName?: string
    gender: string
    phone: string
    email: string
    avatar: string
    isActive: boolean
    roleCodes: string[]
    password?: string
  }

  interface Props {
    visible: boolean
    type: string
    userData?: Partial<Api.System.UserListItem>
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', payload: UserFormPayload): void
  }

  const props = defineProps<Props>()
  const emit = defineEmits<Emits>()

  const roleList = ref<Api.System.RoleListItem[]>([])
  const submitting = ref(false)
  const uploadingAvatar = ref(false)

  const dialogVisible = computed({
    get: () => props.visible,
    set: (value) => emit('update:visible', value)
  })

  const dialogType = computed(() => props.type)
  const formRef = ref<FormInstance>()

  const formData = reactive<UserFormPayload>({
    username: '',
    nickname: '',
    fullName: '',
    gender: 'unknown',
    phone: '',
    email: '',
    avatar: '',
    isActive: true,
    roleCodes: [],
    password: ''
  })

  const rules = reactive<FormRules>({
    username: [
      { required: true, message: '请输入用户名', trigger: 'blur' },
      { min: 2, max: 100, message: '长度在 2 到 100 个字符', trigger: 'blur' }
    ],
    nickname: [{ required: true, message: '请输入昵称', trigger: 'blur' }],
    email: [
      { required: true, message: '请输入邮箱', trigger: 'blur' },
      { type: 'email', message: '请输入正确的邮箱格式', trigger: 'blur' }
    ],
    phone: [{ required: true, message: '请输入手机号', trigger: 'blur' }],
    roleCodes: [{ required: true, message: '请选择角色', trigger: 'change' }],
    password: [
      {
        validator: (_rule, value, callback) => {
          if (dialogType.value === 'add' && !value) {
            callback(new Error('请输入密码'))
            return
          }
          if (value && value.length < 6) {
            callback(new Error('密码长度不能少于 6 位'))
            return
          }
          callback()
        },
        trigger: 'blur'
      }
    ]
  })

  const initFormData = () => {
    const row = props.userData
    const isEdit = dialogType.value === 'edit' && row
    Object.assign(formData, {
      username: isEdit ? row.username || '' : '',
      nickname: isEdit ? row.nickname || row.username || '' : '',
      fullName: isEdit ? row.fullName || row.nickname || '' : '',
      gender: isEdit ? row.gender || 'unknown' : 'unknown',
      phone: isEdit ? row.phone || '' : '',
      email: isEdit ? row.email || '' : '',
      avatar: isEdit ? row.avatar || '' : '',
      isActive: isEdit ? Boolean(row.isActive) : true,
      roleCodes: isEdit ? (Array.isArray(row.roleCodes) ? [...row.roleCodes] : []) : [],
      password: ''
    })
  }

  const loadRoles = async () => {
    const data = await fetchGetRoleList({ limit: 50, isEnabled: true })
    roleList.value = data.records.filter((role) => role.code !== 'R_GUEST')
  }

  watch(
    () => [props.visible, props.type, props.userData],
    async ([visible]) => {
      if (!visible) {
        return
      }
      await loadRoles()
      initFormData()
      nextTick(() => {
        formRef.value?.clearValidate()
      })
    },
    { immediate: true }
  )

  const handleAvatarChange = async (uploadFile: UploadFile) => {
    if (!uploadFile.raw) {
      return
    }

    const allowedTypes = ['image/jpeg', 'image/png', 'image/webp', 'image/gif']
    if (!allowedTypes.includes(uploadFile.raw.type)) {
      ElMessage.error('仅支持 jpg、png、webp、gif 格式的头像图片')
      return
    }
    if (uploadFile.raw.size > 2 * 1024 * 1024) {
      ElMessage.error('头像图片不能超过 2MB')
      return
    }

    uploadingAvatar.value = true
    try {
      const data = await fetchUploadUserAvatar(uploadFile.raw)
      formData.avatar = data.url
    } finally {
      uploadingAvatar.value = false
    }
  }

  const clearAvatar = () => {
    formData.avatar = ''
  }

  const handleSubmit = async () => {
    if (!formRef.value) return

    await formRef.value.validate()
    submitting.value = true
    try {
      const payload: UserFormPayload = {
        username: formData.username,
        nickname: formData.nickname,
        fullName: formData.fullName,
        gender: formData.gender,
        phone: formData.phone,
        email: formData.email,
        avatar: formData.avatar,
        isActive: formData.isActive,
        roleCodes: [...formData.roleCodes]
      }
      if (formData.password) {
        payload.password = formData.password
      }
      emit('submit', payload)
    } finally {
      submitting.value = false
    }
  }
</script>

<style scoped lang="scss">
  .avatar-editor {
    display: flex;
    gap: 16px;
    align-items: center;
  }

  .avatar-editor__preview {
    flex-shrink: 0;
    width: 72px;
    height: 72px;
    overflow: hidden;
    border: 1px solid var(--el-border-color);
    border-radius: 18px;
  }

  .avatar-editor__actions {
    display: flex;
    flex-direction: column;
    gap: 6px;
    align-items: flex-start;
  }

  .avatar-editor__tip {
    font-size: 12px;
    line-height: 1.5;
    color: var(--art-gray-500);
  }
</style>
