<template>
  <section class="paper-result" aria-labelledby="paper-result-title">
    <header class="paper-result__header">
      <div>
        <p>Paper ledger</p>
        <h2 id="paper-result-title">{{ view.name }}</h2>
      </div>
      <div class="paper-result__tools">
        <ElButton
          v-if="can('export')"
          circle
          title="导出结果"
          :loading="exporting"
          @click="download"
        >
          <ArtSvgIcon icon="ri:download-2-line" />
        </ElButton>
        <ElButton
          v-if="can('pause')"
          circle
          type="warning"
          plain
          title="暂停工作流"
          :loading="acting === 'pause'"
          @click="pauseWorkflow"
        >
          <ArtSvgIcon icon="ri:pause-circle-line" />
        </ElButton>
        <ElButton circle title="刷新" :loading="loading" @click="load">
          <ArtSvgIcon icon="ri:refresh-line" />
        </ElButton>
      </div>
    </header>

    <div v-if="account" class="ledger-strip" aria-label="Paper 账户摘要">
      <div
        ><span>现金</span><strong>{{ account.cashBalance }}</strong></div
      >
      <div
        ><span>权益</span><strong>{{ account.equity }}</strong></div
      >
      <div
        ><span>峰值</span><strong>{{ account.peakEquity }}</strong></div
      >
      <div
        ><span>日初权益</span><strong>{{ account.dayStartEquity }}</strong></div
      >
    </div>

    <section
      v-if="account?.positions.length"
      class="paper-section"
      aria-labelledby="position-title"
    >
      <div class="paper-section__heading">
        <h3 id="position-title">持仓</h3>
        <ElTag :type="account.status === 'active' ? 'success' : 'warning'" effect="plain">
          {{ account.status === 'active' ? '运行中' : '已暂停' }}
        </ElTag>
      </div>
      <ElTable :data="account.positions" size="small">
        <ElTableColumn label="品种" min-width="140">
          <template #default="scope">
            <strong>{{ scope.row.instrument }}</strong>
            <small>{{ scope.row.market }}</small>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="quantity" label="数量" min-width="130" />
        <ElTableColumn prop="averagePrice" label="均价" min-width="130" />
        <ElTableColumn prop="lastPrice" label="最新价" min-width="130" />
        <ElTableColumn label="更新时间" min-width="150">
          <template #default="scope">{{ formatTime(scope.row.updatedAt) }}</template>
        </ElTableColumn>
      </ElTable>
    </section>

    <section class="paper-section" aria-labelledby="batch-title">
      <div class="paper-section__heading">
        <h3 id="batch-title">最近执行</h3>
        <span>{{ batches.length }} 条</span>
      </div>
      <ol class="batch-list">
        <li v-for="batch in batches.slice(0, 8)" :key="batch.id">
          <div>
            <strong>#{{ batch.id }}</strong>
            <small
              >{{ batch.currentNodeInstanceId || batch.triggerType }} ·
              {{ formatTime(batch.triggeredAt) }}</small
            >
          </div>
          <ElTag :type="batchStatusType(batch.status)" effect="plain" size="small">
            {{ batchStatusLabel(batch.status) }}
          </ElTag>
          <div class="batch-list__actions">
            <ElButton
              v-if="batch.status === 'failed' && can('retry')"
              circle
              text
              title="重试批次"
              :loading="acting === batch.id"
              @click="actBatch(batch, 'retry')"
            >
              <ArtSvgIcon icon="ri:restart-line" />
            </ElButton>
            <ElButton
              v-if="cancellable(batch.status) && can('cancel')"
              circle
              text
              type="danger"
              title="取消批次"
              :loading="acting === batch.id"
              @click="actBatch(batch, 'cancel')"
            >
              <ArtSvgIcon icon="ri:stop-circle-line" />
            </ElButton>
          </div>
        </li>
        <li v-if="!batches.length" class="batch-list__empty">暂无执行记录</li>
      </ol>
    </section>

    <section class="paper-section" aria-labelledby="signal-title">
      <div class="paper-section__heading">
        <h3 id="signal-title">策略信号</h3>
        <span>{{ signals.length }} 条</span>
      </div>
      <ElTable
        v-loading="loading"
        :data="signals"
        class="signal-table"
        size="small"
        empty-text="暂无策略信号"
      >
        <ElTableColumn label="信号" min-width="170">
          <template #default="scope">
            <strong>{{ scope.row.instrument }}</strong>
            <small>{{ scope.row.strategyId }}@{{ scope.row.strategyVersion }}</small>
            <small v-if="scope.row.rejectionReason" class="signal-reason">
              {{ scope.row.rejectionReason }}
            </small>
          </template>
        </ElTableColumn>
        <ElTableColumn prop="target" label="目标仓位" min-width="110" />
        <ElTableColumn label="状态" width="104">
          <template #default="scope">
            <ElTag :type="statusType(scope.row.status)" effect="plain" size="small">
              {{ statusLabel(scope.row.status) }}
            </ElTag>
          </template>
        </ElTableColumn>
        <ElTableColumn label="评估时间" min-width="150">
          <template #default="scope">{{ formatTime(scope.row.evaluatedAt) }}</template>
        </ElTableColumn>
        <ElTableColumn width="96" align="right">
          <template #default="scope">
            <div v-if="scope.row.status === 'pending'" class="signal-actions">
              <ElButton
                v-if="can('reject')"
                circle
                text
                title="拒绝"
                :loading="deciding === scope.row.id"
                @click="decide(scope.row, 'reject')"
              >
                <ArtSvgIcon icon="ri:close-line" />
              </ElButton>
              <ElButton
                v-if="can('approve')"
                circle
                text
                type="success"
                title="批准"
                :loading="deciding === scope.row.id"
                @click="decide(scope.row, 'approve')"
              >
                <ArtSvgIcon icon="ri:check-line" />
              </ElButton>
            </div>
          </template>
        </ElTableColumn>
      </ElTable>

      <ol v-loading="loading" class="signal-mobile">
        <li v-for="signal in signals" :key="signal.id">
          <div class="signal-mobile__top">
            <strong>{{ signal.instrument }}</strong>
            <ElTag :type="statusType(signal.status)" effect="plain" size="small">
              {{ statusLabel(signal.status) }}
            </ElTag>
          </div>
          <small v-if="signal.rejectionReason" class="signal-reason">
            {{ signal.rejectionReason }}
          </small>
          <dl>
            <div
              ><dt>目标仓位</dt><dd>{{ signal.target }}</dd></div
            >
            <div
              ><dt>市场</dt><dd>{{ signal.market }}</dd></div
            >
            <div
              ><dt>UTC</dt><dd>{{ formatTime(signal.evaluatedAt) }}</dd></div
            >
          </dl>
          <div v-if="signal.status === 'pending'" class="signal-mobile__actions">
            <ElButton
              v-if="can('reject')"
              :loading="deciding === signal.id"
              @click="decide(signal, 'reject')"
              >拒绝</ElButton
            >
            <ElButton
              v-if="can('approve')"
              type="success"
              :loading="deciding === signal.id"
              @click="decide(signal, 'approve')"
              >批准</ElButton
            >
          </div>
        </li>
        <li v-if="!signals.length" class="signal-mobile__empty">暂无策略信号</li>
      </ol>
    </section>
  </section>
