<template>
  <div class="plugin-page" v-loading="loading">
    <header class="plugin-header">
      <div class="title-block">
        <span class="title-icon"><ArtSvgIcon icon="ri:puzzle-2-line" /></span>
        <div>
          <p>插件注册表</p>
          <h1>插件管理</h1>
          <span>当前进程已加载的插件与扩展能力</span>
        </div>
      </div>
      <div class="header-status">
        <span class="loaded-indicator"><i></i> 注册表正常</span>
        <strong>{{ plugins.length }}</strong>
        <span>已安装</span>
        <ElTooltip content="刷新插件列表" placement="top">
          <button type="button" aria-label="刷新插件列表" @click="loadPlugins">
            <ArtSvgIcon icon="ri:refresh-line" />
          </button>
        </ElTooltip>
      </div>
    </header>

    <section v-if="plugins.length" class="plugin-grid" aria-label="已安装插件">
      <article
        v-for="plugin in plugins"
        :key="plugin.id"
        class="plugin-card"
        role="button"
        tabindex="0"
        :aria-label="`查看${pluginLabel(plugin)}详情`"
        @click="openPlugin(plugin)"
        @keydown.enter="openPlugin(plugin)"
        @keydown.space.prevent="openPlugin(plugin)"
      >
        <div class="plugin-card__head">
          <span class="plugin-mark"><ArtSvgIcon :icon="pluginIcon(plugin.id)" /></span>
          <div>
            <h2>{{ pluginLabel(plugin) }}</h2>
            <span class="plugin-id">
              <span class="plugin-id__label">插件标识</span>
              <code>{{ plugin.id }}</code>
            </span>
          </div>
          <span class="version">v{{ plugin.version }}</span>
        </div>

        <div class="plugin-meta">
          <span><ArtSvgIcon icon="ri:checkbox-circle-line" /> 已加载</span>
          <span><ArtSvgIcon icon="ri:apps-line" /> {{ plugin.contributes.length }} 类能力</span>
          <ArtSvgIcon class="detail-arrow" icon="ri:arrow-right-s-line" />
        </div>

        <div class="contribution-list">
          <span v-for="item in plugin.contributes" :key="item">
            <ArtSvgIcon :icon="contributionIcon(item)" />
            {{ contributionLabel(item) }}
          </span>
        </div>
      </article>
    </section>

    <ElEmpty v-else-if="!loading" description="当前没有已加载插件" />

    <ElDrawer
      v-model="detailVisible"
      :title="selectedPlugin ? pluginLabel(selectedPlugin) : '插件详情'"
      size="min(520px, 94vw)"
    >
      <div v-if="selectedPlugin" class="plugin-detail">
        <div class="plugin-detail__summary">
          <span class="plugin-mark"><ArtSvgIcon :icon="pluginIcon(selectedPlugin.id)" /></span>
          <div>
            <code>{{ selectedPlugin.id }}</code>
            <span>v{{ selectedPlugin.version }} · 已加载</span>
          </div>
        </div>

        <section>
          <h3>
            注册节点
            <small>{{ selectedPlugin.nodes.length }}</small>
          </h3>
          <div v-if="selectedPlugin.nodes.length" class="detail-list">
            <div v-for="node in selectedPlugin.nodes" :key="node.type" class="detail-row">
              <div>
                <strong>{{ node.title }}</strong>
                <code>{{ node.type }}</code>
              </div>
              <div class="detail-tags">
                <ElTag size="small" effect="plain">{{ nodeKindLabel(node.kind) }}</ElTag>
                <span>v{{ node.version }}</span>
              </div>
            </div>
          </div>
          <ElEmpty v-else :image-size="52" description="未注册节点" />
        </section>

        <section>
          <h3>
            注册页面
            <small>{{ selectedPlugin.pages.length }}</small>
          </h3>
          <div v-if="selectedPlugin.pages.length" class="detail-list">
            <div
              v-for="page in selectedPlugin.pages"
              :key="`${page.kind}:${page.pageKey}`"
              class="detail-row"
            >
              <div>
                <strong>{{ page.title }}</strong>
                <code>{{ page.pageKey }}</code>
              </div>
              <ElTag size="small" effect="plain">{{ pageKindLabel(page.kind) }}</ElTag>
            </div>
          </div>
          <ElEmpty v-else :image-size="52" description="未注册页面" />
        </section>
      </div>
    </ElDrawer>
  </div>
