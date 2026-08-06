<!-- 菜单管理页面或组件：menu-dialog。 -->
<template>
  <ElDialog
    :title="dialogTitle"
    :model-value="visible"
    @update:model-value="handleCancel"
    width="720px"
    align-center
    @closed="handleClosed"
  >
    <ElForm ref="formRef" :model="form" :rules="rules" label-width="96px">
      <ElFormItem label="类型" prop="menuType">
        <ElRadioGroup v-model="form.menuType" :disabled="isEdit">
          <ElRadioButton value="menu" label="menu">菜单</ElRadioButton>
          <ElRadioButton value="button" label="button">按钮</ElRadioButton>
        </ElRadioGroup>
      </ElFormItem>

      <template v-if="form.menuType === 'menu'">
        <ElFormItem label="父级菜单" prop="parentId">
          <ElSelect v-model="form.parentId" clearable placeholder="不选则创建为一级菜单">
            <ElOption
              v-for="option in menuOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="菜单名称" prop="title">
          <ElInput v-model.trim="form.title" placeholder="请输入菜单名称" />
        </ElFormItem>
        <ElFormItem label="国际化 Key" prop="i18nKey">
          <ElInput v-model.trim="form.i18nKey" placeholder="如：menus.data.news" />
        </ElFormItem>
        <ElFormItem label="中文文案" prop="i18nTexts.zh">
          <ElInput v-model.trim="form.i18nTexts.zh" placeholder="请输入中文文案" />
        </ElFormItem>
        <ElFormItem label="英文文案" prop="i18nTexts.en">
          <ElInput v-model.trim="form.i18nTexts.en" placeholder="请输入英文文案" />
        </ElFormItem>
        <ElFormItem label="路由名称" prop="name">
          <ElInput v-model.trim="form.name" placeholder="如：User、NewsData" />
        </ElFormItem>
        <ElFormItem label="路由地址" prop="path">
          <ElInput
            v-model.trim="form.path"
            placeholder="一级菜单用 / 开头，子菜单建议使用相对路径"
          />
        </ElFormItem>
        <ElFormItem label="组件路径" prop="component">
          <ElInput v-model.trim="form.component" placeholder="如：/system/user 或 /index/index" />
        </ElFormItem>
        <ElFormItem label="页面权限" prop="permissionCode">
          <ElInput
            v-model.trim="form.permissionCode"
            placeholder="如：system.users.view；目录菜单可留空"
          />
        </ElFormItem>
        <ElFormItem label="图标" prop="icon">
          <ElInput v-model.trim="form.icon" placeholder="如：ri:user-line" />
        </ElFormItem>
        <ElFormItem label="可见角色" prop="roleCodes">
          <ElSelect v-model="form.roleCodes" multiple filterable placeholder="请选择可见角色">
            <ElOption
              v-for="role in roleList"
              :key="role.code"
              :label="`${role.displayName} (${role.code})`"
              :value="role.code"
            />
          </ElSelect>
        </ElFormItem>
        <ElRow :gutter="16">
          <ElCol :span="12">
            <ElFormItem label="排序" prop="sort">
              <ElInputNumber v-model="form.sort" :min="1" :max="9999" controls-position="right" />
            </ElFormItem>
          </ElCol>
          <ElCol :span="12">
            <ElFormItem label="徽章文本" prop="badgeText">
              <ElInput v-model.trim="form.badgeText" placeholder="如：New" />
            </ElFormItem>
          </ElCol>
        </ElRow>
        <ElFormItem label="外部链接" prop="link">
          <ElInput v-model.trim="form.link" placeholder="如：https://example.com" />
        </ElFormItem>
        <ElFormItem label="激活路径" prop="activePath">
          <ElInput v-model.trim="form.activePath" placeholder="用于隐藏详情页高亮父菜单" />
        </ElFormItem>
        <ElRow :gutter="16">
          <ElCol :span="8"
            ><ElFormItem label="启用"><ElSwitch v-model="form.isEnable" /></ElFormItem
          ></ElCol>
          <ElCol :span="8"
            ><ElFormItem label="缓存"><ElSwitch v-model="form.keepAlive" /></ElFormItem
          ></ElCol>
          <ElCol :span="8"
            ><ElFormItem label="隐藏菜单"><ElSwitch v-model="form.isHide" /></ElFormItem
          ></ElCol>
        </ElRow>
        <ElRow :gutter="16">
          <ElCol :span="8"
            ><ElFormItem label="隐藏标签"><ElSwitch v-model="form.isHideTab" /></ElFormItem
          ></ElCol>
          <ElCol :span="8"
            ><ElFormItem label="内嵌页面"><ElSwitch v-model="form.isIframe" /></ElFormItem
          ></ElCol>
          <ElCol :span="8"
            ><ElFormItem label="固定标签"><ElSwitch v-model="form.fixedTab" /></ElFormItem
          ></ElCol>
        </ElRow>
        <ElRow :gutter="16">
          <ElCol :span="8"
            ><ElFormItem label="全屏页面"><ElSwitch v-model="form.isFullPage" /></ElFormItem
          ></ElCol>
        </ElRow>
      </template>

      <template v-else>
        <ElFormItem label="所属菜单" prop="menuId">
          <ElSelect v-model="form.menuId" filterable placeholder="请选择父级菜单">
            <ElOption
              v-for="option in menuOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="权限名称" prop="title">
          <ElInput v-model.trim="form.title" placeholder="如：新增、编辑、删除" />
        </ElFormItem>
        <ElFormItem label="国际化 Key" prop="i18nKey">
          <ElInput v-model.trim="form.i18nKey" placeholder="如：permissions.user.add" />
        </ElFormItem>
        <ElFormItem label="中文文案" prop="i18nTexts.zh">
          <ElInput v-model.trim="form.i18nTexts.zh" placeholder="请输入中文文案" />
        </ElFormItem>
        <ElFormItem label="英文文案" prop="i18nTexts.en">
          <ElInput v-model.trim="form.i18nTexts.en" placeholder="请输入英文文案" />
        </ElFormItem>
        <ElFormItem label="权限码" prop="permissionCode">
          <ElInput v-model.trim="form.permissionCode" placeholder="如：data.news.create" />
        </ElFormItem>
        <ElFormItem label="角色范围" prop="roleCodes">
          <ElSelect v-model="form.roleCodes" multiple filterable placeholder="请选择角色">
            <ElOption
              v-for="role in roleList"
              :key="role.code"
              :label="`${role.displayName} (${role.code})`"
              :value="role.code"
            />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="排序" prop="sort">
          <ElInputNumber v-model="form.sort" :min="1" :max="9999" controls-position="right" />
        </ElFormItem>
      </template>
    </ElForm>

    <template #footer>
      <span class="dialog-footer">
        <ElButton @click="handleCancel">取消</ElButton>
        <ElButton type="primary" :loading="submitting" @click="handleSubmit">确定</ElButton>
      </span>
    </template>
  </ElDialog>
