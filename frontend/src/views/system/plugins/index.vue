<template>
  <div class="plugin-page art-full-height" v-loading="loading">
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
          <span class="plugin-mark"><ArtSvgIcon :icon="pluginIcon()" /></span>
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
      size="min(600px, 96vw)"
    >
      <div v-if="selectedPlugin" class="plugin-detail">
        <div class="plugin-detail__summary">
          <span class="plugin-mark"><ArtSvgIcon :icon="pluginIcon()" /></span>
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
            <div
              v-for="node in selectedPlugin.nodes"
              :key="node.type"
              :class="['node-entry', { 'is-open': expandedNodeType === node.type }]"
            >
              <button
                type="button"
                class="node-row"
                :aria-expanded="expandedNodeType === node.type"
                @click="toggleNode(node.type)"
              >
                <span class="node-copy">
                  <strong>{{ node.title }}</strong>
                  <code>{{ node.type }}</code>
                </span>
                <span class="node-controls">
                  <ElTag size="small" effect="plain">{{ nodeKindLabel(node.kind) }}</ElTag>
                  <span class="node-version">v{{ node.version }}</span>
                  <ArtSvgIcon
                    icon="ri:arrow-down-s-line"
                    :class="['node-chevron', { 'is-open': expandedNodeType === node.type }]"
                  />
                </span>
              </button>

              <div v-if="expandedNodeType === node.type" class="node-config">
                <div class="node-config__heading">
                  <strong>配置参数</strong>
                  <small>{{ configFields(node).length }} 项</small>
                </div>
                <dl v-if="configFields(node).length" class="parameter-list">
                  <div
                    v-for="[fieldName, schema] in configFields(node)"
                    :key="fieldName"
                    class="parameter-row"
                  >
                    <dt>
                      <span class="parameter-title">
                        <strong>{{ schema.title || fieldName }}</strong>
                        <code>{{ fieldName }}</code>
                      </span>
                      <span class="parameter-tags">
                        <ElTag size="small" effect="plain">{{
                          schemaTypeLabel(schema.type)
                        }}</ElTag>
                        <ElTag
                          v-if="isConfigRequired(node, fieldName)"
                          size="small"
                          type="warning"
                          effect="plain"
                        >
                          必填
                        </ElTag>
                        <ElTag
                          v-if="schema['x-coinsphere-secret'] === true"
                          size="small"
                          type="danger"
                          effect="plain"
                        >
                          密钥
                        </ElTag>
                      </span>
                    </dt>
                    <dd v-if="schema.description" class="parameter-description">
                      {{ schema.description }}
                    </dd>
                    <dd v-if="hasSchemaDefault(schema)" class="parameter-meta">
                      <span>默认值</span>
                      <code>{{ formatSchemaValue(schema.default) }}</code>
                    </dd>
                    <dd v-if="schemaOptions(schema)" class="parameter-meta">
                      <span>可选值</span>
                      <span>{{ schemaOptions(schema) }}</span>
                    </dd>
                  </div>
                </dl>
                <p v-else class="node-config__empty">无需配置参数</p>
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
  const expandedNodeType = ref<string>()

  type PluginNode = Api.System.InstalledPlugin['nodes'][number]
  type ConfigSchema = Record<string, any>

  const pluginIcon = () => 'ri:puzzle-2-line'

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

  const pluginLabel = (plugin: Api.System.InstalledPlugin) => plugin.name || plugin.id
  const contributionLabel = (value: string) => labels[value] || value
  const contributionIcon = (value: string) => icons[value] || 'ri:add-circle-line'
  const nodeKindLabel = (kind: 'action' | 'trigger') => (kind === 'trigger' ? '触发器' : '动作')
  const pageKindLabel = (kind: 'page' | 'resultPage') => (kind === 'resultPage' ? '结果页' : '页面')
  const schemaTypeLabels: Record<string, string> = {
    string: '文本',
    integer: '整数',
    number: '数值',
    boolean: '布尔值',
    array: '数组',
    object: '对象'
  }
  const configFields = (node: PluginNode) =>
    Object.entries(node.configSchema?.properties || {}) as Array<[string, ConfigSchema]>
  const isConfigRequired = (node: PluginNode, fieldName: string) =>
    Array.isArray(node.configSchema?.required) && node.configSchema.required.includes(fieldName)
  const schemaTypeLabel = (value: unknown) =>
    Array.isArray(value)
      ? value.map((item) => schemaTypeLabels[String(item)] || String(item)).join(' / ')
      : schemaTypeLabels[String(value)] || String(value || '任意')
  const formatSchemaValue = (value: unknown) =>
    typeof value === 'string' ? value : JSON.stringify(value)
  const hasSchemaDefault = (schema: ConfigSchema) =>
    Object.prototype.hasOwnProperty.call(schema, 'default')
  const schemaOptions = (schema: ConfigSchema) => {
    if (!Array.isArray(schema.enum)) return ''
    const labels = Array.isArray(schema.enumLabels) ? schema.enumLabels : []
    return schema.enum
      .map((value: unknown, index: number) => formatSchemaValue(labels[index] ?? value))
      .join('、')
  }
  const toggleNode = (nodeType: string) => {
    expandedNodeType.value = expandedNodeType.value === nodeType ? undefined : nodeType
  }
  const openPlugin = (plugin: Api.System.InstalledPlugin) => {
    selectedPlugin.value = plugin
    expandedNodeType.value = undefined
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
    padding: 0;
    color: var(--art-gray-900);
    background: transparent;
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

  .node-entry {
    border-bottom: 1px solid var(--art-gray-300);
  }

  .node-row,
  .node-controls,
  .parameter-row dt,
  .parameter-tags,
  .node-config__heading,
  .parameter-meta {
    display: flex;
    align-items: center;
  }

  .node-row {
    gap: 16px;
    justify-content: space-between;
    width: 100%;
    min-height: 64px;
    padding: 10px 0;
    font: inherit;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
  }

  .node-row:hover,
  .node-row:focus-visible {
    background: var(--art-gray-100);
    outline: none;
  }

  .node-copy {
    min-width: 0;
  }

  .node-copy strong,
  .node-copy code,
  .parameter-title strong,
  .parameter-title code {
    display: block;
  }

  .node-copy strong,
  .parameter-title strong {
    margin-bottom: 4px;
    font-size: 13px;
    font-weight: 650;
  }

  .node-copy code,
  .parameter-title code,
  .parameter-meta code {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .node-copy code {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .node-controls {
    flex: 0 0 auto;
    gap: 8px;
  }

  .node-version {
    font-size: 12px;
    color: var(--art-gray-600);
  }

  .node-chevron {
    width: 18px;
    height: 18px;
    color: var(--art-gray-500);
    transition: transform 160ms ease;
  }

  .node-chevron.is-open {
    transform: rotate(180deg);
  }

  .node-config {
    padding: 12px 14px 14px;
    margin-bottom: 12px;
    background: var(--art-gray-100);
    border-left: 2px solid #147d78;
  }

  .node-config__heading {
    gap: 7px;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .node-config__heading strong {
    font-size: 12px;
    font-weight: 700;
  }

  .node-config__heading small {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    color: var(--art-gray-500);
  }

  .parameter-list {
    margin: 0;
  }

  .parameter-row {
    padding: 10px 0;
    border-top: 1px solid var(--art-gray-300);
  }

  .parameter-row dt {
    gap: 12px;
    justify-content: space-between;
  }

  .parameter-title {
    min-width: 0;
  }

  .parameter-title code,
  .parameter-description,
  .parameter-meta > span:last-child,
  .parameter-meta code {
    overflow-wrap: anywhere;
  }

  .parameter-tags {
    flex: 0 0 auto;
    flex-wrap: wrap;
    gap: 5px;
    justify-content: flex-end;
  }

  .parameter-row dd {
    margin-left: 0;
  }

  .parameter-description {
    margin-top: 7px;
    font-size: 12px;
    line-height: 1.6;
    color: var(--art-gray-600);
  }

  .parameter-meta {
    gap: 8px;
    align-items: flex-start;
    margin-top: 7px;
    font-size: 11px;
    line-height: 1.6;
    color: var(--art-gray-700);
  }

  .parameter-meta > span:first-child {
    flex: 0 0 auto;
    color: var(--art-gray-500);
  }

  .node-config__empty {
    margin: 0;
    font-size: 12px;
    color: var(--art-gray-500);
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
      padding: 0;
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

    .node-row,
    .parameter-row dt {
      gap: 10px;
    }

    .node-config {
      padding-right: 10px;
      padding-left: 10px;
    }
  }
</style>
