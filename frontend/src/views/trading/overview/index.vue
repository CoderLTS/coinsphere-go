<template>
  <div class="trading-page" v-loading="loading">
    <header class="page-toolbar">
      <div>
        <span class="context-label">{{ t('trading.context') }}</span>
        <h1>{{ t('trading.title') }}</h1>
      </div>
      <ElTooltip :content="t('common.refresh')">
        <ElButton
          class="icon-button"
          circle
          :icon="Refresh"
          :loading="loading"
          @click="loadOverview"
        />
      </ElTooltip>
    </header>

    <section
      class="safety-rail"
      :class="overview.control.emergencyStopped ? 'is-stopped' : 'is-running'"
    >
      <div class="safety-symbol" aria-hidden="true">
        <ElIcon :size="22">
          <WarningFilled v-if="overview.control.emergencyStopped" />
          <CircleCheckFilled v-else />
        </ElIcon>
      </div>
      <div class="safety-copy">
        <span>{{ t('trading.control.label') }}</span>
        <strong>
          {{
            overview.control.emergencyStopped
              ? t('trading.control.stopped')
              : t('trading.control.running')
          }}
        </strong>
        <small v-if="overview.control.stopReason">{{ overview.control.stopReason }}</small>
      </div>
      <div class="safety-meta">
        <span>{{ t('trading.control.updatedAt') }}</span>
        <strong>{{ formatTime(overview.control.updatedAt) }}</strong>
      </div>
      <ElButton
        v-if="!overview.control.emergencyStopped"
        type="danger"
        :icon="SwitchButton"
        :loading="commandLoading === 'emergency-stop'"
        @click="activateEmergencyStop"
      >
        {{ t('trading.control.stop') }}
      </ElButton>
      <ElButton
        v-else-if="isSuper"
        type="danger"
        plain
        :icon="Unlock"
        :loading="commandLoading === 'emergency-release'"
        @click="releaseEmergencyStop"
      >
        {{ t('trading.control.release') }}
      </ElButton>
    </section>

    <section v-if="overview.accounts.length === 0" class="empty-state">
      <ElEmpty :description="t('trading.accounts.empty')">
        <ElButton type="primary" :icon="Plus" @click="openCreateDialog">
          {{ t('trading.accounts.create') }}
        </ElButton>
      </ElEmpty>
    </section>

    <div v-else class="trading-workspace">
      <aside class="account-rail">
        <div class="account-rail-header">
          <div>
            <span>{{ t('trading.accounts.title') }}</span>
            <strong>{{ overview.accounts.length }}</strong>
          </div>
          <ElTooltip :content="t('trading.accounts.create')">
            <ElButton class="icon-button" circle :icon="Plus" @click="openCreateDialog" />
          </ElTooltip>
        </div>

        <ElSelect
          v-model="selectedAccountId"
          class="mobile-account-select"
          :placeholder="t('trading.accounts.select')"
        >
          <ElOption
            v-for="account in overview.accounts"
            :key="account.id"
            :label="account.name"
            :value="account.id"
          />
        </ElSelect>

        <nav class="account-list" :aria-label="t('trading.accounts.title')">
          <button
            v-for="account in overview.accounts"
            :key="account.id"
            type="button"
            class="account-item"
            :class="{ 'is-selected': account.id === selectedAccountId }"
            @click="selectedAccountId = account.id"
          >
            <span class="account-item-main">
              <strong>{{ account.name }}</strong>
              <small
                >{{ marketLabel(account.market) }} · {{ account.environment.toUpperCase() }}</small
              >
            </span>
            <span class="account-state" :class="account.status">
              {{ accountStatusLabel(account.status) }}
            </span>
          </button>
        </nav>
      </aside>

      <main v-if="selectedAccount" class="account-detail">
        <header class="account-heading">
          <div class="account-title">
            <div class="account-title-row">
              <h2>{{ selectedAccount.name }}</h2>
              <ElTag
                size="small"
                effect="plain"
                :type="selectedAccount.status === 'active' ? 'success' : 'warning'"
              >
                {{ accountStatusLabel(selectedAccount.status) }}
              </ElTag>
              <ElTag
                v-if="!selectedAccount.risk.complete"
                size="small"
                type="danger"
                effect="plain"
              >
                {{ t('trading.risk.incomplete') }}
              </ElTag>
            </div>
            <small v-if="selectedAccount.pauseReason" class="pause-reason">
              {{ selectedAccount.pauseReason }}
            </small>
          </div>
          <div class="account-actions">
            <ElTooltip :content="t('trading.risk.edit')">
              <ElButton class="icon-button" circle :icon="EditPen" @click="openRiskDialog" />
            </ElTooltip>
            <ElButton
              v-if="selectedAccount.status === 'paused'"
              :icon="VideoPlay"
              :loading="commandLoading === 'resume'"
              :disabled="
                overview.control.emergencyStopped ||
                !selectedAccount.risk.complete ||
                (selectedAccount.environment !== 'paper' &&
                  (!selectedAccount.credentialsConfigured ||
                    selectedAccount.credentialVerificationStatus !== 'verified' ||
                    selectedAccount.reconciliation.status !== 'matched'))
              "
              @click="resumeAccount"
            >
              {{ t('trading.accounts.resume') }}
            </ElButton>
          </div>
        </header>

        <section
          v-if="
            selectedAccount.environment !== 'live' ||
            (selectedAccount.market === 'spot' && overview.capabilities.spotLiveAutoEnabled) ||
            selectedAccount.automationAuthorized ||
            selectedAccount.automationEnabled ||
            selectedAccount.autoAuthorized
          "
          class="account-switches"
        >
          <label>
            <span>{{ t('trading.automation.authorization') }}</span>
            <ElSwitch
              :model-value="selectedAccount.automationAuthorized"
              :disabled="
                !isSuper ||
                Boolean(commandLoading) ||
                (selectedAccount.environment === 'live' &&
                  (selectedAccount.market !== 'spot' ||
                    !overview.capabilities.spotLiveAutoEnabled) &&
                  !selectedAccount.automationAuthorized)
              "
              :loading="commandLoading === 'authorization'"
              @change="setAuthorization"
            />
          </label>
          <label>
            <span>{{ t('trading.automation.accountSwitch') }}</span>
            <ElSwitch
              :model-value="selectedAccount.automationEnabled"
              :disabled="automationSwitchDisabled"
              :loading="commandLoading === 'automation'"
              @change="setAutomation"
            />
          </label>
        </section>

        <section v-if="selectedAccount.environment === 'live'" class="account-switches">
          <label>
            <span>{{ t('trading.manual.authorization') }}</span>
            <ElTag :type="selectedAccount.manualAuthorized ? 'success' : 'warning'" effect="plain">
              {{
                selectedAccount.manualAuthorized
                  ? t('trading.manual.authorized')
                  : t('trading.manual.pending')
              }}
            </ElTag>
          </label>
          <label
            v-if="
              (selectedAccount.market === 'spot' && overview.capabilities.spotLiveAutoEnabled) ||
              selectedAccount.autoAuthorized
            "
          >
            <span>{{ t('trading.automation.ownerRelease') }}</span>
            <ElTag :type="selectedAccount.autoAuthorized ? 'success' : 'warning'" effect="plain">
              {{
                selectedAccount.autoAuthorized
                  ? t('trading.manual.authorized')
                  : t('trading.manual.pending')
              }}
            </ElTag>
          </label>
        </section>

        <section v-if="selectedAccount.environment !== 'paper'" class="credential-strip">
          <div class="credential-summary">
            <div class="credential-state">
              <ElIcon :size="20"><Key /></ElIcon>
              <div>
                <span>{{ t('trading.credentials.label') }}</span>
                <strong>{{ credentialStatusLabel }}</strong>
                <small>{{ formatTime(selectedAccount.credentialsUpdatedAt) }}</small>
              </div>
            </div>
            <div class="credential-state">
              <ElIcon :size="20"><Refresh /></ElIcon>
              <div>
                <span>{{ t('trading.reconciliation.label') }}</span>
                <strong>{{ reconciliationStatusLabel }}</strong>
                <small>{{ reconciliationSummary }}</small>
              </div>
            </div>
          </div>
          <div class="credential-actions">
            <ElTag
              size="small"
              effect="plain"
              :type="
                selectedAccount.credentialVerificationStatus === 'verified' ? 'success' : 'warning'
              "
            >
              {{ credentialVerificationLabel }}
            </ElTag>
            <ElTag size="small" effect="plain" :type="reconciliationStatusType">
              {{ reconciliationStatusLabel }}
            </ElTag>
            <ElButton :icon="Key" @click="openCredentialDialog">
              {{
                selectedAccount.credentialsConfigured
                  ? t('trading.credentials.replace')
                  : t('trading.credentials.configure')
              }}
            </ElButton>
            <ElButton
              v-if="selectedAccount.credentialsConfigured"
              type="danger"
              plain
              :icon="Delete"
              :loading="commandLoading === 'credential-revoke'"
              @click="revokeCredentials"
            >
              {{ t('trading.credentials.revoke') }}
            </ElButton>
          </div>
        </section>

        <section v-if="selectedAccount.environment === 'paper'" class="balance-strip">
          <div>
            <span>{{ t('trading.balance.equity') }}</span>
            <strong class="decimal-value">{{ balanceValue('equity') }} USDT</strong>
          </div>
          <div>
            <span>{{ t('trading.balance.cash') }}</span>
            <strong class="decimal-value">{{ balanceValue('cashBalance') }} USDT</strong>
          </div>
          <div>
            <span>{{ t('trading.balance.realizedPnl') }}</span>
            <strong class="decimal-value" :class="pnlClass(selectedBalance?.realizedPnl)">
              {{ balanceValue('realizedPnl') }} USDT
            </strong>
          </div>
          <div>
            <span>{{ t('trading.balance.unrealizedPnl') }}</span>
            <strong class="decimal-value" :class="pnlClass(selectedBalance?.unrealizedPnl)">
              {{ balanceValue('unrealizedPnl') }} USDT
            </strong>
          </div>
        </section>

        <section
          v-else-if="selectedTestnetBalances.length"
          class="balance-strip testnet-balance-strip"
        >
          <div v-for="balance in selectedTestnetBalances" :key="balance.asset">
            <span>{{ balance.asset }}</span>
            <strong class="decimal-value">{{ balance.totalBalance }}</strong>
            <small>
              {{ t('trading.balance.available') }}
              <span class="decimal-value">{{ balance.availableBalance }}</span>
            </small>
          </div>
        </section>

        <section class="risk-ledger">
          <header>
            <div>
              <span>{{ t('trading.risk.title') }}</span>
              <strong>
                {{
                  selectedAccount.risk.complete
                    ? t('trading.risk.complete')
                    : t('trading.risk.incomplete')
                }}
              </strong>
            </div>
            <small>{{ whitelistSummary }}</small>
          </header>
          <dl>
            <div v-for="item in riskItems" :key="item.label">
              <dt>{{ item.label }}</dt>
              <dd class="decimal-value">{{ item.value }}</dd>
            </div>
          </dl>
        </section>

        <section
          v-if="selectedAccount.environment !== 'paper' && selectedAuditSummary"
          class="audit-summary"
        >
          <header>
            <div>
              <span>{{ t('trading.audit.title') }}</span>
              <strong>{{ t('trading.audit.rawState') }}</strong>
            </div>
            <ElTag size="small" effect="plain" :type="reconciliationStatusType">
              {{ reconciliationStatusLabel }}
            </ElTag>
          </header>
          <div v-if="selectedAuditSummary.reconciliation.errorCode" class="audit-error">
            <span>{{ t('trading.audit.errorCode') }}</span>
            <code>{{ selectedAuditSummary.reconciliation.errorCode }}</code>
          </div>
          <dl class="audit-grid">
            <div v-for="item in auditCountItems" :key="item.label">
              <dt>{{ item.label }}</dt>
              <dd class="decimal-value">{{ item.value }}</dd>
            </div>
          </dl>
          <dl v-if="selectedAuditSummary.riskState" class="audit-grid audit-risk-grid">
            <div v-for="item in auditRiskItems" :key="item.label">
              <dt>{{ item.label }}</dt>
              <dd class="decimal-value">{{ item.value }}</dd>
            </div>
          </dl>
          <footer class="audit-meta">
            <span
              >{{ t('trading.audit.observedAt') }}
              {{ formatTime(selectedAuditSummary.reconciliation.lastObservedAt) }}</span
            >
            <span
              >{{ t('trading.audit.lastFactAt') }}
              {{ formatTime(selectedAuditSummary.lastFactAt) }}</span
            >
          </footer>
        </section>

        <ElTabs v-model="activeTab" class="trading-tabs">
          <ElTabPane
            :label="`${t('trading.tabs.positions')} ${selectedPositions.length}`"
            name="positions"
          >
            <ElTable :data="selectedPositions" :empty-text="t('common.noData')" stripe>
              <ElTableColumn prop="symbol" :label="t('trading.table.symbol')" min-width="120" />
              <ElTableColumn :label="t('trading.table.quantity')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.quantity }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.entryPrice')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.averageEntryPrice }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.lastPrice')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.lastPrice }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.market === 'usd_m' && selectedAccount.environment !== 'paper'"
                :label="t('trading.risk.leverage')"
                width="100"
                align="right"
              >
                <template #default="{ row }">
                  {{ row.leverage ? `${row.leverage}x` : '--' }}
                </template>
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.market === 'usd_m' && selectedAccount.environment !== 'paper'"
                :label="t('trading.table.marginMode')"
                width="110"
                align="center"
              >
                <template #default="{ row }">
                  {{ row.isolated === true ? t('trading.table.isolated') : '--' }}
                </template>
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.market === 'usd_m' && selectedAccount.environment !== 'paper'"
                :label="t('trading.table.liquidationPrice')"
                min-width="160"
                align="right"
              >
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.liquidationPrice }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.market === 'usd_m' && selectedAccount.environment !== 'paper'"
                :label="t('trading.table.liquidationDistance')"
                min-width="160"
                align="right"
              >
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.liquidationDistanceRatio }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn
                :label="t('trading.balance.realizedPnl')"
                min-width="150"
                align="right"
              >
                <template #default="{ row }">
                  <span class="decimal-value" :class="pnlClass(row.realizedPnl)">{{
                    row.realizedPnl
                  }}</span>
                </template>
              </ElTableColumn>
              <ElTableColumn
                :label="t('trading.balance.unrealizedPnl')"
                min-width="150"
                align="right"
              >
                <template #default="{ row }">
                  <span class="decimal-value" :class="pnlClass(row.unrealizedPnl)">{{
                    row.unrealizedPnl
                  }}</span>
                </template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.updatedAt')" min-width="180">
                <template #default="{ row }">{{ formatTime(row.updatedAt) }}</template>
              </ElTableColumn>
            </ElTable>
          </ElTabPane>

          <ElTabPane :label="`${t('trading.tabs.orders')} ${selectedOrders.length}`" name="orders">
            <ElTable :data="selectedOrders" :empty-text="t('common.noData')" stripe>
              <ElTableColumn prop="symbol" :label="t('trading.table.symbol')" min-width="120" />
              <ElTableColumn
                v-if="selectedAccount.environment !== 'paper'"
                :label="t('trading.table.purpose')"
                width="120"
                align="center"
              >
                <template #default="{ row }">
                  <ElTag :type="orderPurposeType(row.purpose)" effect="plain" size="small">
                    {{ orderPurposeLabel(row.purpose) }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.side')" width="100" align="center">
                <template #default="{ row }">
                  <ElTag
                    :type="row.side === 'buy' ? 'success' : 'danger'"
                    effect="plain"
                    size="small"
                  >
                    {{ sideLabel(row.side) }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.environment !== 'paper'"
                :label="t('trading.table.orderType')"
                min-width="170"
              >
                <template #default="{ row }">
                  <div class="order-shape">
                    <span>{{ orderTypeLabel(row.orderType) }}</span>
                    <small v-if="row.closePosition">{{
                      t('trading.orderFlag.closePosition')
                    }}</small>
                    <small v-else-if="row.reduceOnly">{{
                      t('trading.orderFlag.reduceOnly')
                    }}</small>
                  </div>
                </template>
              </ElTableColumn>
              <ElTableColumn
                v-if="selectedAccount.environment !== 'paper'"
                :label="t('trading.table.stopPrice')"
                min-width="150"
                align="right"
              >
                <template #default="{ row }">
                  <span class="decimal-value">{{
                    row.stopPrice === '0' ? '--' : row.stopPrice
                  }}</span>
                </template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.quantity')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{
                    row.closePosition ? '--' : row.quantity
                  }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.averagePrice')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.averagePrice }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.status')" width="120" align="center">
                <template #default="{ row }">
                  <ElTag :type="orderStatusType(row.status)" effect="plain" size="small">{{
                    orderStatusLabel(row.status)
                  }}</ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn
                prop="clientOrderId"
                :label="t('trading.table.clientOrderId')"
                min-width="230"
              />
              <ElTableColumn :label="t('trading.table.createdAt')" min-width="180">
                <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
              </ElTableColumn>
            </ElTable>
          </ElTabPane>

          <ElTabPane
            :label="`${t('trading.tabs.intents')} ${selectedIntents.length}`"
            name="intents"
          >
            <ElTable :data="selectedIntents" :empty-text="t('common.noData')" stripe>
              <ElTableColumn prop="symbol" :label="t('trading.table.symbol')" min-width="120" />
              <ElTableColumn :label="t('trading.table.mode')" width="110" align="center">
                <template #default="{ row }">{{ modeLabel(row.mode) }}</template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.target')" width="120" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.target }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.status')" width="130" align="center">
                <template #default="{ row }">
                  <ElTag :type="intentStatusType(row.status)" effect="plain" size="small">
                    {{ intentStatusLabel(row.status) }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn prop="blockReason" :label="t('trading.table.reason')" min-width="220">
                <template #default="{ row }">{{ row.blockReason || '--' }}</template>
              </ElTableColumn>
              <ElTableColumn
                prop="clientOrderId"
                :label="t('trading.table.clientOrderId')"
                min-width="230"
              />
              <ElTableColumn :label="t('trading.table.createdAt')" min-width="180">
                <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
              </ElTableColumn>
            </ElTable>
          </ElTabPane>

          <ElTabPane
            v-if="selectedAccount.environment !== 'paper'"
            :label="`${t('trading.tabs.ledger')} ${selectedTradeFacts.length}`"
            name="ledger"
          >
            <ElTable :data="selectedTradeFacts" :empty-text="t('common.noData')" stripe>
              <ElTableColumn :label="t('trading.table.eventType')" width="120" align="center">
                <template #default="{ row }">
                  <ElTag :type="tradeFactEventType(row.eventType)" effect="plain" size="small">
                    {{ tradeFactEventLabel(row.eventType) }}
                  </ElTag>
                </template>
              </ElTableColumn>
              <ElTableColumn prop="symbol" :label="t('trading.table.symbol')" min-width="120" />
              <ElTableColumn :label="t('trading.table.side')" width="100" align="center">
                <template #default="{ row }">{{ row.side ? sideLabel(row.side) : '--' }}</template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.quantity')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.quantity }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.averagePrice')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.price }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.amount')" min-width="150" align="right">
                <template #default="{ row }"
                  ><span class="decimal-value">{{ row.amount }} {{ row.asset }}</span></template
                >
              </ElTableColumn>
              <ElTableColumn
                :label="t('trading.table.externalId')"
                prop="externalTradeId"
                min-width="180"
              >
                <template #default="{ row }">
                  <span class="decimal-value">
                    {{ row.externalTradeId || row.externalTransactionId || '--' }}
                  </span>
                </template>
              </ElTableColumn>
              <ElTableColumn :label="t('trading.table.occurredAt')" min-width="180">
                <template #default="{ row }">{{ formatTime(row.occurredAt) }}</template>
              </ElTableColumn>
            </ElTable>
          </ElTabPane>
        </ElTabs>
      </main>
    </div>

    <ElDialog
      v-model="accountDialogVisible"
      :title="
        dialogMode === 'create' ? t('trading.dialog.createTitle') : t('trading.dialog.riskTitle')
      "
      width="min(720px, calc(100vw - 28px))"
      destroy-on-close
    >
      <ElForm
        ref="accountFormRef"
        :model="accountForm"
        :rules="accountFormRules"
        label-position="top"
      >
        <div v-if="dialogMode === 'create'" class="form-grid">
          <ElFormItem :label="t('trading.form.name')" prop="name">
            <ElInput v-model="accountForm.name" maxlength="120" />
          </ElFormItem>
          <ElFormItem :label="t('trading.form.environment')" prop="environment">
            <ElSegmented v-model="accountForm.environment" :options="environmentOptions" />
          </ElFormItem>
          <ElFormItem :label="t('trading.form.market')" prop="market">
            <ElSegmented
              v-model="accountForm.market"
              :options="marketOptions"
              @change="handleMarketChange"
            />
          </ElFormItem>
          <ElFormItem :label="t('trading.form.initialBalance')" prop="initialBalance">
            <ElInput v-model="accountForm.initialBalance" inputmode="decimal">
              <template #append>USDT</template>
            </ElInput>
          </ElFormItem>
          <ElFormItem :label="t('trading.form.feeRate')" prop="paperFeeRate">
            <ElInput v-model="accountForm.paperFeeRate" inputmode="decimal" />
          </ElFormItem>
        </div>

        <ElFormItem :label="t('trading.form.instruments')" prop="instrumentIds">
          <ElSelect
            v-model="accountForm.instrumentIds"
            multiple
            filterable
            collapse-tags
            collapse-tags-tooltip
            :loading="symbolsLoading"
            class="full-width"
          >
            <ElOption
              v-for="symbol in availableSymbols"
              :key="symbol.id"
              :value="symbol.id"
              :label="`${symbol.nativeSymbol} · ${symbol.baseAsset}/${symbol.quoteAsset}`"
            />
          </ElSelect>
        </ElFormItem>

        <div class="form-grid risk-form-grid">
          <ElFormItem :label="t('trading.risk.maxTotal')" prop="maxTotalNotional">
            <ElInput v-model="accountForm.maxTotalNotional" inputmode="decimal"
              ><template #append>USDT</template></ElInput
            >
          </ElFormItem>
          <ElFormItem :label="t('trading.risk.maxSymbol')" prop="maxSymbolNotional">
            <ElInput v-model="accountForm.maxSymbolNotional" inputmode="decimal"
              ><template #append>USDT</template></ElInput
            >
          </ElFormItem>
          <ElFormItem :label="t('trading.risk.maxOrder')" prop="maxOrderNotional">
            <ElInput v-model="accountForm.maxOrderNotional" inputmode="decimal"
              ><template #append>USDT</template></ElInput
            >
          </ElFormItem>
          <ElFormItem :label="t('trading.risk.maxDailyLoss')" prop="maxDailyLoss">
            <ElInput v-model="accountForm.maxDailyLoss" inputmode="decimal"
              ><template #append>USDT</template></ElInput
            >
          </ElFormItem>
          <ElFormItem :label="t('trading.risk.maxDrawdown')" prop="maxDrawdown">
            <ElInput v-model="accountForm.maxDrawdown" inputmode="decimal"
              ><template #append>USDT</template></ElInput
            >
          </ElFormItem>
          <ElFormItem :label="t('trading.risk.maxQuoteAge')" prop="maxQuoteAgeSeconds">
            <ElInputNumber
              v-model="accountForm.maxQuoteAgeSeconds"
              :min="1"
              :max="300"
              controls-position="right"
            />
          </ElFormItem>
          <ElFormItem
            v-if="accountForm.market === 'usd_m'"
            :label="t('trading.risk.leverage')"
            prop="leverage"
          >
            <ElInputNumber
              v-model="accountForm.leverage"
              :min="1"
              :max="5"
              controls-position="right"
            />
          </ElFormItem>
        </div>
      </ElForm>
      <template #footer>
        <ElButton @click="accountDialogVisible = false">{{ t('common.cancel') }}</ElButton>
        <ElButton type="primary" :loading="dialogSubmitting" @click="submitAccountDialog">
          {{ dialogMode === 'create' ? t('trading.dialog.create') : t('trading.dialog.saveRisk') }}
        </ElButton>
      </template>
    </ElDialog>

    <ElDialog
      v-model="credentialDialogVisible"
      :title="t('trading.credentials.dialogTitle')"
      width="min(520px, calc(100vw - 28px))"
      destroy-on-close
      @closed="resetCredentialForm"
    >
      <ElForm
        ref="credentialFormRef"
        :model="credentialForm"
        :rules="credentialFormRules"
        label-position="top"
      >
        <ElFormItem :label="t('trading.credentials.apiKey')" prop="apiKey">
          <ElInput
            v-model="credentialForm.apiKey"
            type="password"
            show-password
            autocomplete="off"
          />
        </ElFormItem>
        <ElFormItem :label="t('trading.credentials.apiSecret')" prop="apiSecret">
          <ElInput
            v-model="credentialForm.apiSecret"
            type="password"
            show-password
            autocomplete="off"
          />
        </ElFormItem>
        <ElFormItem prop="withdrawalDisabled">
          <ElCheckbox v-model="credentialForm.withdrawalDisabled">
            {{ t('trading.credentials.withdrawalDisabled') }}
          </ElCheckbox>
        </ElFormItem>
        <ElFormItem prop="ipWhitelistConfigured">
          <ElCheckbox v-model="credentialForm.ipWhitelistConfigured">
            {{ t('trading.credentials.ipWhitelistConfigured') }}
          </ElCheckbox>
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="credentialDialogVisible = false">{{ t('common.cancel') }}</ElButton>
        <ElButton
          type="primary"
          :icon="Key"
          :loading="credentialSubmitting"
          @click="saveCredentials"
        >
          {{ t('trading.credentials.save') }}
        </ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<script setup lang="ts">
  import {
    CircleCheckFilled,
    Delete,
    EditPen,
    Key,
    Plus,
    Refresh,
    SwitchButton,
    Unlock,
    VideoPlay,
    WarningFilled
  } from '@element-plus/icons-vue'
  import { ElMessageBox, type FormInstance, type FormRules, type TagProps } from 'element-plus'
  import { useI18n } from 'vue-i18n'
  import { fetchReauth } from '@/api/auth'
  import { useUserStore } from '@/store/modules/user'
  import {
    fetchActivateTradingEmergencyStop,
    fetchCreateTradingAccount,
    fetchMarketSymbols,
    fetchReleaseTradingEmergencyStop,
    fetchRevokeTradingCredentials,
    fetchResumeTradingAccount,
    fetchSaveTradingCredentials,
    fetchSetTradingAuthorization,
    fetchSetTradingAutomation,
    fetchTradingOverview,
    fetchUpdateTradingRisk,
    type MarketSymbol,
    type PaperBalance,
    type TradingOverview,
    type TradingRiskPayload
  } from '@/api/trading'

  defineOptions({ name: 'TradingOverviewPage' })

  type AccountFormModel = {
    name: string
    market: 'spot' | 'usd_m'
    environment: 'paper' | 'testnet' | 'live'
    initialBalance: string
    paperFeeRate: string
    instrumentIds: string[]
    maxTotalNotional: string
    maxSymbolNotional: string
    maxOrderNotional: string
    maxDailyLoss: string
    maxDrawdown: string
    maxQuoteAgeSeconds: number
    leverage: number
  }

  type CredentialFormModel = {
    apiKey: string
    apiSecret: string
    withdrawalDisabled: boolean
    ipWhitelistConfigured: boolean
  }

  type OrderRow = {
    symbol: string
    side: string
    quantity: string
    filledQuantity: string
    averagePrice: string
    purpose: string
    orderType: string
    stopPrice: string
    closePosition: boolean
    reduceOnly: boolean
    workingType: string
    replacesOrderId: string | null
    status: string
    clientOrderId: string
    createdAt: string
  }

  const emptyControl = (): TradingOverview['control'] => ({
    emergencyStopped: true,
    stopReason: '',
    stoppedAt: '',
    stoppedByUserId: null,
    releasedAt: null,
    releasedByUserId: null,
    updatedAt: ''
  })

  const createEmptyOverview = (): TradingOverview => ({
    capabilities: {
      spotLiveManualEnabled: false,
      spotLiveAutoEnabled: false,
      usdMLiveManualEnabled: false
    },
    control: emptyControl(),
    accounts: [],
    intents: [],
    orders: [],
    positions: [],
    balances: [],
    testnetBalances: [],
    testnetPositions: [],
    testnetOpenOrders: [],
    testnetOrders: [],
    testnetTradeFacts: [],
    testnetAuditSummaries: []
  })

  const createAccountForm = (): AccountFormModel => ({
    name: '',
    market: 'spot',
    environment: 'paper',
    initialBalance: '10000',
    paperFeeRate: '0.001',
    instrumentIds: [],
    maxTotalNotional: '5000',
    maxSymbolNotional: '2500',
    maxOrderNotional: '1000',
    maxDailyLoss: '500',
    maxDrawdown: '1000',
    maxQuoteAgeSeconds: 30,
    leverage: 2
  })

  const createCredentialForm = (): CredentialFormModel => ({
    apiKey: '',
    apiSecret: '',
    withdrawalDisabled: false,
    ipWhitelistConfigured: false
  })

  const { t, locale, te } = useI18n()
  const userStore = useUserStore()
  const loading = ref(false)
  const commandLoading = ref('')
  const dialogSubmitting = ref(false)
  const credentialSubmitting = ref(false)
  const symbolsLoading = ref(false)
  const overview = reactive<TradingOverview>(createEmptyOverview())
  const selectedAccountId = ref('')
  const activeTab = ref('positions')
  const accountDialogVisible = ref(false)
  const credentialDialogVisible = ref(false)
  const dialogMode = ref<'create' | 'risk'>('create')
  const accountFormRef = ref<FormInstance>()
  const credentialFormRef = ref<FormInstance>()
  const accountForm = reactive<AccountFormModel>(createAccountForm())
  const credentialForm = reactive<CredentialFormModel>(createCredentialForm())
  const availableSymbols = ref<MarketSymbol[]>([])

  const isSuper = computed(() => userStore.info.roleCodes.includes('R_SUPER'))
  const selectedAccount = computed(
    () => overview.accounts.find((account) => account.id === selectedAccountId.value) || null
  )
  const selectedBalance = computed(
    () => overview.balances.find((balance) => balance.accountId === selectedAccountId.value) || null
  )
  const selectedTestnetBalances = computed(() =>
    overview.testnetBalances.filter((balance) => balance.accountId === selectedAccountId.value)
  )
  const selectedPositions = computed(() => {
    if (selectedAccount.value?.environment !== 'paper') {
      return overview.testnetPositions
        .filter((position) => position.accountId === selectedAccountId.value)
        .map((position) => ({
          symbol: position.symbol,
          quantity: position.quantity,
          averageEntryPrice: position.entryPrice,
          lastPrice: position.markPrice === '0' ? '--' : position.markPrice,
          leverage: position.leverage,
          isolated: position.isolated,
          liquidationPrice: position.liquidationPrice,
          liquidationDistanceRatio: position.liquidationDistanceRatio,
          realizedPnl: '--',
          unrealizedPnl: position.unrealizedPnl,
          updatedAt: position.observedAt
        }))
    }
    return overview.positions.filter((position) => position.accountId === selectedAccountId.value)
  })
  const selectedOrders = computed<OrderRow[]>(() => {
    if (selectedAccount.value?.environment !== 'paper') {
      const managed = overview.testnetOrders
        .filter((order) => order.accountId === selectedAccountId.value)
        .map((order) => ({
          symbol: order.symbol,
          side: order.side,
          quantity: order.quantity,
          filledQuantity: order.filledQuantity,
          averagePrice: order.averagePrice,
          purpose: order.purpose,
          orderType: order.orderType,
          stopPrice: order.stopPrice,
          closePosition: order.closePosition,
          reduceOnly: order.reduceOnly,
          workingType: order.workingType,
          replacesOrderId: order.replacesOrderId,
          status: order.status,
          clientOrderId: order.clientOrderId,
          createdAt: order.submittedAt
        }))
      const managedClientOrderIds = new Set(managed.map((order) => order.clientOrderId))
      const external = overview.testnetOpenOrders
        .filter(
          (order) =>
            order.accountId === selectedAccountId.value &&
            !managedClientOrderIds.has(order.clientOrderId)
        )
        .map((order) => ({
          symbol: order.symbol,
          side: order.side,
          quantity: order.originalQuantity,
          filledQuantity: order.executedQuantity,
          averagePrice: order.price,
          purpose: 'external',
          orderType: order.orderType,
          stopPrice: order.stopPrice,
          closePosition: order.closePosition,
          reduceOnly: order.reduceOnly,
          workingType: order.workingType,
          replacesOrderId: null,
          status: order.status,
          clientOrderId: order.clientOrderId,
          createdAt: order.observedAt
        }))
      return [...managed, ...external]
    }
    return overview.orders
      .filter((order) => order.accountId === selectedAccountId.value)
      .map((order) => ({
        symbol: order.symbol,
        side: order.side,
        quantity: order.quantity,
        filledQuantity: order.filledQuantity,
        averagePrice: order.averagePrice,
        purpose: '',
        orderType: '',
        stopPrice: '0',
        closePosition: false,
        reduceOnly: false,
        workingType: '',
        replacesOrderId: null,
        status: order.status,
        clientOrderId: order.clientOrderId,
        createdAt: order.createdAt
      }))
  })
  const selectedIntents = computed(() =>
    overview.intents.filter((intent) => intent.accountId === selectedAccountId.value)
  )
  const selectedTradeFacts = computed(() =>
    overview.testnetTradeFacts.filter((fact) => fact.accountId === selectedAccountId.value)
  )
  const selectedAuditSummary = computed(
    () =>
      overview.testnetAuditSummaries.find(
        (summary) => summary.accountId === selectedAccountId.value
      ) || null
  )
  const auditCountItems = computed(() => {
    const summary = selectedAuditSummary.value
    return [
      { label: t('trading.audit.unknownOrders'), value: summary?.unknownOrderCount ?? 0 },
      { label: t('trading.audit.protectionOrders'), value: summary?.protectionOrderCount ?? 0 },
      {
        label: t('trading.audit.activeProtectionOrders'),
        value: summary?.activeProtectionOrderCount ?? 0
      },
      { label: t('trading.audit.recoveredOrders'), value: summary?.recoveredOrderCount ?? 0 },
      { label: t('trading.audit.tradeFacts'), value: summary?.tradeFactCount ?? 0 },
      { label: t('trading.audit.fills'), value: summary?.fillFactCount ?? 0 },
      { label: t('trading.audit.fees'), value: summary?.feeFactCount ?? 0 },
      { label: t('trading.audit.funding'), value: summary?.fundingFactCount ?? 0 }
    ]
  })
  const auditRiskItems = computed(() => {
    const risk = selectedAuditSummary.value?.riskState
    return [
      { label: t('trading.audit.baselineEquity'), value: risk?.baselineEquity ?? '--' },
      { label: t('trading.audit.equity'), value: risk?.equity ?? '--' },
      { label: t('trading.audit.peakEquity'), value: risk?.peakEquity ?? '--' },
      { label: t('trading.audit.dayStartEquity'), value: risk?.dayStartEquity ?? '--' },
      { label: t('trading.audit.dayStartDate'), value: risk?.dayStartDate ?? '--' },
      { label: t('trading.audit.riskUpdatedAt'), value: formatTime(risk?.updatedAt) }
    ]
  })
  const automationSwitchDisabled = computed(() => {
    const account = selectedAccount.value
    if (!account || Boolean(commandLoading.value)) return true
    if (account.automationEnabled) return false
    if (
      account.environment === 'live' &&
      (account.market !== 'spot' || !overview.capabilities.spotLiveAutoEnabled)
    )
      return true
    return (
      overview.control.emergencyStopped ||
      account.status !== 'active' ||
      !account.automationAuthorized ||
      (account.environment === 'live' && !account.manualAuthorized) ||
      !account.risk.complete ||
      (account.environment !== 'paper' &&
        (!account.credentialsConfigured ||
          account.credentialVerificationStatus !== 'verified' ||
          account.reconciliation.status !== 'matched'))
    )
  })
  const credentialStatusLabel = computed(() => {
    const account = selectedAccount.value
    if (account?.credentialStatus === 'revoked') return t('trading.credentials.revoked')
    if (!account?.credentialsConfigured) return t('trading.credentials.notConfigured')
    return t('trading.credentials.configured')
  })
  const credentialVerificationLabel = computed(() => {
    const account = selectedAccount.value
    if (!account?.credentialsConfigured) return t('trading.credentials.verification.unverified')
    const status = account.credentialVerificationStatus || 'unverified'
    return t(`trading.credentials.verification.${status}`)
  })
  const reconciliationStatusLabel = computed(() => {
    const status = selectedAccount.value?.reconciliation.status || 'pending'
    return t(`trading.reconciliation.status.${status}`)
  })
  const reconciliationStatusType = computed<TagProps['type']>(() => {
    const status = selectedAccount.value?.reconciliation.status
    if (status === 'matched') return 'success'
    if (status === 'mismatch') return 'danger'
    if (status === 'unknown') return 'warning'
    return 'info'
  })
  const reconciliationSummary = computed(() => {
    const reconciliation = selectedAccount.value?.reconciliation
    if (!reconciliation?.lastObservedAt) return formatTime(reconciliation?.lastAttemptedAt)
    return t('trading.reconciliation.summary', {
      balances: reconciliation.balanceCount,
      positions: reconciliation.positionCount,
      orders: reconciliation.openOrderCount,
      time: formatTime(reconciliation.lastObservedAt)
    })
  })
  const whitelistSummary = computed(() => {
    const account = selectedAccount.value
    if (!account) return '--'
    const names = account.risk.instrumentIds.map(
      (id) =>
        availableSymbols.value.find((symbol) => symbol.id === id)?.nativeSymbol || id.slice(0, 8)
    )
    return names.length ? names.join(' · ') : t('trading.risk.noInstruments')
  })
  const riskItems = computed(() => {
    const risk = selectedAccount.value?.risk
    return [
      { label: t('trading.risk.maxTotal'), value: amountLimit(risk?.maxTotalNotional) },
      { label: t('trading.risk.maxSymbol'), value: amountLimit(risk?.maxSymbolNotional) },
      { label: t('trading.risk.maxOrder'), value: amountLimit(risk?.maxOrderNotional) },
      { label: t('trading.risk.maxDailyLoss'), value: amountLimit(risk?.maxDailyLoss) },
      { label: t('trading.risk.maxDrawdown'), value: amountLimit(risk?.maxDrawdown) },
      {
        label: t('trading.risk.maxQuoteAge'),
        value: risk?.maxQuoteAgeSeconds ? `${risk.maxQuoteAgeSeconds} s` : '--'
      },
      {
        label: t('trading.risk.leverage'),
        value: risk?.leverage ? `${risk.leverage}x` : '--'
      }
    ]
  })
  const marketOptions = computed(() => {
    const options = [] as Array<{ label: string; value: 'spot' | 'usd_m' }>
    if (accountForm.environment !== 'live' || overview.capabilities.spotLiveManualEnabled) {
      options.push({ label: t('trading.market.spot'), value: 'spot' })
    }
    if (accountForm.environment !== 'live' || overview.capabilities.usdMLiveManualEnabled) {
      options.push({ label: t('trading.market.usdM'), value: 'usd_m' })
    }
    return options
  })
  const environmentOptions = computed(() => {
    const options = [
      { label: t('trading.environment.paper'), value: 'paper' },
      { label: t('trading.environment.testnet'), value: 'testnet' }
    ]
    if (
      overview.capabilities.spotLiveManualEnabled ||
      overview.capabilities.usdMLiveManualEnabled
    ) {
      options.push({ label: t('trading.environment.live'), value: 'live' })
    }
    return options
  })

  const positiveDecimalRule = (
    _rule: unknown,
    value: string,
    callback: (error?: Error) => void
  ) => {
    if (!/^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value) || Number(value) <= 0) {
      callback(new Error(t('trading.validation.positiveDecimal')))
      return
    }
    callback()
  }
  const feeRateRule = (_rule: unknown, value: string, callback: (error?: Error) => void) => {
    if (!/^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value) || Number(value) < 0 || Number(value) > 0.01) {
      callback(new Error(t('trading.validation.feeRate')))
      return
    }
    callback()
  }
  const requiredConfirmationRule = (
    _rule: unknown,
    value: boolean,
    callback: (error?: Error) => void
  ) => {
    if (!value) {
      callback(new Error(t('trading.validation.confirmSafety')))
      return
    }
    callback()
  }
  const accountFormRules = computed<FormRules<AccountFormModel>>(() => ({
    name: [
      {
        required: dialogMode.value === 'create',
        message: t('trading.validation.name'),
        trigger: 'blur'
      }
    ],
    initialBalance: [{ validator: positiveDecimalRule, trigger: 'blur' }],
    paperFeeRate: [{ validator: feeRateRule, trigger: 'blur' }],
    instrumentIds: [
      {
        type: 'array',
        required: true,
        min: 1,
        message: t('trading.validation.instruments'),
        trigger: 'change'
      }
    ],
    maxTotalNotional: [{ validator: positiveDecimalRule, trigger: 'blur' }],
    maxSymbolNotional: [{ validator: positiveDecimalRule, trigger: 'blur' }],
    maxOrderNotional: [{ validator: positiveDecimalRule, trigger: 'blur' }],
    maxDailyLoss: [{ validator: positiveDecimalRule, trigger: 'blur' }],
    maxDrawdown: [{ validator: positiveDecimalRule, trigger: 'blur' }]
  }))
  const credentialFormRules = computed<FormRules<CredentialFormModel>>(() => ({
    apiKey: [{ required: true, message: t('trading.validation.apiKey'), trigger: 'blur' }],
    apiSecret: [{ required: true, message: t('trading.validation.apiSecret'), trigger: 'blur' }],
    withdrawalDisabled: [{ validator: requiredConfirmationRule, trigger: 'change' }],
    ipWhitelistConfigured: [{ validator: requiredConfirmationRule, trigger: 'change' }]
  }))

  const loadOverview = async () => {
    loading.value = true
    try {
      const data = await fetchTradingOverview()
      Object.assign(overview, data)
      if (!data.accounts.some((account) => account.id === selectedAccountId.value)) {
        selectedAccountId.value = data.accounts[0]?.id || ''
      }
    } finally {
      loading.value = false
    }
  }

  const loadSymbols = async (market: 'spot' | 'usd_m') => {
    symbolsLoading.value = true
    try {
      const result = await fetchMarketSymbols(market)
      availableSymbols.value = result.records
    } finally {
      symbolsLoading.value = false
    }
  }

  const openCreateDialog = async () => {
    dialogMode.value = 'create'
    Object.assign(accountForm, createAccountForm())
    accountDialogVisible.value = true
    await loadSymbols(accountForm.market)
  }

  const openRiskDialog = async () => {
    const account = selectedAccount.value
    if (!account) return
    dialogMode.value = 'risk'
    Object.assign(accountForm, {
      name: account.name,
      market: account.market,
      environment: account.environment,
      initialBalance: account.initialBalance,
      paperFeeRate: account.paperFeeRate,
      instrumentIds: [...account.risk.instrumentIds],
      maxTotalNotional: account.risk.maxTotalNotional || '',
      maxSymbolNotional: account.risk.maxSymbolNotional || '',
      maxOrderNotional: account.risk.maxOrderNotional || '',
      maxDailyLoss: account.risk.maxDailyLoss || '',
      maxDrawdown: account.risk.maxDrawdown || '',
      maxQuoteAgeSeconds: account.risk.maxQuoteAgeSeconds || 30,
      leverage: account.risk.leverage || 2
    })
    accountDialogVisible.value = true
    await loadSymbols(account.market)
  }

  const handleMarketChange = async (value: string | number | boolean) => {
    accountForm.instrumentIds = []
    await loadSymbols(value as 'spot' | 'usd_m')
  }

  const buildRiskPayload = (): TradingRiskPayload => ({
    instrumentIds: [...accountForm.instrumentIds],
    maxTotalNotional: accountForm.maxTotalNotional,
    maxSymbolNotional: accountForm.maxSymbolNotional,
    maxOrderNotional: accountForm.maxOrderNotional,
    maxDailyLoss: accountForm.maxDailyLoss,
    maxDrawdown: accountForm.maxDrawdown,
    maxQuoteAgeSeconds: accountForm.maxQuoteAgeSeconds,
    ...(accountForm.market === 'usd_m' ? { leverage: accountForm.leverage } : {})
  })

  const submitAccountDialog = async () => {
    if (!accountFormRef.value || dialogSubmitting.value) return
    const valid = await accountFormRef.value.validate().catch(() => false)
    if (!valid) return
    dialogSubmitting.value = true
    try {
      if (dialogMode.value === 'create') {
        const account = await fetchCreateTradingAccount(
          {
            name: accountForm.name,
            market: accountForm.market,
            environment: accountForm.environment,
            initialBalance: accountForm.initialBalance,
            paperFeeRate: accountForm.paperFeeRate,
            risk: buildRiskPayload()
          },
          commandKey()
        )
        selectedAccountId.value = account.id
      } else if (selectedAccount.value) {
        const reauthToken = await requestReauth(t('trading.reauth.riskTitle'))
        if (!reauthToken) return
        await fetchUpdateTradingRisk(
          selectedAccount.value.id,
          buildRiskPayload(),
          commandKey(),
          reauthToken
        )
      }
      accountDialogVisible.value = false
      await loadOverview()
    } finally {
      dialogSubmitting.value = false
    }
  }

  const resetCredentialForm = () => {
    Object.assign(credentialForm, createCredentialForm())
    credentialFormRef.value?.clearValidate()
  }

  const openCredentialDialog = () => {
    resetCredentialForm()
    credentialDialogVisible.value = true
  }

  const saveCredentials = async () => {
    const account = selectedAccount.value
    if (!account || account.environment === 'paper' || !credentialFormRef.value) return
    const valid = await credentialFormRef.value.validate().catch(() => false)
    if (!valid || credentialSubmitting.value) return
    const reauthToken = await requestReauth(t('trading.reauth.credentialsTitle'))
    if (!reauthToken) return
    credentialSubmitting.value = true
    try {
      await fetchSaveTradingCredentials(
        account.id,
        {
          apiKey: credentialForm.apiKey,
          apiSecret: credentialForm.apiSecret,
          withdrawalDisabled: credentialForm.withdrawalDisabled,
          ipWhitelistConfigured: credentialForm.ipWhitelistConfigured
        },
        commandKey(),
        reauthToken
      )
      credentialDialogVisible.value = false
      await loadOverview()
    } finally {
      credentialSubmitting.value = false
    }
  }

  const revokeCredentials = async () => {
    const account = selectedAccount.value
    if (!account || account.environment === 'paper') return
    try {
      await ElMessageBox.confirm(
        t('trading.credentials.revokeConfirm'),
        t('trading.credentials.revokeTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )
    } catch (error) {
      if (error === 'cancel' || error === 'close') return
      throw error
    }
    const reauthToken = await requestReauth(t('trading.reauth.credentialsTitle'))
    if (!reauthToken) return
    await runCommand('credential-revoke', async () => {
      await fetchRevokeTradingCredentials(account.id, commandKey(), reauthToken)
    })
  }

  const activateEmergencyStop = async () => {
    const reason = await requestText(
      t('trading.control.stopTitle'),
      t('trading.control.reasonPlaceholder')
    )
    if (!reason) return
    await runCommand('emergency-stop', async () => {
      await fetchActivateTradingEmergencyStop(reason, commandKey())
    })
  }

  const releaseEmergencyStop = async () => {
    const token = await requestReauth(t('trading.reauth.releaseTitle'))
    if (!token) return
    await runCommand('emergency-release', async () => {
      await fetchReleaseTradingEmergencyStop(commandKey(), token)
    })
  }

  const resumeAccount = async () => {
    const account = selectedAccount.value
    if (!account) return
    const token = await requestReauth(t('trading.reauth.resumeTitle'))
    if (!token) return
    await runCommand('resume', async () => {
      await fetchResumeTradingAccount(account.id, commandKey(), token)
    })
  }

  const setAutomation = async (value: string | number | boolean) => {
    const account = selectedAccount.value
    if (!account) return
    const enabled = Boolean(value)
    const token = enabled ? await requestReauth(t('trading.reauth.automationTitle')) : undefined
    if (enabled && !token) return
    await runCommand('automation', async () => {
      await fetchSetTradingAutomation(account.id, enabled, commandKey(), token ?? undefined)
    })
  }

  const setAuthorization = async (value: string | number | boolean) => {
    const account = selectedAccount.value
    if (!account || !isSuper.value) return
    const token = await requestReauth(t('trading.reauth.authorizationTitle'))
    if (!token) return
    await runCommand('authorization', async () => {
      await fetchSetTradingAuthorization(account.id, Boolean(value), commandKey(), token)
    })
  }

  const runCommand = async (name: string, command: () => Promise<void>) => {
    if (commandLoading.value) return
    commandLoading.value = name
    try {
      await command()
      await loadOverview()
    } finally {
      commandLoading.value = ''
    }
  }

  const requestReauth = async (title: string) => {
    try {
      const result = await ElMessageBox.prompt(t('trading.reauth.message'), title, {
        inputType: 'password',
        inputPlaceholder: t('trading.reauth.password'),
        inputValidator: (value) => Boolean(value?.trim()) || t('trading.reauth.required'),
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel')
      })
      return (await fetchReauth(result.value)).reauthToken
    } catch (error) {
      if (error === 'cancel' || error === 'close') return null
      throw error
    }
  }

  const requestText = async (title: string, placeholder: string) => {
    try {
      const result = await ElMessageBox.prompt('', title, {
        inputPlaceholder: placeholder,
        inputValidator: (value) => Boolean(value?.trim()) || t('trading.validation.reason'),
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel')
      })
      return result.value.trim()
    } catch (error) {
      if (error === 'cancel' || error === 'close') return ''
      throw error
    }
  }

  const commandKey = () => crypto.randomUUID()
  const amountLimit = (value?: string | null) => (value ? `${value} USDT` : '--')
  const balanceValue = (key: keyof PaperBalance) => String(selectedBalance.value?.[key] || '0')
  const pnlClass = (value?: string | null) => ({
    'is-positive': Number(value || 0) > 0,
    'is-negative': Number(value || 0) < 0
  })
  const marketLabel = (market: string) =>
    market === 'usd_m' ? t('trading.market.usdM') : t('trading.market.spot')
  const accountStatusLabel = (status: string) =>
    status === 'active' ? t('trading.status.active') : t('trading.status.paused')
  const sideLabel = (side: string) =>
    side === 'buy' ? t('trading.side.buy') : t('trading.side.sell')
  const modeLabel = (mode: string) =>
    mode === 'auto' ? t('trading.mode.auto') : t('trading.mode.manual')
  const orderStatusLabel = (status: string) =>
    t(`trading.order.${status}` as 'trading.order.filled')
  const orderStatusType = (status: string): TagProps['type'] => {
    if (status === 'filled') return 'success'
    if (status === 'rejected' || status === 'canceled' || status === 'expired') return 'danger'
    if (status === 'unknown' || status === 'partially_filled') return 'warning'
    return 'info'
  }
  const orderPurposeLabel = (purpose: string) =>
    t(`trading.orderPurpose.${purpose}` as 'trading.orderPurpose.rebalance')
  const orderPurposeType = (purpose: string): TagProps['type'] => {
    if (purpose === 'protection') return 'warning'
    if (purpose === 'flatten') return 'danger'
    if (purpose === 'rebalance') return 'success'
    return 'info'
  }
  const orderTypeLabel = (orderType: string) => {
    const key = `trading.orderType.${orderType}`
    return te(key) ? t(key) : orderType || '--'
  }
  const intentStatusLabel = (status: string) =>
    t(`trading.intent.${status}` as 'trading.intent.pending')
  const intentStatusType = (status: string): TagProps['type'] => {
    if (status === 'executed') return 'success'
    if (status === 'blocked' || status === 'failed') return 'danger'
    if (status === 'processing' || status === 'reconciling') return 'warning'
    return 'info'
  }
  const tradeFactEventLabel = (eventType: string) =>
    t(`trading.tradeFact.${eventType}` as 'trading.tradeFact.fill')
  const tradeFactEventType = (eventType: string): TagProps['type'] => {
    if (eventType === 'fill') return 'success'
    if (eventType === 'fee') return 'warning'
    return 'info'
  }
  const formatTime = (value?: string | null) => {
    if (!value) return '--'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return new Intl.DateTimeFormat(locale.value.startsWith('zh') ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
      timeZone: 'UTC'
    }).format(date)
  }

  onMounted(() => {
    void loadOverview()
  })

  watch(selectedAccountId, () => {
    if (selectedAccount.value?.environment === 'paper' && activeTab.value === 'ledger') {
      activeTab.value = 'positions'
    }
  })

  watch(
    () => accountForm.environment,
    (environment) => {
      if (environment !== 'live') return
      if (accountForm.market === 'spot' && !overview.capabilities.spotLiveManualEnabled) {
        accountForm.market = 'usd_m'
      } else if (accountForm.market === 'usd_m' && !overview.capabilities.usdMLiveManualEnabled) {
        accountForm.market = 'spot'
      }
    }
  )
</script>

<style scoped lang="scss">
  .trading-page {
    --trading-ink: #19242d;
    --trading-muted: #697681;
    --trading-line: var(--el-border-color-light);
    --trading-active: #25745a;
    --trading-stop: #b83f45;
    --trading-warning: #a76616;
    --trading-focus: #3569a8;

    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
    color: var(--el-text-color-primary);
    letter-spacing: 0;
  }

  .page-toolbar,
  .safety-rail,
  .account-rail-header,
  .account-heading,
  .account-title-row,
  .account-actions,
  .account-switches,
  .credential-strip,
  .credential-summary,
  .credential-state,
  .credential-actions,
  .risk-ledger > header,
  .audit-summary > header {
    display: flex;
    align-items: center;
  }

  .page-toolbar {
    justify-content: space-between;
    min-height: 52px;

    h1 {
      margin: 3px 0 0;
      font-size: 22px;
      font-weight: 650;
      line-height: 1.2;
    }
  }

  .context-label {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    font-weight: 700;
    color: var(--trading-muted);
  }

  .icon-button {
    width: 34px;
    min-width: 34px;
    height: 34px;
  }

  .safety-rail {
    display: grid;
    grid-template-columns: 38px minmax(180px, 1fr) auto auto;
    gap: 14px;
    min-height: 76px;
    padding: 12px 16px;
    border: 1px solid;
    border-radius: 6px;

    &.is-running {
      background: color-mix(in srgb, var(--trading-active) 8%, var(--el-bg-color));
      border-color: color-mix(in srgb, var(--trading-active) 38%, var(--trading-line));

      .safety-symbol,
      .safety-copy strong {
        color: var(--trading-active);
      }
    }

    &.is-stopped {
      background: color-mix(in srgb, var(--trading-stop) 9%, var(--el-bg-color));
      border-color: color-mix(in srgb, var(--trading-stop) 42%, var(--trading-line));

      .safety-symbol,
      .safety-copy strong {
        color: var(--trading-stop);
      }
    }
  }

  .safety-symbol {
    display: grid;
    place-items: center;
    width: 38px;
    height: 38px;
    background: var(--el-bg-color);
    border: 1px solid currentcolor;
    border-radius: 50%;
  }

  .safety-copy,
  .safety-meta {
    display: flex;
    flex-direction: column;
    justify-content: center;
    min-width: 0;

    span,
    small {
      font-size: 12px;
      color: var(--trading-muted);
    }

    strong {
      margin-top: 2px;
      font-size: 15px;
    }

    small {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .safety-meta {
    min-width: 176px;
    padding-right: 8px;
    border-right: 1px solid var(--trading-line);

    strong {
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 12px;
      color: var(--el-text-color-regular);
    }
  }

  .empty-state,
  .trading-workspace {
    background: var(--el-bg-color);
    border: 1px solid var(--trading-line);
    border-radius: 6px;
  }

  .empty-state {
    min-height: 360px;
  }

  .trading-workspace {
    display: grid;
    grid-template-columns: 248px minmax(0, 1fr);
    min-height: 640px;
    overflow: hidden;
  }

  .account-rail {
    min-width: 0;
    background: color-mix(in srgb, var(--el-fill-color-light) 65%, var(--el-bg-color));
    border-right: 1px solid var(--trading-line);
  }

  .account-rail-header {
    justify-content: space-between;
    min-height: 64px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--trading-line);

    div {
      display: flex;
      gap: 8px;
      align-items: baseline;
    }

    span {
      font-size: 13px;
      color: var(--trading-muted);
    }

    strong {
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 14px;
    }
  }

  .account-list {
    display: flex;
    flex-direction: column;
  }

  .account-item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 10px;
    align-items: center;
    min-height: 68px;
    padding: 10px 14px;
    color: inherit;
    text-align: left;
    cursor: pointer;
    background: transparent;
    border: 0;
    border-bottom: 1px solid var(--trading-line);

    &:hover,
    &:focus-visible {
      background: var(--el-fill-color-light);
    }

    &:focus-visible {
      outline: 2px solid var(--trading-focus);
      outline-offset: -2px;
    }

    &.is-selected {
      background: var(--el-bg-color);
      box-shadow: inset 3px 0 0 var(--trading-focus);
    }
  }

  .account-item-main {
    min-width: 0;

    strong,
    small {
      display: block;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    strong {
      font-size: 14px;
    }

    small {
      margin-top: 4px;
      font-size: 11px;
      color: var(--trading-muted);
    }
  }

  .account-state {
    width: 8px;
    height: 8px;
    overflow: hidden;
    color: transparent;
    border-radius: 50%;

    &.active {
      background: var(--trading-active);
    }

    &.paused {
      background: var(--trading-warning);
    }
  }

  .mobile-account-select {
    display: none;
  }

  .account-detail {
    min-width: 0;
    padding: 20px;
  }

  .account-heading {
    gap: 16px;
    justify-content: space-between;
    min-height: 48px;
  }

  .account-title {
    min-width: 0;
  }

  .account-title-row {
    flex-wrap: wrap;
    gap: 8px;

    h2 {
      max-width: 100%;
      margin: 0;
      overflow: hidden;
      font-size: 19px;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .pause-reason {
    display: block;
    margin-top: 4px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 11px;
    color: var(--trading-warning);
  }

  .account-actions {
    flex: 0 0 auto;
    gap: 8px;
  }

  .account-switches {
    gap: 28px;
    min-height: 54px;
    padding: 8px 0;
    margin-top: 14px;
    border-top: 1px solid var(--trading-line);
    border-bottom: 1px solid var(--trading-line);

    label {
      display: flex;
      gap: 10px;
      align-items: center;
      font-size: 13px;
      color: var(--el-text-color-regular);
    }
  }

  .credential-strip {
    gap: 16px;
    justify-content: space-between;
    min-height: 64px;
    padding: 10px 0;
    border-bottom: 1px solid var(--trading-line);
  }

  .credential-summary {
    flex-wrap: wrap;
    gap: 18px;
    min-width: 0;
  }

  .credential-state {
    gap: 10px;
    min-width: 0;
    color: var(--trading-focus);

    div {
      min-width: 0;
    }

    span,
    strong,
    small {
      display: block;
    }

    span,
    small {
      font-size: 11px;
      color: var(--trading-muted);
    }

    strong {
      margin: 2px 0;
      font-size: 13px;
      color: var(--el-text-color-primary);
    }
  }

  .credential-actions {
    flex-wrap: wrap;
    gap: 8px;
    justify-content: flex-end;

    :deep(.el-button + .el-button) {
      margin-left: 0;
    }
  }

  .balance-strip {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    margin-top: 18px;
    border-top: 1px solid var(--trading-line);
    border-bottom: 1px solid var(--trading-line);

    > div {
      min-width: 0;
      padding: 15px 14px;
      border-right: 1px solid var(--trading-line);

      &:first-child {
        padding-left: 0;
      }

      &:last-child {
        padding-right: 0;
        border-right: 0;
      }
    }

    span,
    strong,
    small {
      display: block;
    }

    span {
      font-size: 11px;
      color: var(--trading-muted);
    }

    strong {
      margin-top: 7px;
      overflow: hidden;
      font-size: 15px;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    small {
      margin-top: 5px;
      font-size: 11px;
      color: var(--trading-muted);

      span {
        display: inline;
      }
    }
  }

  .decimal-value {
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-variant-numeric: tabular-nums;
  }

  .order-shape {
    display: flex;
    flex-direction: column;
    gap: 2px;

    small {
      color: var(--trading-muted);
    }
  }

  .is-positive {
    color: var(--trading-active);
  }

  .is-negative {
    color: var(--trading-stop);
  }

  .risk-ledger {
    padding: 14px 0 0;
    margin-top: 20px;
    border-top: 2px solid var(--trading-ink);

    > header {
      gap: 16px;
      justify-content: space-between;

      div {
        display: flex;
        gap: 8px;
        align-items: baseline;
      }

      span,
      small {
        font-size: 11px;
        color: var(--trading-muted);
      }

      strong {
        font-size: 13px;
      }

      small {
        max-width: 55%;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
    }

    dl {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      margin: 12px 0 0;
      border-top: 1px solid var(--trading-line);
      border-left: 1px solid var(--trading-line);
    }

    dl > div {
      min-width: 0;
      padding: 10px;
      border-right: 1px solid var(--trading-line);
      border-bottom: 1px solid var(--trading-line);
    }

    dt {
      font-size: 10px;
      color: var(--trading-muted);
    }

    dd {
      margin: 5px 0 0;
      overflow: hidden;
      font-size: 12px;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .audit-summary {
    padding: 14px 0 0;
    margin-top: 20px;
    border-top: 2px solid var(--trading-ink);

    > header {
      gap: 16px;
      justify-content: space-between;

      div {
        display: flex;
        gap: 8px;
        align-items: baseline;
      }

      span {
        font-size: 11px;
        color: var(--trading-muted);
      }

      strong {
        font-size: 13px;
      }
    }
  }

  .audit-error {
    display: flex;
    gap: 8px;
    align-items: baseline;
    padding: 10px 0 0;
    font-size: 11px;
    color: var(--trading-muted);

    code {
      overflow: hidden;
      color: var(--trading-stop);
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .audit-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    margin: 12px 0 0;
    border-top: 1px solid var(--trading-line);
    border-left: 1px solid var(--trading-line);

    > div {
      min-width: 0;
      padding: 10px;
      border-right: 1px solid var(--trading-line);
      border-bottom: 1px solid var(--trading-line);
    }

    dt {
      font-size: 10px;
      color: var(--trading-muted);
    }

    dd {
      margin: 5px 0 0;
      overflow: hidden;
      font-size: 12px;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .audit-risk-grid {
    margin-top: 10px;
  }

  .audit-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 12px 24px;
    padding-top: 10px;
    font-size: 11px;
    color: var(--trading-muted);
  }

  .trading-tabs {
    margin-top: 20px;
  }

  .form-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0 16px;
  }

  .risk-form-grid {
    padding-top: 4px;
    border-top: 1px solid var(--trading-line);
  }

  .full-width,
  :deep(.el-input-number),
  :deep(.el-segmented) {
    width: 100%;
  }

  :deep(.el-table) {
    width: 100%;
  }

  @media (max-width: 980px) {
    .trading-workspace {
      grid-template-columns: 210px minmax(0, 1fr);
    }

    .balance-strip {
      grid-template-columns: repeat(2, minmax(0, 1fr));

      > div:nth-child(2) {
        border-right: 0;
      }

      > div:nth-child(-n + 2) {
        border-bottom: 1px solid var(--trading-line);
      }

      > div:nth-child(3) {
        padding-left: 0;
      }
    }

    .risk-ledger dl {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .audit-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 720px) {
    .safety-rail {
      grid-template-columns: 38px minmax(0, 1fr);

      .safety-meta {
        grid-column: 2;
        min-width: 0;
        padding: 0;
        border: 0;
      }

      .el-button {
        grid-column: 1 / -1;
        width: 100%;
        margin: 0;
      }
    }

    .trading-workspace {
      display: block;
    }

    .account-rail {
      padding: 12px;
      border-right: 0;
      border-bottom: 1px solid var(--trading-line);
    }

    .account-rail-header {
      min-height: 42px;
      padding: 0 0 10px;
      border: 0;
    }

    .account-list {
      display: none;
    }

    .mobile-account-select {
      display: block;
      width: 100%;
    }

    .account-detail {
      padding: 16px 12px;
    }

    .account-heading {
      align-items: flex-start;
    }

    .account-actions {
      flex-direction: column;
      align-items: flex-end;
    }

    .account-switches {
      flex-direction: column;
      gap: 10px;
      align-items: stretch;

      label {
        justify-content: space-between;
      }
    }

    .credential-strip {
      flex-direction: column;
      align-items: stretch;
    }

    .credential-summary {
      align-items: flex-start;
    }

    .credential-actions {
      justify-content: flex-start;

      .el-button {
        flex: 1 1 auto;
      }
    }

    .balance-strip {
      grid-template-columns: 1fr;

      > div,
      > div:first-child,
      > div:nth-child(3) {
        padding: 12px 0;
        border-right: 0;
        border-bottom: 1px solid var(--trading-line);
      }

      > div:last-child {
        border-bottom: 0;
      }
    }

    .risk-ledger {
      > header {
        align-items: flex-start;
      }

      > header small {
        max-width: 48%;
        white-space: normal;
      }

      dl {
        grid-template-columns: 1fr;
      }
    }

    .audit-summary {
      > header {
        align-items: flex-start;
      }
    }

    .audit-grid {
      grid-template-columns: 1fr;
    }

    .form-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
