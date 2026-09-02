<template>
  <div class="plugin-page art-full-height" v-loading="loading">
    <ArtSearchBar
      v-model="formFilters"
      :items="formItems"
      :show-expand="false"
      @reset="handleReset"
      @search="handleSearch"
    />

    <div class="plugin-toolbar">
      <div class="plugin-summary" aria-live="polite">
        <strong>{{ filteredPlugins.length }}</strong>
        <span>个插件</span>
        <i></i>
        <span>{{ filteredNodeCount }} 个节点</span>
        <i></i>
        <span>{{ filteredPageCount }} 个页面</span>
      </div>
      <ElTooltip content="刷新插件列表" placement="top">
        <ElButton
          circle
          :icon="Refresh"
          :loading="loading"
          aria-label="刷新插件列表"
          @click="loadPlugins"
        />
      </ElTooltip>
    </div>

    <section v-if="filteredPlugins.length" class="plugin-grid" aria-label="已加载插件">
      <article
        v-for="plugin in filteredPlugins"
        :key="plugin.id"
        class="plugin-card"
        role="button"
        tabindex="0"
        :style="pluginStyle(plugin)"
        :aria-label="`查看${pluginLabel(plugin)}详情`"
        @click="openPlugin(plugin)"
        @keydown.enter="openPlugin(plugin)"
        @keydown.space.prevent="openPlugin(plugin)"
      >
        <header class="plugin-card__header">
          <span class="plugin-mark">
            <ArtSvgIcon :icon="pluginVisual(plugin).icon" />
          </span>
          <div class="plugin-card__title">
            <h2>{{ pluginLabel(plugin) }}</h2>
            <span>第 {{ plugin.version }} 版</span>
          </div>
          <ElTag type="success" effect="plain" size="small">运行中</ElTag>
        </header>

        <dl class="plugin-metrics">
          <div>
            <dt>节点</dt>
            <dd>{{ plugin.nodes.length }}</dd>
          </div>
          <div>
            <dt>页面</dt>
            <dd>{{ plugin.pages.length }}</dd>
          </div>
          <div>
            <dt>能力</dt>
            <dd>{{ plugin.contributes.length }}</dd>
          </div>
        </dl>

        <div class="plugin-capabilities">
          <span class="plugin-capabilities__label">提供能力</span>
          <div>
            <span v-for="item in contributionPreview(plugin)" :key="item">
              <ArtSvgIcon :icon="contributionIcon(item)" />
              {{ contributionLabel(item) }}
            </span>
            <span v-if="hiddenContributionCount(plugin)" class="capability-more">
              另有 {{ hiddenContributionCount(plugin) }} 项
            </span>
          </div>
        </div>

        <footer class="plugin-card__footer">
          <span>查看详情</span>
          <ArtSvgIcon icon="ri:arrow-right-line" />
        </footer>
      </article>
    </section>

    <ElEmpty v-else-if="!loading" description="没有符合条件的插件" />

    <ElDrawer
      v-model="detailVisible"
      :title="selectedPlugin ? `${pluginLabel(selectedPlugin)}详情` : '插件详情'"
      size="min(620px, 96vw)"
    >
      <div v-if="selectedPlugin" class="plugin-detail" :style="pluginStyle(selectedPlugin)">
        <div class="plugin-detail__summary">
          <span class="plugin-mark">
            <ArtSvgIcon :icon="pluginVisual(selectedPlugin).icon" />
          </span>
          <div>
            <strong>{{ pluginLabel(selectedPlugin) }}</strong>
            <span>第 {{ selectedPlugin.version }} 版</span>
          </div>
          <ElTag type="success" effect="plain">运行正常</ElTag>
        </div>

        <dl class="detail-metrics">
          <div>
            <dt>节点数量</dt>
            <dd>{{ selectedPlugin.nodes.length }}</dd>
          </div>
          <div>
            <dt>页面数量</dt>
            <dd>{{ selectedPlugin.pages.length }}</dd>
          </div>
          <div>
            <dt>能力类型</dt>
            <dd>{{ selectedPlugin.contributes.length }}</dd>
          </div>
        </dl>

        <section class="detail-capabilities">
          <h3>扩展能力</h3>
          <div>
            <span v-for="item in selectedPlugin.contributes" :key="item">
              <ArtSvgIcon :icon="contributionIcon(item)" />
              {{ contributionLabel(item) }}
            </span>
          </div>
        </section>

        <ElTabs v-model="detailTab" class="plugin-detail__tabs">
          <ElTabPane name="nodes">
            <template #label>
              <span class="detail-tab-label">
                注册节点
                <small>{{ selectedPlugin.nodes.length }}</small>
              </span>
            </template>

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
                    <strong>{{ localizeText(node.title, '扩展节点') }}</strong>
                    <span>第 {{ node.version }} 版</span>
                  </span>
                  <span class="node-controls">
                    <ElTag size="small" effect="plain">{{ nodeKindLabel(node.kind) }}</ElTag>
                    <ArtSvgIcon
                      icon="ri:arrow-down-s-line"
                      :class="['node-chevron', { 'is-open': expandedNodeType === node.type }]"
                    />
                  </span>
                </button>

                <div v-if="expandedNodeType === node.type" class="node-config">
                  <div class="node-config__heading">
                    <strong>配置参数</strong>
                    <small>共 {{ configFields(node).length }} 项</small>
                  </div>
                  <dl v-if="configFields(node).length" class="parameter-list">
                    <div
                      v-for="([fieldName, schema], index) in configFields(node)"
                      :key="fieldName"
                      class="parameter-row"
                    >
                      <dt>
                        <strong>{{ schemaTitle(schema, index) }}</strong>
                        <span class="parameter-tags">
                          <ElTag size="small" effect="plain">
                            {{ schemaTypeLabel(schema.type) }}
                          </ElTag>
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
                      <dd v-if="localizeText(schema.description)" class="parameter-description">
                        {{ localizeText(schema.description) }}
                      </dd>
                      <dd v-if="schemaDefault(schema)" class="parameter-meta">
                        <span>默认值</span>
                        <span>{{ schemaDefault(schema) }}</span>
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
            <ElEmpty v-else :image-size="64" description="未注册节点" />
          </ElTabPane>

          <ElTabPane name="pages">
            <template #label>
              <span class="detail-tab-label">
                注册页面
                <small>{{ selectedPlugin.pages.length }}</small>
              </span>
            </template>

            <div v-if="selectedPlugin.pages.length" class="detail-list">
              <div
                v-for="page in selectedPlugin.pages"
                :key="`${page.kind}:${page.pageKey}`"
                class="detail-row"
              >
                <strong>{{ localizeText(page.title, '扩展页面') }}</strong>
                <ElTag size="small" effect="plain">{{ pageKindLabel(page.kind) }}</ElTag>
              </div>
            </div>
            <ElEmpty v-else :image-size="64" description="未注册页面" />
          </ElTabPane>
        </ElTabs>
      </div>
    </ElDrawer>
  </div>
