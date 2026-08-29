<template>
  <div class="plugin-page" v-loading="loading">
    <header class="plugin-header">
      <div class="title-block">
        <span class="title-icon"><ArtSvgIcon icon="ri:puzzle-2-line" /></span>
        <div>
          <p>EXTENSION REGISTRY</p>
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
      <article v-for="plugin in plugins" :key="plugin.id" class="plugin-card">
        <div class="plugin-card__head">
          <span class="plugin-mark"><ArtSvgIcon :icon="pluginIcon(plugin.id)" /></span>
          <div>
            <h2>{{ plugin.name }}</h2>
            <code>{{ plugin.id }}</code>
          </div>
          <span class="version">v{{ plugin.version }}</span>
        </div>

        <div class="plugin-meta">
          <span><ArtSvgIcon icon="ri:checkbox-circle-line" /> 已加载</span>
          <span><ArtSvgIcon icon="ri:apps-line" /> {{ plugin.contributes.length }} 类能力</span>
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
  </div>
</template>

<script setup lang="ts">
  import { fetchGetInstalledPlugins } from '@/api/system'

  defineOptions({ name: 'Plugins' })

  const loading = ref(false)
  const plugins = ref<Api.System.InstalledPlugin[]>([])

  const pluginIcon = (id: string) => {
    if (id.includes('quant')) return 'ri:line-chart-line'
    if (id.includes('notification')) return 'ri:notification-3-line'
    if (id.includes('connector')) return 'ri:links-line'
    if (id.includes('ai')) return 'ri:sparkling-2-line'
    return 'ri:puzzle-2-line'
  }

  const labels: Record<string, string> = {
    nodes: '节点',
    triggers: '触发器',
    strategies: '策略',
    apiRoutes: 'API 路由',
    pages: '页面',
    resultPages: '结果页'
  }

  const icons: Record<string, string> = {
    nodes: 'ri:node-tree',
    triggers: 'ri:flashlight-line',
    strategies: 'ri:stock-line',
    apiRoutes: 'ri:route-line',
    pages: 'ri:layout-grid-line',
    resultPages: 'ri:file-chart-line'
  }

  const contributionLabel = (value: string) => labels[value] || value
  const contributionIcon = (value: string) => icons[value] || 'ri:add-circle-line'

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
    background: var(--default-box-color);
    border: 1px solid var(--art-gray-300);
    border-radius: 8px;
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
