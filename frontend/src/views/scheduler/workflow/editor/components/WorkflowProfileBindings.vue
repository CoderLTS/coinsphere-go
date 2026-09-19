<template>
  <section v-if="slots.length" class="profile-bindings">
    <div class="profile-bindings__title">Profile</div>
    <div v-for="slot in slots" :key="slot.key" class="profile-bindings__slot">
      <ElFormItem :label="slot.title" :required="slot.required">
        <ElSelect
          :model-value="bindingValue(slot.key)"
          filterable
          clearable
          :placeholder="slot.required ? '选择已发布 Profile' : '不绑定'"
          @change="(value) => select(slot.key, value)"
        >
          <ElOption
            v-for="profile in profilesFor(slot)"
            :key="`${profile.pluginId}:${profile.id}:${profile.version || profile.latestPublishedVersion}`"
            :value="profileKey(profile)"
            :label="profileLabel(profile)"
          >
            <span>{{ profile.name }}</span>
            <small class="profile-bindings__option-summary">{{
              profile.summary || profile.type
            }}</small>
          </ElOption>
        </ElSelect>
        <div v-if="selectedProfile(slot.key)" class="profile-bindings__summary">
          {{ selectedProfile(slot.key)!.summary || '已发布 Profile' }} ·
          {{
            selectedProfile(slot.key)!.latestPublishedVersion || selectedProfile(slot.key)!.version
          }}
        </div>
      </ElFormItem>
    </div>
    <ElAlert
      v-if="loadingError"
      type="warning"
      :closable="false"
      title="Profile 列表暂时不可用，可稍后刷新页面重试"
    />
  </section>
</template>

<script setup lang="ts">
  import type { ProfileRecord } from '@/api/profiles'
  import { fetchProfiles, profileRef } from '@/api/profiles'
  import { fetchGetInstalledPlugins } from '@/api/system'
  import type { ProfileRef, WorkflowGraphNode, WorkflowNodeDefinition } from '@/api/workflows'

  const props = defineProps<{ node: WorkflowGraphNode; definition: WorkflowNodeDefinition }>()
  const emit = defineEmits<{ (event: 'update', bindings: Record<string, ProfileRef>): void }>()
  const profiles = ref<ProfileRecord[]>([])
  const loadingError = ref(false)
  const slots = computed(() => props.definition.profileSlots || [])
  const bindingValue = (key: string) => {
    const ref = props.node.profileBindings?.[key]
    return ref ? `${ref.pluginId}:${ref.profileId}:${ref.version}:${ref.type}` : ''
  }
  const profileVersion = (profile: ProfileRecord) =>
    profile.version || profile.latestPublishedVersion || ''
  const profileKey = (profile: ProfileRecord) =>
    `${profile.pluginId}:${profile.id}:${profileVersion(profile)}:${profile.type}`
  const profileLabel = (profile: ProfileRecord) =>
    `${profile.name} · ${profile.summary || profile.type} · ${profileVersion(profile) || '未发布'}`
  const profilesFor = (slot: { profileTypes: string[] }) =>
    profiles.value.flatMap((profile) => {
      if (!slot.profileTypes.includes(profile.type) || profile.status === 'disabled') return []
      const published = (profile.versions || []).filter((version) => version.status === 'published')
      if (published.length)
        return published.map((version) => ({
          ...profile,
          version: version.version,
          latestPublishedVersion: version.version,
          config: version.config
        }))
      return profile.latestPublishedVersion || profile.version ? [profile] : []
    })
  const selectedProfile = (key: string) => {
    const ref = props.node.profileBindings?.[key]
    return profilesFor({ profileTypes: ref?.type ? [ref.type] : [] }).find(
      (p) =>
        p.pluginId === ref?.pluginId &&
        p.id === ref?.profileId &&
        p.type === ref?.type &&
        profileVersion(p) === ref?.version
    )
  }
  const select = (key: string, value: string | undefined) => {
    const next = { ...(props.node.profileBindings || {}) }
    if (!value) delete next[key]
    else {
      const [pluginId, profileId, version, ...type] = value.split(':')
      const found = profilesFor({ profileTypes: [type.join(':')] }).find(
        (p) =>
          p.pluginId === pluginId &&
          p.id === profileId &&
          p.type === type.join(':') &&
          profileVersion(p) === version
      )
      if (found) next[key] = profileRef(found, version)
    }
    emit('update', next)
  }
  const load = async () => {
    loadingError.value = false
    try {
      const types = [...new Set(slots.value.flatMap((slot) => slot.profileTypes))]
      const owners = (await fetchGetInstalledPlugins()).map((plugin) => plugin.id)
      const results = await Promise.all(
        owners.flatMap((owner) =>
          types.map((type) =>
            fetchProfiles(owner, type).catch(() => ({ items: [] as ProfileRecord[] }))
          )
        )
      )
      const unique = new Map<string, ProfileRecord>()
      results
        .flatMap((result) => result.items)
        .forEach((profile) =>
          unique.set(
            `${profile.pluginId}:${profile.id}:${profile.latestPublishedVersion || profile.version}`,
            profile
          )
        )
      profiles.value = [...unique.values()]
    } catch {
      loadingError.value = true
    }
  }
  watch(slots, load, { immediate: true })
</script>

<style scoped>
  .profile-bindings {
    padding-top: 4px;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  .profile-bindings__title {
    margin: 18px 0 12px;
    font-size: 13px;
    font-weight: 600;
  }

  .profile-bindings__summary {
    margin-top: 4px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--el-text-color-secondary);
  }

  .profile-bindings__option-summary {
    display: block;
    font-size: 11px;
    color: var(--el-text-color-secondary);
  }
</style>