</template>

<script setup lang="ts">
  import {
    applyResultViewBatchAction,
    fetchResultViewBatches,
    pauseResultViewWorkflow,
    type ResultView,
    type ResultViewBatch,
    type ResultViewBatchStatus
  } from '@/api/resultViews'
  import { ElMessageBox } from 'element-plus'
  import {
    decidePaperSignal,
    exportPaperResult,
    fetchPaperResult,
    type PaperAccount,
    type PaperSignal,
    type PaperSignalStatus
  } from '@/api/paper'
  import { useAuth } from '@/hooks/core/useAuth'
  import { useUserStore } from '@/store/modules/user'

  const props = defineProps<{ view: ResultView }>()
  const { hasAuth } = useAuth()
  const userStore = useUserStore()
  const signals = ref<PaperSignal[]>([])
  const accounts = ref<PaperAccount[]>([])
  const batches = ref<ResultViewBatch[]>([])
  const loading = ref(false)
  const exporting = ref(false)
  const deciding = ref<number>()
  const acting = ref<number | 'pause'>()
  const account = computed(() => accounts.value[0])
  const permissions: Record<string, string> = {
    approve: 'result.views.approve',
    reject: 'result.views.reject',
    retry: 'result.views.retry',
    cancel: 'result.views.cancel',
    pause: 'result.views.pause',
    export: 'result.views.export'
  }
  const labels: Record<PaperSignalStatus, string> = {
    pending: '待审批',
    superseded: '已取代',
    approved: '已批准',
    rejected: '已拒绝',
    executed: '已执行'
  }

  const can = (action: string) =>
    props.view.allowedActions.includes(action) &&
    (userStore.info.roleCodes.includes('R_SUPER') || hasAuth(permissions[action]))
  const statusType = (status: PaperSignalStatus) =>
    ({
      pending: 'warning',
      superseded: 'info',
      approved: 'success',
      rejected: 'danger',
      executed: 'success'
    })[status] as 'warning' | 'info' | 'success' | 'danger'
  const statusLabel = (status: PaperSignalStatus) => labels[status]
  const batchStatusLabel = (status: ResultViewBatchStatus) =>
    ({
      queued: '排队中',
      running: '运行中',
      waiting: '等待审批',
      retrying: '重试中',
      succeeded: '已成功',
      failed: '失败',
      cancelled: '已取消'
    })[status]
  const batchStatusType = (status: ResultViewBatchStatus) =>
    ({
      queued: 'info',
      running: 'warning',
      waiting: 'warning',
      retrying: 'warning',
      succeeded: 'success',
      failed: 'danger',
      cancelled: 'info'
    })[status] as 'info' | 'warning' | 'success' | 'danger'
  const cancellable = (status: ResultViewBatchStatus) =>
    ['queued', 'running', 'waiting', 'retrying'].includes(status)
  const formatTime = (value: string) =>
    new Intl.DateTimeFormat('zh-CN', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZone: 'UTC'
    }).format(new Date(value))

  const load = async () => {
    loading.value = true
    try {
      const [result, batchResult] = await Promise.all([
        fetchPaperResult(props.view.id),
        fetchResultViewBatches(props.view.id)
      ])
      signals.value = result.signals
      accounts.value = result.accounts
      batches.value = batchResult.items
    } finally {
      loading.value = false
    }
  }
  const actBatch = async (batch: ResultViewBatch, action: 'retry' | 'cancel') => {
    if (action === 'cancel') {
      await ElMessageBox.confirm('取消后当前批次不会继续执行。', '取消批次', {
        confirmButtonText: '取消批次',
        cancelButtonText: '返回',
        type: 'warning'
      })
    }
    acting.value = batch.id
    try {
      await applyResultViewBatchAction(props.view.id, batch.id, action)
      await load()
    } finally {
      acting.value = undefined
    }
  }
  const pauseWorkflow = async () => {
    await ElMessageBox.confirm(
      '暂停后不再接收新的触发，当前动作完成后从检查点停止。',
      '暂停工作流',
      {
        confirmButtonText: '暂停',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    acting.value = 'pause'
    try {
      await pauseResultViewWorkflow(props.view.id)
      ElMessage.success('工作流已暂停')
      await load()
    } finally {
      acting.value = undefined
    }
  }
  const decide = async (signal: PaperSignal, action: 'approve' | 'reject') => {
    if (action === 'reject') {
      await ElMessageBox.confirm('拒绝后该信号不能再次执行。', '拒绝信号', {
        confirmButtonText: '拒绝',
        cancelButtonText: '取消',
        type: 'warning'
      })
    }
    deciding.value = signal.id
    try {
      await decidePaperSignal(props.view.id, signal.id, action)
      ElMessage.success(action === 'approve' ? '已批准' : '已拒绝')
      await load()
    } finally {
      deciding.value = undefined
    }
  }
  const download = async () => {
    exporting.value = true
    try {
      const blob = await exportPaperResult(props.view.id)
      const link = document.createElement('a')
      link.href = URL.createObjectURL(blob)
      link.download = `paper-result-${props.view.id}.json`
      link.click()
      URL.revokeObjectURL(link.href)
    } finally {
      exporting.value = false
    }
  }

  watch(() => props.view.id, load, { immediate: true })
</script>

<style scoped>
  .paper-result {
    min-width: 0;
    color: var(--el-text-color-primary);
    letter-spacing: 0;
  }

  .paper-result__header,
  .paper-section__heading,
  .signal-mobile__top,
  .signal-actions,
  .paper-result__tools {
    display: flex;
    align-items: center;
  }

  .paper-result__header,
  .paper-section__heading,
  .signal-mobile__top {
    justify-content: space-between;
  }

  .paper-result__header {
    min-height: 52px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--el-border-color);
  }

  .paper-result__header p,
  .paper-result__header h2,
  .paper-section h3 {
    margin: 0;
  }

  .signal-reason {
    color: var(--el-color-danger) !important;
  }

  .paper-result__header p,
  .paper-result small,
  .paper-section__heading > span,
  .ledger-strip span {
    display: block;
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .paper-result__header h2 {
    margin-top: 3px;
    font-size: 20px;
    font-weight: 650;
  }

  .paper-result__tools,
  .signal-actions {
    gap: 4px;
  }

  .ledger-strip {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    margin: 14px 0 20px;
    border-block: 1px solid var(--el-border-color-lighter);
  }

  .ledger-strip > div {
    min-width: 0;
    padding: 12px 14px;
    border-right: 1px solid var(--el-border-color-lighter);
  }

  .ledger-strip > div:last-child {
    border-right: 0;
  }

  .ledger-strip strong,
  .paper-result :deep(.el-table__cell:nth-child(2)),
  .paper-result :deep(.el-table__cell:nth-child(3)),
  .paper-result :deep(.el-table__cell:nth-child(4)) {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  }

  .ledger-strip strong {
    display: block;
    margin-top: 4px;
    overflow: hidden;
    font-size: 15px;
    text-overflow: ellipsis;
  }

  .paper-section {
    margin-top: 22px;
  }

  .paper-section__heading {
    min-height: 32px;
    margin-bottom: 8px;
  }

  .paper-section h3 {
    font-size: 14px;
    font-weight: 650;
  }

  .signal-mobile {
    display: none;
  }

  .batch-list {
    padding: 0;
    margin: 0;
    list-style: none;
    border-top: 1px solid var(--el-border-color-lighter);
  }

  .batch-list > li {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto 68px;
    gap: 10px;
    align-items: center;
    min-height: 54px;
    border-bottom: 1px solid var(--el-border-color-lighter);
  }

  .batch-list strong,
  .batch-list small {
    display: block;
  }

  .batch-list__actions {
    display: flex;
    justify-content: flex-end;
    min-width: 68px;
  }

  .batch-list__empty {
    display: block !important;
    padding: 24px 0;
    color: var(--el-text-color-secondary);
    text-align: center;
  }

  @media (max-width: 700px) {
    .ledger-strip {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .ledger-strip > div:nth-child(2) {
      border-right: 0;
    }

    .signal-table {
      display: none;
    }

    .signal-mobile {
      display: grid;
      gap: 0;
      padding: 0;
      margin: 0;
      list-style: none;
      border-top: 1px solid var(--el-border-color-lighter);
    }

    .signal-mobile > li {
      padding: 14px 0;
      border-bottom: 1px solid var(--el-border-color-lighter);
    }

    .signal-mobile dl {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 8px;
      margin: 12px 0 0;
    }

    .signal-mobile dl div {
      min-width: 0;
    }

    .signal-mobile dt {
      font-size: 11px;
      color: var(--el-text-color-secondary);
    }

    .signal-mobile dd {
      margin: 3px 0 0;
      overflow: hidden;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 12px;
      text-overflow: ellipsis;
    }

    .signal-mobile__actions {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
      margin-top: 12px;
    }

    .signal-mobile__empty {
      color: var(--el-text-color-secondary);
      text-align: center;
    }

    .batch-list > li {
      grid-template-columns: minmax(0, 1fr) auto;
      padding: 8px 0;
    }

    .batch-list__actions {
      grid-column: 2;
    }
  }
</style>
