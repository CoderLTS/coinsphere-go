<template>
  <section class="contract-result" aria-labelledby="contract-result-title">
    <header class="contract-result__header">
      <div>
        <p class="contract-result__label">Plugin contract</p>
        <h2 id="contract-result-title">Contract run</h2>
      </div>
      <span class="contract-result__status">Accepted</span>
    </header>

    <dl class="contract-result__trace">
      <div>
        <dt>Input</dt>
        <dd>{{ result.input }}</dd>
      </div>
      <div>
        <dt>Action</dt>
        <dd>{{ result.action }}</dd>
      </div>
      <div>
        <dt>Output</dt>
        <dd>{{ result.output }}</dd>
      </div>
    </dl>

    <footer>
      <span>Operation</span>
      <code>{{ result.operationKey }}</code>
    </footer>
  </section>
</template>

<script setup lang="ts">
interface ContractResult {
  input: string;
  action: string;
  output: string;
  operationKey: string;
}

const { result }: { result: ContractResult } = defineProps(["result"]);
</script>

<style scoped>
.contract-result {
  width: min(100%, 760px);
  margin: 0 auto;
  color: var(--el-text-color-primary);
  letter-spacing: 0;
}

.contract-result__header {
  display: flex;
  gap: 24px;
  align-items: flex-start;
  justify-content: space-between;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--el-border-color);
}

.contract-result__label {
  margin: 0 0 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

h2 {
  margin: 0;
  font-size: 22px;
  font-weight: 650;
}

.contract-result__status {
  padding: 5px 9px;
  font-size: 12px;
  font-weight: 600;
  color: var(--el-color-success-dark-2);
  background: var(--el-color-success-light-9);
  border: 1px solid var(--el-color-success-light-5);
  border-radius: 6px;
}

.contract-result__trace {
  padding: 8px 0 8px 22px;
  margin: 0;
  border-left: 2px solid var(--el-border-color);
}

.contract-result__trace > div {
  position: relative;
  display: grid;
  grid-template-columns: minmax(72px, 110px) minmax(0, 1fr);
  gap: 16px;
  padding: 16px 0;
}

.contract-result__trace > div::before {
  position: absolute;
  top: 21px;
  left: -28px;
  width: 10px;
  height: 10px;
  content: "";
  background: var(--el-bg-color);
  border: 2px solid var(--el-color-primary);
  border-radius: 50%;
}

dt {
  font-size: 12px;
  font-weight: 600;
  color: var(--el-text-color-secondary);
}

dd {
  min-width: 0;
  margin: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}

footer {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  justify-content: space-between;
  padding-top: 16px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  border-top: 1px solid var(--el-border-color-lighter);
}

code {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  color: var(--el-text-color-primary);
}

@media (max-width: 540px) {
  .contract-result__trace > div {
    grid-template-columns: 1fr;
    gap: 6px;
  }
}
</style>