</template>

<script setup lang="ts">
  import type { FormInstance, FormRules } from 'element-plus'
  import { fetchGetRoleList } from '@/api/system'
  import { formatMenuTitle } from '@/utils/router'
  import type { AppRouteRecord } from '@/types/router'

  export interface MenuDialogPayload {
    id?: number
    menuType: 'menu' | 'button'
    parentId: number | null
    menuId: number | null
    title: string
    name: string
    path: string
    component: string
    icon: string
    sort: number
    isEnable: boolean
    keepAlive: boolean
    isHide: boolean
    isHideTab: boolean
    link: string
    isIframe: boolean
    badgeText: string
    fixedTab: boolean
    activePath: string
    roleCodes: string[]
    isFullPage: boolean
    permissionCode: string
    i18nKey: string
    i18nTexts: Api.System.I18nTexts
  }

  interface Props {
    visible: boolean
    editData?: Record<string, any> | null
    type?: 'menu' | 'button'
    menuTree?: AppRouteRecord[]
  }

  interface Emits {
    (e: 'update:visible', value: boolean): void
    (e: 'submit', data: MenuDialogPayload): void
  }

  const props = withDefaults(defineProps<Props>(), {
    visible: false,
    type: 'menu',
    menuTree: () => []
  })

  const emit = defineEmits<Emits>()

  const formRef = ref<FormInstance>()
  const isEdit = ref(false)
  const submitting = ref(false)
  const roleList = ref<Api.System.RoleListItem[]>([])

  const form = reactive<MenuDialogPayload>({
    menuType: 'menu',
    id: undefined,
    parentId: null,
    menuId: null,
    title: '',
    name: '',
    path: '',
    component: '',
    icon: '',
    sort: 1,
    isEnable: true,
    keepAlive: false,
    isHide: false,
    isHideTab: false,
    link: '',
    isIframe: false,
    badgeText: '',
    fixedTab: false,
    activePath: '',
    roleCodes: [],
    isFullPage: false,
    permissionCode: '',
    i18nKey: '',
    i18nTexts: {
      zh: '',
      en: ''
    }
  })

  const rules = reactive<FormRules>({
    title: [{ required: true, message: '请输入名称', trigger: 'blur' }],
    name: [{ required: true, message: '请输入路由名称', trigger: 'blur' }],
    path: [{ required: true, message: '请输入路由地址', trigger: 'blur' }],
    roleCodes: [{ required: true, message: '请选择角色', trigger: 'change' }],
    menuId: [{ required: true, message: '请选择所属菜单', trigger: 'change' }],
    permissionCode: [{ required: false, message: '请输入权限码', trigger: 'blur' }],
    i18nKey: [
      { required: true, message: '请输入国际化 key', trigger: 'blur' },
      {
        pattern: /^[a-zA-Z0-9_.-]+$/,
        message: '国际化 key 仅支持字母、数字、点、下划线和中划线',
        trigger: 'blur'
      }
    ],
    'i18nTexts.zh': [{ required: true, message: '请输入中文文案', trigger: 'blur' }],
    'i18nTexts.en': [{ required: true, message: '请输入英文文案', trigger: 'blur' }]
  })

  const dialogTitle = computed(() => {
    const typeLabel = form.menuType === 'menu' ? '菜单' : '按钮'
    return `${isEdit.value ? '编辑' : '新增'}${typeLabel}`
  })

  const collectDescendantIds = (items: AppRouteRecord[], targetId: number): number[] => {
    const result: number[] = []

    const collect = (nodes: AppRouteRecord[]) => {
      for (const node of nodes) {
        if (!node.id) continue
        result.push(Number(node.id))
        if (node.children?.length) {
          collect(node.children)
        }
      }
    }

    const visit = (nodes: AppRouteRecord[]) => {
      for (const node of nodes) {
        if (!node.id) continue
        if (Number(node.id) === targetId) {
          collect(node.children || [])
          return true
        }
        if (node.children?.length && visit(node.children)) {
          return true
        }
      }
      return false
    }

    visit(items)
    return result
  }

  const menuOptions = computed(() => {
    const excludedIds = new Set<number>()

    if (isEdit.value && form.menuType === 'menu' && form.id) {
      excludedIds.add(Number(form.id))
      collectDescendantIds(props.menuTree, Number(form.id)).forEach((id) => excludedIds.add(id))
    }

    const options: Array<{ label: string; value: number }> = []

    const getDisplayTitle = (i18nKey: string | undefined, fallbackTitle: string | undefined) => {
      if (i18nKey) {
        const translated = formatMenuTitle(i18nKey)
        if (translated !== i18nKey) {
          return translated
        }
      }
      return fallbackTitle || ''
    }

    const walk = (nodes: AppRouteRecord[], depth: number) => {
      nodes.forEach((node) => {
        if (!node.id || node.meta?.isAuthButton) return
        if (!excludedIds.has(Number(node.id))) {
          options.push({
            value: Number(node.id),
            label: `${'　'.repeat(depth)}${getDisplayTitle(node.meta?.i18nKey, node.meta?.title)}`
          })
        }
        if (node.children?.length) {
          walk(
            node.children.filter((child) => !child.meta?.isAuthButton),
            depth + 1
          )
        }
      })
    }

    walk(props.menuTree, 0)
    return options
  })

  const resetForm = () => {
    formRef.value?.resetFields()
    Object.assign(form, {
      menuType: props.type,
      id: undefined,
      parentId: null,
      menuId: null,
      title: '',
      name: '',
      path: '',
      component: '',
      icon: '',
      sort: 1,
      isEnable: true,
      keepAlive: false,
      isHide: false,
      isHideTab: false,
      link: '',
      isIframe: false,
      badgeText: '',
      fixedTab: false,
      activePath: '',
      roleCodes: [],
      isFullPage: false,
      permissionCode: '',
      i18nKey: '',
      i18nTexts: {
        zh: '',
        en: ''
      }
    })
  }

  const loadFormData = () => {
    if (!props.editData) {
      return
    }

    if (form.menuType === 'menu') {
      const row = props.editData
      isEdit.value = Boolean(row.id)
      Object.assign(form, {
        id: row.id,
        parentId: row.parentId ?? null,
        title: String(row.meta?.title || ''),
        i18nKey: String(row.meta?.i18nKey || ''),
        i18nTexts: {
          zh: String(row.meta?.i18nTexts?.zh || row.meta?.title || ''),
          en: String(row.meta?.i18nTexts?.en || row.meta?.title || '')
        },
        name: row.name || '',
        path: row.path || '',
        component: row.component || '',
        icon: row.meta?.icon || '',
        sort: row.meta?.sort || 1,
        isEnable: row.meta?.isEnable !== false,
        keepAlive: row.meta?.keepAlive ?? false,
        isHide: row.meta?.isHide ?? false,
        isHideTab: row.meta?.isHideTab ?? false,
        link: row.meta?.link || '',
        isIframe: row.meta?.isIframe ?? false,
        badgeText: row.meta?.showTextBadge || '',
        fixedTab: row.meta?.fixedTab ?? false,
        activePath: row.meta?.activePath || '',
        roleCodes: [...(row.meta?.roles || [])],
        isFullPage: row.meta?.isFullPage ?? false,
        permissionCode: row.meta?.permissionCode || ''
      })
      return
    }

    isEdit.value = Boolean(props.editData.id)
    Object.assign(form, {
      id: props.editData.id,
      menuId: props.editData.menuId ?? null,
      title: props.editData.title || '',
      i18nKey: props.editData.i18nKey || '',
      i18nTexts: {
        zh: props.editData.i18nTexts?.zh || props.editData.title || '',
        en: props.editData.i18nTexts?.en || props.editData.title || ''
      },
      permissionCode: props.editData.permissionCode || '',
      sort: props.editData.sort || 1,
      roleCodes: [...(props.editData.roleCodes || [])]
    })
  }

  const loadRoles = async () => {
    const data = await fetchGetRoleList({ limit: 50, isEnabled: true })
    roleList.value = data.records
  }

  watch(
    () => props.visible,
    async (visible) => {
      if (!visible) return
      resetForm()
      isEdit.value = false
      form.menuType = props.type
      await loadRoles()
      if (props.editData) {
        loadFormData()
      }
      nextTick(() => {
        formRef.value?.clearValidate()
      })
    }
  )

  watch(
    () => props.type,
    (type) => {
      if (!props.visible) return
      form.menuType = type
    }
  )

  const handleSubmit = async () => {
    if (!formRef.value) return
    await formRef.value.validate()
    submitting.value = true
    try {
      emit('submit', {
        ...form,
        permissionCode: form.permissionCode.trim(),
        roleCodes: [...form.roleCodes],
        i18nTexts: {
          zh: form.i18nTexts.zh,
          en: form.i18nTexts.en
        }
      })
    } finally {
      submitting.value = false
    }
  }

  const handleCancel = () => {
    emit('update:visible', false)
  }

  const handleClosed = () => {
    resetForm()
    isEdit.value = false
  }
</script>