</template>

<script setup lang="ts">
  import { fetchGetInstalledPlugins } from '@/api/system'

  defineOptions({ name: 'Plugins' })

  const loading = ref(false)
  const plugins = ref<Api.System.InstalledPlugin[]>([])
  const selectedPlugin = ref<Api.System.InstalledPlugin>()
  const detailVisible = ref(false)

  const pluginIcon = (id: string) => {
    if (id.includes('quant')) return 'ri:line-chart-line'
    if (id.includes('notification')) return 'ri:notification-3-line'
    if (id.includes('qq')) return 'ri:qq-line'
    if (id.includes('connector')) return 'ri:links-line'
    if (id.includes('ai')) return 'ri:sparkling-2-line'
    return 'ri:puzzle-2-line'
  }

  const labels: Record<string, string> = {
    nodes: '节点',
    triggers: '触发器',
    strategies: '策略',
    apiRoutes: '接口路由',
    pages: '页面',
    resultPages: '结果页',
    assistantQueries: '助手查询',
    migrations: '数据库迁移'
  }

  const pluginNames: Record<string, string> = {
    'official.connector': '连接器',
    'official.ai': '人工智能',
    'official.quant': '量化分析',
    'official.notification': '通知',
    'official.qq': 'QQ机器人'
  }

  const icons: Record<string, string> = {
    nodes: 'ri:node-tree',
    triggers: 'ri:flashlight-line',
    strategies: 'ri:stock-line',
    apiRoutes: 'ri:route-line',
    pages: 'ri:layout-grid-line',
    resultPages: 'ri:file-chart-line',
    assistantQueries: 'ri:question-answer-line',
    migrations: 'ri:database-2-line'
  }

  const pluginLabel = (plugin: Api.System.InstalledPlugin) =>
    pluginNames[plugin.id] || plugin.name || plugin.id
  const contributionLabel = (value: string) => labels[value] || value
  const contributionIcon = (value: string) => icons[value] || 'ri:add-circle-line'
  const nodeKindLabel = (kind: 'action' | 'trigger') => (kind === 'trigger' ? '触发器' : '动作')
  const pageKindLabel = (kind: 'page' | 'resultPage') => (kind === 'resultPage' ? '结果页' : '页面')
  const openPlugin = (plugin: Api.System.InstalledPlugin) => {
    selectedPlugin.value = plugin
    detailVisible.value = true
  }

  const loadPlugins = async () => {
    loading.value = true
    try {
      plugins.value = await fetchGetInstalledPlugins()
    } finally {
      loading.value = false
    }
  }

  onMounted(() => void loadPlugins())
</script>

