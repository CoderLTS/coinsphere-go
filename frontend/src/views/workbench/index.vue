<template>
  <main v-loading="loading" class="workbench">
    <header
      ><div><h1>工作台</h1><p>查看你的工作流、待办与共享结果</p></div
      ><ElButton @click="load">刷新</ElButton></header
    >
    <ElAlert v-if="error" :title="error" type="error" :closable="false" />
    <section v-if="hasAuth('human_tasks.read')"
      ><h2>待办</h2
      ><ElTable :data="data.tasks || []" empty-text="暂无待处理事项"
        ><ElTableColumn prop="prompt" label="事项" min-width="220" /><ElTableColumn
          prop="expiresAt"
          label="截止时间"
          min-width="200"
        /><ElTableColumn label="操作" min-width="160"
          ><template #default="{ row }"
            ><ElSpace
              ><ElButton
                v-if="hasAuth('workflows.read')"
                link
                @click="router.push(`/scheduler/execution/${row.runId}/detail`)"
                >查看运行</ElButton
              ><ElButton
                v-if="hasAuth('human_tasks.decide')"
                link
                type="primary"
                @click="decide(row.id, 'approve')"
                >通过</ElButton
              ><ElButton
                v-if="hasAuth('human_tasks.decide')"
                link
                type="danger"
                @click="decide(row.id, 'reject')"
                >拒绝</ElButton
              ></ElSpace
            ></template
          ></ElTableColumn
        ></ElTable
      ></section
    >
    <section v-if="hasAuth('workflows.read')"
      ><div class="section-title"
        ><h2>工作流</h2
        ><ElButton link @click="router.push('/scheduler/definition')">查看全部</ElButton></div
      ><ElTable :data="data.workflows?.records || []" empty-text="暂无获授权工作流"
        ><ElTableColumn label="名称"
          ><template #default="{ row }"
            ><ElButton link @click="router.push(`/scheduler/workflow/${row.id}/edit`)">{{
              row.name
            }}</ElButton></template
          ></ElTableColumn
        ><ElTableColumn label="最近运行"
          ><template #default="{ row }">{{
            runStatusLabel(row.latestRunStatus)
          }}</template></ElTableColumn
        ></ElTable
      ></section
    >
    <section v-if="hasAuth('result_views.read')"
      ><div class="section-title"
        ><h2>共享结果</h2
        ><ElButton link @click="router.push('/results')">打开结果中心</ElButton></div
      ><p>{{ data.resultViews?.length || 0 }} 个获授权视图</p></section
    >
    <ElEmpty
      v-if="
        !hasAuth('workflows.read') && !hasAuth('human_tasks.read') && !hasAuth('result_views.read')
      "
      description="暂无获授权内容，请联系管理员分配能力与资源范围"
    />
  </main>
</template>
<script setup lang="ts">
  import { ElMessageBox } from 'element-plus'
  import { fetchWorkbench, decideWorkflowHumanTask } from '@/api/workflows'
  import { useAuth } from '@/hooks/core/useAuth'
  import { runStatusLabel } from '@/components/workflow/status'
  const router = useRouter(),
    { hasAuth } = useAuth(),
    data = ref<Awaited<ReturnType<typeof fetchWorkbench>>>({}),
    loading = ref(false),
    error = ref('')
  const load = async () => {
    loading.value = true
    error.value = ''
    try {
      data.value = await fetchWorkbench()
    } catch (cause: any) {
      error.value = cause.message
    } finally {
      loading.value = false
    }
  }
  const decide = async (id: number, action: 'approve' | 'reject') => {
    await ElMessageBox.confirm(`${action === 'approve' ? '通过' : '拒绝'}此待办？`, '处理待办')
    await decideWorkflowHumanTask(id, action)
    await load()
  }
  onMounted(load)
</script>
<style scoped>
  .workbench {
    padding: 24px;
    color: var(--el-text-color-primary);
  }
  header,
  .section-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }
  h1 {
    font-size: 26px;
    margin: 0 0 8px;
  }
  h2 {
    font-size: 18px;
  }
  p {
    color: var(--el-text-color-secondary);
  }
  section {
    padding: 20px 0;
    border-bottom: 1px solid var(--el-border-color);
  }
</style>