</template>

<script setup lang="ts">
  import { Refresh } from '@element-plus/icons-vue'
  import { fetchGetInstalledPlugins } from '@/api/system'

  defineOptions({ name: 'Plugins' })

  type InstalledPlugin = Api.System.InstalledPlugin
  type PluginNode = InstalledPlugin['nodes'][number]
  type ConfigSchema = Record<string, any>

  const loading = ref(false)
  const plugins = ref<InstalledPlugin[]>([])
  const selectedPlugin = ref<InstalledPlugin>()
  const detailVisible = ref(false)
  const expandedNodeType = ref<string>()
  const detailTab = ref('nodes')

  const initialFilters = {
    keyword: '',
    contribution: ''
  }
  const formFilters = reactive({ ...initialFilters })
  const appliedFilters = reactive({ ...initialFilters })

  const pluginNames: Record<string, string> = {
    'official.ai': '人工智能',
    'official.binance': '币安',
    'official.connector': '连接器',
    'official.notification': '通知',
    'official.qq': '消息机器人',
    'official.quant': '量化'
  }

  const pluginVisuals: Record<string, { icon: string; color: string; background: string }> = {
    'official.ai': {
      icon: 'ri:robot-2-line',
      color: '#2563eb',
      background: 'rgb(37 99 235 / 0.1)'
    },
    'official.binance': {
      icon: 'ri:exchange-dollar-line',
      color: '#b45309',
      background: 'rgb(180 83 9 / 0.1)'
    },
    'official.connector': {
      icon: 'ri:links-line',
      color: '#0f766e',
      background: 'rgb(15 118 110 / 0.1)'
    },
    'official.notification': {
      icon: 'ri:notification-3-line',
      color: '#7c3aed',
      background: 'rgb(124 58 237 / 0.1)'
    },
    'official.qq': {
      icon: 'ri:message-3-line',
      color: '#0369a1',
      background: 'rgb(3 105 161 / 0.1)'
    },
    'official.quant': {
      icon: 'ri:line-chart-line',
      color: '#be123c',
      background: 'rgb(190 18 60 / 0.1)'
    }
  }
  const defaultPluginVisual = {
    icon: 'ri:puzzle-2-line',
    color: '#475569',
    background: 'rgb(71 85 105 / 0.1)'
  }

  const contributionLabels: Record<string, string> = {
    nodes: '工作流节点',
    triggers: '流程触发',
    strategies: '量化策略',
    marketDataProviders: '行情服务',
    executionProviders: '交易执行',
    apiRoutes: '接口服务',
    pages: '管理页面',
    resultPages: '结果页面',
    assistantQueries: '助手查询',
    workflowValidators: '工作流校验',
    templates: '工作流模板',
    migrations: '数据升级'
  }

  const contributionIcons: Record<string, string> = {
    nodes: 'ri:node-tree',
    triggers: 'ri:flashlight-line',
    strategies: 'ri:stock-line',
    marketDataProviders: 'ri:line-chart-line',
    executionProviders: 'ri:exchange-line',
    apiRoutes: 'ri:route-line',
    pages: 'ri:layout-grid-line',
    resultPages: 'ri:file-chart-line',
    assistantQueries: 'ri:question-answer-line',
    workflowValidators: 'ri:shield-check-line',
    templates: 'ri:file-copy-2-line',
    migrations: 'ri:database-2-line'
  }

  const textReplacements: Array<[RegExp, string]> = [
    [/Binance/gi, '币安'],
    [/K\s*线/gi, '蜡烛图'],
    [/WebSocket/gi, '长连接'],
    [/Webhook/gi, '网络回调'],
    [/TLS\s*SMTP/gi, '安全邮件协议'],
    [/HTTP\s*请求/gi, '网络请求'],
    [/HTTP/gi, '网络协议'],
    [/Paper/gi, '模拟交易'],
    [/AI/gi, '智能模型'],
    [/QQ/gi, '即时通信'],
    [/MACD/gi, '指数平滑异同移动平均线'],
    [/KDJ/gi, '随机指标'],
    [/RSI/gi, '相对强弱指标'],
    [/SMA/gi, '简单移动平均线'],
    [/REST/gi, '接口'],
    [/UTC/gi, '协调世界时'],
    [/API/gi, '接口'],
    [/URL/gi, '地址'],
    [/JSON/gi, '结构化数据'],
    [/CloudEvent/gi, '云事件'],
    [/Cron/gi, '定时表达式'],
    [/\bID\b/gi, '标识']
  ]

  const schemaTypeLabels: Record<string, string> = {
    string: '文本',
    integer: '整数',
    number: '数值',
    boolean: '布尔值',
    array: '列表',
    object: '对象'
  }

  const valueLabels: Record<string, string> = {
    binance: '币安',
    spot: '现货',
    usdm: '合约',
    open: '开仓',
    reduce: '减仓',
    buy: '买入',
    sell: '卖出',
    hold: '观望',
    true: '是',
    false: '否'
  }

  const pluginVisual = (plugin: InstalledPlugin) => pluginVisuals[plugin.id] || defaultPluginVisual
  const pluginStyle = (plugin: InstalledPlugin) => ({
    '--plugin-color': pluginVisual(plugin).color,
    '--plugin-background': pluginVisual(plugin).background
  })
  const localizeText = (value: unknown, fallback = '') => {
    let result = String(value || '').trim()
    textReplacements.forEach(([pattern, replacement]) => {
      result = result.replace(pattern, replacement)
    })
    result = result.replace(/\s+/g, ' ')
    return /[A-Za-z]/.test(result) ? fallback : result
  }
  const pluginLabel = (plugin: InstalledPlugin) =>
    pluginNames[plugin.id] || localizeText(plugin.name, '扩展插件')
  const contributionLabel = (value: string) => contributionLabels[value] || '其他能力'
  const contributionIcon = (value: string) => contributionIcons[value] || 'ri:add-circle-line'
  const contributionPreview = (plugin: InstalledPlugin) => plugin.contributes.slice(0, 4)
  const hiddenContributionCount = (plugin: InstalledPlugin) =>
    Math.max(plugin.contributes.length - 4, 0)
  const nodeKindLabel = (kind: 'action' | 'trigger') => (kind === 'trigger' ? '触发器' : '动作')
  const pageKindLabel = (kind: 'page' | 'resultPage') =>
    kind === 'resultPage' ? '结果页面' : '管理页面'

  const contributionOptions = computed(() => {
    const values = new Set(plugins.value.flatMap((plugin) => plugin.contributes))
    return Array.from(values)
      .map((value) => ({ value, label: contributionLabel(value) }))
      .sort((left, right) => left.label.localeCompare(right.label, 'zh-CN'))
  })

  const formItems = computed(() => [
    {
      label: '插件名称',
      key: 'keyword',
      type: 'input',
      props: { clearable: true, placeholder: '请输入插件名称' }
    },
    {
      label: '能力类型',
      key: 'contribution',
      type: 'select',
      props: {
        clearable: true,
        placeholder: '请选择能力类型',
        options: contributionOptions.value
      }
    }
  ])

  const filteredPlugins = computed(() => {
    const keyword = appliedFilters.keyword.trim()
    return plugins.value.filter((plugin) => {
      const matchesKeyword = !keyword || pluginLabel(plugin).includes(keyword)
      const matchesContribution =
        !appliedFilters.contribution || plugin.contributes.includes(appliedFilters.contribution)
      return matchesKeyword && matchesContribution
    })
  })
  const filteredNodeCount = computed(() =>
    filteredPlugins.value.reduce((total, plugin) => total + plugin.nodes.length, 0)
  )
  const filteredPageCount = computed(() =>
    filteredPlugins.value.reduce((total, plugin) => total + plugin.pages.length, 0)
  )

  const configFields = (node: PluginNode) =>
    Object.entries(node.configSchema?.properties || {}) as Array<[string, ConfigSchema]>
  const isConfigRequired = (node: PluginNode, fieldName: string) =>
    Array.isArray(node.configSchema?.required) && node.configSchema.required.includes(fieldName)
  const schemaTitle = (schema: ConfigSchema, index: number) =>
    localizeText(schema.title, `参数 ${index + 1}`)
  const schemaTypeLabel = (value: unknown) =>
    Array.isArray(value)
      ? value.map((item) => schemaTypeLabels[String(item)] || '任意').join('、')
      : schemaTypeLabels[String(value)] || '任意'
  const formatDisplayValue = (value: unknown): string => {
    if (typeof value === 'boolean') return value ? '是' : '否'
    if (typeof value === 'number') return String(value)
    if (typeof value === 'string') {
      const normalized = value.trim()
      if (valueLabels[normalized]) return valueLabels[normalized]

      const interval = /^(\d+)(m|h|d|w)$/.exec(normalized)
      if (interval) {
        const units = { m: '分钟', h: '小时', d: '天', w: '周' }
        return interval[1] + ' ' + units[interval[2] as keyof typeof units]
      }

      return localizeText(normalized)
    }
    if (Array.isArray(value)) {
      return value
        .map((item) => formatDisplayValue(item))
        .filter(Boolean)
        .join('、')
    }
    return ''
  }
  const schemaDefault = (schema: ConfigSchema) =>
    Object.prototype.hasOwnProperty.call(schema, 'default')
      ? formatDisplayValue(schema.default)
      : ''
  const schemaOptions = (schema: ConfigSchema) => {
    if (!Array.isArray(schema.enum)) return ''
    const optionLabels = Array.isArray(schema.enumLabels) ? schema.enumLabels : []
    return schema.enum
      .map((value: unknown, index: number) =>
        formatDisplayValue(optionLabels[index] === undefined ? value : optionLabels[index])
      )
      .filter(Boolean)
      .join('、')
  }
  const toggleNode = (nodeType: string) => {
    expandedNodeType.value = expandedNodeType.value === nodeType ? undefined : nodeType
  }
  const openPlugin = (plugin: InstalledPlugin) => {
    selectedPlugin.value = plugin
    expandedNodeType.value = undefined
    detailTab.value = plugin.nodes.length ? 'nodes' : 'pages'
    detailVisible.value = true
  }
  const handleSearch = (filters: Record<string, string>) => {
    Object.assign(appliedFilters, initialFilters, filters)
  }
  const handleReset = () => {
    Object.assign(formFilters, initialFilters)
    Object.assign(appliedFilters, initialFilters)
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
    color: var(--art-gray-900);
  }

  .plugin-toolbar,
  .plugin-summary,
  .plugin-card__header,
  .plugin-capabilities > div,
  .plugin-card__footer,
  .plugin-detail__summary,
  .detail-capabilities > div,
  .detail-tab-label,
  .node-row,
  .node-controls,
  .parameter-row dt,
  .parameter-tags,
  .node-config__heading,
  .parameter-meta,
  .detail-row {
    display: flex;
    align-items: center;
  }

  .plugin-toolbar {
    justify-content: space-between;
    min-height: 54px;
    padding: 0 4px;
  }

  .plugin-summary {
    gap: 9px;
    font-size: 13px;
    color: var(--art-gray-600);
  }

  .plugin-summary strong {
    font-size: 18px;
    color: var(--art-gray-900);
  }

  .plugin-summary i {
    width: 1px;
    height: 12px;
    background: var(--art-gray-300);
  }

  .plugin-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
  }

  .plugin-card {
    position: relative;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 262px;
    padding: 18px;
    cursor: pointer;
    background: var(--default-box-color);
    border: 1px solid var(--art-card-border);
    border-radius: 8px;
    transition:
      border-color 160ms ease,
      box-shadow 160ms ease,
      transform 160ms ease;
  }

  .plugin-card::before {
    position: absolute;
    top: 0;
    right: 18px;
    left: 18px;
    height: 2px;
    content: '';
    background: var(--plugin-color);
    border-radius: 0 0 2px 2px;
    opacity: 0;
    transition: opacity 160ms ease;
  }

  .plugin-card:hover,
  .plugin-card:focus-visible {
    border-color: var(--plugin-color);
    outline: none;
    box-shadow: 0 6px 18px rgb(15 23 42 / 0.07);
    transform: translateY(-1px);
  }

  .plugin-card:hover::before,
  .plugin-card:focus-visible::before {
    opacity: 1;
  }

  .plugin-card__header {
    gap: 12px;
  }

  .plugin-mark {
    display: grid;
    flex: 0 0 auto;
    width: 42px;
    height: 42px;
    font-size: 19px;
    color: var(--plugin-color);
    background: var(--plugin-background);
    border-radius: 7px;
    place-items: center;
  }

  .plugin-card__title {
    min-width: 0;
    margin-right: auto;
  }

  .plugin-card__title h2 {
    margin: 0;
    overflow: hidden;
    font-size: 15px;
    line-height: 1.5;
    text-overflow: ellipsis;
    letter-spacing: 0;
    white-space: nowrap;
  }

  .plugin-card__title span {
    display: block;
    margin-top: 2px;
    font-size: 12px;
    color: var(--art-gray-500);
  }

  .plugin-metrics,
  .detail-metrics {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    margin: 18px 0 0;
  }

  .plugin-metrics {
    padding: 12px 0;
    background: var(--art-gray-100);
    border-radius: 6px;
  }

  .plugin-metrics div,
  .detail-metrics div {
    text-align: center;
    border-right: 1px solid var(--art-gray-300);
  }

  .plugin-metrics div:last-child,
  .detail-metrics div:last-child {
    border-right: 0;
  }

  .plugin-metrics dt,
  .detail-metrics dt {
    font-size: 11px;
    color: var(--art-gray-500);
  }

  .plugin-metrics dd,
  .detail-metrics dd {
    margin: 4px 0 0;
    font-size: 16px;
    font-weight: 600;
    color: var(--art-gray-900);
  }

  .plugin-capabilities {
    padding: 15px 0;
  }

  .plugin-capabilities__label,
  .detail-capabilities h3 {
    display: block;
    margin-bottom: 9px;
    font-size: 12px;
    font-weight: 600;
    color: var(--art-gray-700);
  }

  .plugin-capabilities > div,
  .detail-capabilities > div {
    flex-wrap: wrap;
    gap: 7px;
  }

  .plugin-capabilities > div > span,
  .detail-capabilities > div > span {
    display: inline-flex;
    gap: 5px;
    align-items: center;
    padding: 4px 7px;
    font-size: 11px;
    line-height: 1.4;
    color: var(--art-gray-700);
    background: var(--art-gray-200);
    border-radius: 4px;
  }

  .plugin-capabilities .capability-more {
    color: var(--plugin-color);
    background: var(--plugin-background);
  }

  .plugin-card__footer {
    justify-content: space-between;
    padding-top: 12px;
    margin-top: auto;
    font-size: 12px;
    color: var(--art-gray-500);
    border-top: 1px solid var(--art-gray-300);
  }

  .plugin-card:hover .plugin-card__footer,
  .plugin-card:focus-visible .plugin-card__footer {
    color: var(--plugin-color);
  }

  .plugin-detail__summary {
    gap: 12px;
    padding-bottom: 18px;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .plugin-detail__summary > div {
    min-width: 0;
    margin-right: auto;
  }

  .plugin-detail__summary strong,
  .plugin-detail__summary span {
    display: block;
  }

  .plugin-detail__summary strong {
    font-size: 15px;
  }

  .plugin-detail__summary > div > span {
    margin-top: 3px;
    font-size: 12px;
    color: var(--art-gray-500);
  }

  .detail-metrics {
    padding: 17px 0;
    border-bottom: 1px solid var(--art-gray-300);
  }

  .detail-capabilities {
    padding: 18px 0 12px;
  }

  .detail-capabilities h3 {
    margin-top: 0;
  }

  .plugin-detail__tabs {
    margin-top: 8px;
  }

  .detail-tab-label {
    gap: 6px;
  }

  .detail-tab-label small {
    min-width: 20px;
    padding: 0 5px;
    font-size: 10px;
    line-height: 18px;
    color: var(--art-gray-600);
    text-align: center;
    background: var(--art-gray-200);
    border-radius: 9px;
  }

  .detail-list {
    border-top: 1px solid var(--art-gray-300);
  }

  .node-entry,
  .detail-row {
    border-bottom: 1px solid var(--art-gray-300);
  }

  .node-row {
    gap: 16px;
    justify-content: space-between;
    width: 100%;
    min-height: 64px;
    padding: 10px 4px;
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
  .node-copy span {
    display: block;
  }

  .node-copy strong,
  .detail-row strong {
    font-size: 13px;
    font-weight: 600;
  }

  .node-copy span {
    margin-top: 4px;
    font-size: 11px;
    color: var(--art-gray-500);
  }

  .node-controls {
    flex: 0 0 auto;
    gap: 8px;
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
    padding: 13px 14px 14px;
    margin-bottom: 12px;
    background: var(--art-gray-100);
    border-left: 2px solid var(--plugin-color, var(--theme-color));
  }

  .node-config__heading {
    justify-content: space-between;
    margin-bottom: 9px;
  }

  .node-config__heading strong {
    font-size: 12px;
  }

  .node-config__heading small {
    font-size: 11px;
    color: var(--art-gray-500);
  }

  .parameter-list {
    margin: 0;
  }

  .parameter-row {
    padding: 11px 0;
    border-top: 1px solid var(--art-gray-300);
  }

  .parameter-row dt {
    gap: 12px;
    justify-content: space-between;
  }

  .parameter-row dt > strong {
    font-size: 12px;
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

  .parameter-description,
  .parameter-meta {
    margin-top: 7px;
    font-size: 12px;
    line-height: 1.6;
    color: var(--art-gray-600);
  }

  .parameter-description {
    overflow-wrap: anywhere;
  }

  .parameter-meta {
    gap: 8px;
    align-items: flex-start;
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

  .detail-row {
    gap: 16px;
    justify-content: space-between;
    min-height: 58px;
    padding: 10px 4px;
  }

  @media (width <= 1180px) {
    .plugin-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (width <= 700px) {
    .plugin-grid {
      grid-template-columns: 1fr;
    }

    .plugin-toolbar {
      align-items: flex-start;
      padding: 9px 2px;
    }

    .plugin-summary {
      flex-wrap: wrap;
    }

    .plugin-card {
      min-height: 0;
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

  @media (prefers-reduced-motion: reduce) {
    .plugin-card,
    .plugin-card::before,
    .node-chevron {
      transition: none;
    }

    .plugin-card:hover,
    .plugin-card:focus-visible {
      transform: none;
    }
  }
</style>