<style scoped lang="scss">
  .plugin-page {
    min-height: 100%;
    padding: 24px;
    color: var(--art-gray-900);
    background: var(--art-gray-100);
  }

  .plugin-header,
  .title-block,
  .header-status,
  .plugin-card__head,
  .plugin-meta,
  .contribution-list span {
    display: flex;
    align-items: center;
  }

  .plugin-header {
    gap: 24px;
    justify-content: space-between;
    padding: 20px 22px;
    background: var(--default-box-color);
    border: 1px solid var(--art-gray-300);
    border-radius: 8px;
  }

  .title-block {
    gap: 14px;
  }

  .title-icon,
  .plugin-mark {
    display: grid;
    flex: 0 0 auto;
    place-items: center;
    color: #fff;
    border-radius: 8px;
  }

  .title-icon {
    width: 48px;
    height: 48px;
    font-size: 23px;
    background: #2878ff;
  }

  .title-block p,
  .title-block h1,
  .plugin-card h2 {
    margin: 0;
    letter-spacing: 0;
  }

  .title-block p {
    margin-bottom: 3px;
    font-size: 10px;
    font-weight: 700;
    color: #2878ff;
  }

  .title-block h1 {
    font-size: 20px;
    line-height: 1.3;
  }

  .title-block div > span,
  .header-status > span,
  .plugin-card code {
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .header-status {
    gap: 10px;
  }

  .header-status strong {
    margin-left: 14px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 24px;
  }

  .loaded-indicator {
    display: inline-flex;
    gap: 7px;
    align-items: center;
    color: #16a86b !important;
  }

  .loaded-indicator i {
    width: 7px;
    height: 7px;
    background: #16a86b;
    border-radius: 50%;
  }

  .header-status button {
    display: grid;
    width: 36px;
    height: 36px;
    margin-left: 10px;
    color: var(--art-gray-800);
    cursor: pointer;
    background: var(--art-gray-200);
    border: 1px solid var(--art-gray-300);
    border-radius: 6px;
    place-items: center;
  }

  .plugin-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
    margin-top: 16px;
  }

  .plugin-card {
    min-width: 0;
    padding: 18px;
    cursor: pointer;
    background: var(--default-box-color);
    border: 1px solid var(--art-gray-300);
    border-radius: 8px;
  }

  .plugin-card:hover,
  .plugin-card:focus-visible {
    border-color: #7aa7c7;
    outline: none;
    box-shadow: 0 4px 14px rgb(30 61 80 / 0.08);
  }

  .plugin-card__head {
    gap: 12px;
  }

  .plugin-mark {
    width: 42px;
    height: 42px;
    font-size: 19px;
    background: #147d78;
  }

  .plugin-card__head > div {
    min-width: 0;
  }

  .plugin-card h2 {
    overflow: hidden;
    font-size: 15px;
    line-height: 1.5;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .plugin-card code {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .plugin-id {
    display: flex;
    gap: 6px;
    align-items: center;
  }

  .plugin-id__label {
    flex: 0 0 auto;
    font-size: 11px;
    color: var(--art-gray-500);
  }

  .plugin-id code {
    display: inline;
  }

  .version {
    flex: 0 0 auto;
    padding: 4px 7px;
    margin-left: auto;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    color: #2878ff;
    background: rgb(40 120 255 / 0.1);
    border-radius: 5px;
  }

  .plugin-meta {
    gap: 18px;
    padding: 14px 0;
    margin-top: 16px;
    font-size: 12px;
    color: var(--art-gray-700);
    border-top: 1px solid var(--art-gray-300);
    border-bottom: 1px solid var(--art-gray-300);
  }

  .plugin-meta span,
  .contribution-list span {
    display: inline-flex;
    gap: 6px;
    align-items: center;
  }

  .plugin-meta span:first-child {
    color: #16a86b;
  }

  .detail-arrow {
    margin-left: auto;
    font-size: 18px;
    color: var(--art-gray-500);
  }

  .contribution-list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding-top: 14px;
  }

  .contribution-list span {
    padding: 5px 8px;
    font-size: 11px;
    color: var(--art-gray-800);
    background: var(--art-gray-200);
    border-radius: 5px;
  }

  .plugin-detail__summary {
    display: flex;
    gap: 12px;
    align-items: center;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .plugin-detail__summary > div {
    min-width: 0;
  }

  .plugin-detail__summary code,
  .plugin-detail__summary span,
  .detail-row code,
  .detail-tags span {
    display: block;
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .plugin-detail__summary code,
  .detail-row code {
    overflow: hidden;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .plugin-detail__summary span {
    margin-top: 4px;
  }

  .plugin-detail section {
    margin-top: 24px;
  }

  .plugin-detail h3 {
    margin: 0 0 8px;
    font-size: 14px;
    letter-spacing: 0;
  }

  .plugin-detail h3 small {
    margin-left: 5px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    font-weight: 500;
    color: var(--art-gray-500);
  }

  .detail-list {
    border-top: 1px solid var(--art-gray-300);
  }

  .detail-row,
  .detail-tags {
    display: flex;
    align-items: center;
  }

  .detail-row {
    gap: 16px;
    justify-content: space-between;
    min-height: 64px;
    padding: 10px 0;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .detail-row > div:first-child {
    min-width: 0;
  }

  .detail-row strong {
    display: block;
    margin-bottom: 4px;
    font-size: 13px;
    font-weight: 650;
  }

  .detail-tags {
    flex: 0 0 auto;
    gap: 8px;
  }

  @media (max-width: 1100px) {
    .plugin-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 700px) {
    .plugin-page {
      padding: 12px;
    }

    .plugin-header {
      align-items: flex-start;
      flex-direction: column;
    }

    .header-status {
      width: 100%;
    }

    .header-status button {
      margin-left: auto;
    }

    .plugin-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
