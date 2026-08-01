<!-- 页面或页面组件：index。 -->
<template>
  <div class="user-center-page">
    <ElRow :gutter="20">
      <ElCol :xs="24" :lg="8">
        <ElCard shadow="never" class="profile-card">
          <div class="profile-card__banner"></div>
      <img class="profile-card__avatar" :src="userAvatar" alt="用户头像" />
          <div class="profile-card__name">{{ userInfo.username || '未登录用户' }}</div>
          <div class="profile-card__role">{{ roleText }}</div>
          <div class="profile-card__desc">{{ profileDescription }}</div>

          <div class="profile-card__meta">
            <div class="meta-item">
              <ArtSvgIcon icon="ri:mail-line" />
              <span>{{ userInfo.email || '--' }}</span>
            </div>
            <div class="meta-item">
              <ArtSvgIcon icon="ri:shield-user-line" />
              <span>{{ roleText }}</span>
            </div>
            <div class="meta-item">
              <ArtSvgIcon icon="ri:user-smile-line" />
              <span>coinsphere</span>
            </div>
          </div>
        </ElCard>
      </ElCol>

      <ElCol :xs="24" :lg="16">
        <ElCard shadow="never" class="detail-card">
          <template #header>
            <div class="detail-card__header">
              <span>账户信息</span>
            </div>
          </template>
          <ElDescriptions :column="1" border>
            <ElDescriptionsItem label="用户名">{{ userInfo.username || '--' }}</ElDescriptionsItem>
            <ElDescriptionsItem label="邮箱">{{ userInfo.email || '--' }}</ElDescriptionsItem>
            <ElDescriptionsItem label="角色">
              <ElSpace wrap>
                <ElTag v-for="role in userInfo.roleCodes || []" :key="role" type="primary" effect="plain">
                  {{ role }}
                </ElTag>
                <span v-if="!userInfo.roleCodes?.length">--</span>
              </ElSpace>
            </ElDescriptionsItem>
            <ElDescriptionsItem label="权限">
              <ElSpace wrap>
                <ElTag
                  v-for="permission in userInfo.permissions || []"
                  :key="permission"
                  type="success"
                  effect="plain"
                >
                  {{ permission }}
                </ElTag>
                <span v-if="!userInfo.permissions?.length">--</span>
              </ElSpace>
            </ElDescriptionsItem>
          </ElDescriptions>
        </ElCard>

        <ElCard shadow="never" class="detail-card">
          <template #header>
            <div class="detail-card__header">
              <span>说明</span>
            </div>
          </template>
          <div class="explain-block">
            <p>当前控制台采用后端菜单模式，路由、菜单和权限码都由后端返回，前端只负责渲染。</p>
            <p>新增页面时，需要同时补菜单配置、接口权限码和后端路由校验，才能形成完整 RBAC 闭环。</p>
          </div>
        </ElCard>
      </ElCol>
    </ElRow>
  </div>
</template>

<script setup lang="ts">
  import defaultAvatar from '@imgs/user/avatar.webp'
  import { useUserStore } from '@/store/modules/user'

  defineOptions({ name: 'UserCenter' })

  const userStore = useUserStore()
  const userInfo = computed(() => userStore.getUserInfo)
  const userAvatar = computed(() => userInfo.value.avatar || defaultAvatar)

  const roleText = computed(() => {
    const roles = userInfo.value.roleCodes || []
    if (!roles.length) return '未分配角色'
    return roles.join(' / ')
  })

  const profileDescription = computed(() => {
    if (userInfo.value.roleCodes?.includes('R_SUPER')) return '系统与业务配置的最终负责人'
    return '关注同步结果与 AI 分析结论'
  })
</script>

<style scoped lang="scss">
  .user-center-page {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .profile-card,
  .detail-card {
    border-radius: 20px;
  }

  .profile-card {
    position: relative;
    overflow: hidden;
    text-align: center;

    :deep(.el-card__body) {
      padding: 0 24px 24px;
    }
  }

  .profile-card__banner {
    height: 128px;
    margin: 0 -24px;
    background:
      linear-gradient(140deg, rgba(93, 135, 255, 0.85), rgba(56, 192, 252, 0.45)),
      linear-gradient(180deg, #f8fbff, #eef5ff);
  }

  .profile-card__avatar {
    width: 84px;
    height: 84px;
    margin-top: -42px;
    border: 4px solid #fff;
    border-radius: 50%;
    object-fit: cover;
  }

  .profile-card__name {
    margin-top: 14px;
    font-size: 24px;
    font-weight: 700;
    color: var(--art-gray-900);
  }

  .profile-card__role {
    margin-top: 8px;
    font-size: 13px;
    color: var(--art-gray-500);
  }

  .profile-card__desc {
    margin-top: 14px;
    font-size: 14px;
    line-height: 1.8;
    color: var(--art-gray-600);
  }

  .profile-card__meta {
    margin-top: 24px;
    display: flex;
    flex-direction: column;
    gap: 12px;
    text-align: left;
  }

  .meta-item {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 14px;
    color: var(--art-gray-600);
  }

  .detail-card {
    margin-bottom: 20px;

    :deep(.el-card__header) {
      padding: 18px 22px 14px;
    }

    :deep(.el-card__body) {
      padding: 0 22px 22px;
    }
  }

  .detail-card__header {
    font-size: 16px;
    font-weight: 600;
    color: var(--art-gray-900);
  }

  .explain-block {
    padding-top: 18px;
    font-size: 14px;
    line-height: 1.9;
    color: var(--art-gray-600);

    p {
      margin: 0 0 12px;
    }
  }
</style>
