package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

// StartRuntime 启动全部后台循环:调度、派发、事件、恢复与清理。
// 单进程内 goroutine 协作,替代原 orchestrator/worker 双进程 + Redis。
func (a *App) StartRuntime() {
	if a.runtimeCtx == nil || a.runtimeCtx.Err() != nil {
		return
	}
	a.bootstrapRuntimeEntries(a.runtimeCtx)
	a.reconcileScheduleRegistrations(a.runtimeCtx)

	// spawn 会用 go 关键字为每个循环各开一条 goroutine(并发执行流),
	// 于是这五条后台循环同时运行、互不阻塞。见 GO入门笔记『并发』。
	a.spawn(a.schedulerLoop)
	a.spawn(a.dispatchLoop)
	a.spawn(a.eventOutboxLoop)
	a.spawn(a.workflowWaitLoop)
	a.spawn(a.staleRecoveryLoop)
	a.spawn(a.cleanupLoop)
	if a.Paper != nil {
		a.spawn(func(ctx context.Context) {
			if err := a.Paper.Run(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "paper executor stopped", "error_category", "database")
			}
		})
	}
	if a.MarketData != nil {
		a.spawn(func(ctx context.Context) {
			if err := a.MarketData.Run(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "market data runtime stopped", "error_category", "market_data")
			}
		})
	}
	slog.InfoContext(a.runtimeCtx, "runtime started", "worker_id", a.WorkerID, "concurrency", a.Cfg.Workflow.ExecutorConcurrency)
}

// StopRuntime 通知全部循环退出并等待收尾。
func (a *App) StopRuntime(ctx context.Context) error {
	a.runtimeCancel()
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("runtime stopped", "worker_id", a.WorkerID)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// spawn 统一负责"开一条后台 goroutine 并把它纳入等待组 wg"。
// 参数 loop func() 的类型是"函数":表示传进来一个无参数、无返回值的函数值,把要并发跑的循环当值传入。
func (a *App) spawn(loop func(context.Context)) {
	// wg.Add(1):等待组计数 +1,登记"又多了一条要等它结束的 goroutine"。见 GO入门笔记『并发』。
	a.wg.Add(1)
	// go func(){ ... }() 用 go 关键字开一条新的并发执行流(goroutine);
	// 末尾的 () 表示"立刻调用"这个匿名函数,函数体就在新 goroutine 里跑。
	go func() {
		// defer 登记收尾动作:不管下面的 loop() 怎样退出,本 goroutine 结束前一定执行 wg.Done()(计数 -1)。见 GO入门笔记『defer』。
		defer a.wg.Done()
		loop(a.runtimeCtx)
	}()
}

// sleeping 实现"可被中断的睡眠":要么睡满 interval,要么中途被停止信号叫醒。
// 返回 bool:true = 正常睡够(调用方可继续下一轮循环);false = 收到停止信号(该退出循环了)。
// interval time.Duration 是 Go 的"时间长度"类型(按纳秒计),如 500*time.Millisecond。见 GO入门笔记『复合类型』。
func (a *App) sleeping(ctx context.Context, interval time.Duration) bool {
	// select 同时等多个 channel,哪个 case 先就绪就走哪个;这是 Go 并发协调的核心。见 GO入门笔记『并发』。
	select {
	// 若 ctx 被取消,这个接收立刻就绪 → 返回 false,让调用方的 for 循环退出。
	case <-ctx.Done():
		return false
	// 否则 time.After 会在 interval 之后才送来一个值 → 说明睡够了,返回 true。
	case <-time.After(interval):
		return true
	}
}

// wakeDispatcher 立刻叫醒派发循环(有新任务入队时用),免得白等一个轮询周期。
// 这是"非阻塞发送"写法:能塞进 channel 就塞;塞不进(说明已有一个唤醒信号在排队)就走 default 直接返回,绝不阻塞。
func (a *App) wakeDispatcher() {
	select {
	// struct{}{} 是一个"零字节"的空结构体值,channel 只借它传"有事发生"这一信号,不关心内容。见 GO入门笔记『并发』。
	case a.dispatcherWake <- struct{}{}:
	// default 让 select 在没有 case 就绪时不等待、立即返回。
	default:
	}
}

// bootstrapRuntimeEntries 启动时为已激活但没有入口记录的 workflow 重建入口
// (覆盖种子数据只写激活状态不写入口的情况)。
func (a *App) bootstrapRuntimeEntries(ctx context.Context) {
	// var states []T 声明一个切片(slice):可变长数组,[]db.WorkflowRuntimeState 表示"一堆运行态记录"。见 GO入门笔记『复合类型』。
	var states []db.WorkflowRuntimeState
	// GORM:等价 SQL "SELECT * FROM workflow_runtime_states WHERE active_workflow_definition_id IS NOT NULL"。
	// &states 传切片的地址,GORM 把查询结果写回到 states 里。见 GO入门笔记『框架:GORM』。
	a.DB.WithContext(ctx).Where("active_workflow_definition_id IS NOT NULL").Find(&states)
	// for i := range states 遍历切片,i 是下标;:= 是短声明(自动推断类型,只能在函数内用)。见 GO入门笔记『复合类型』『变量、函数、错误』。
	for i := range states {
		if ctx.Err() != nil {
			return
		}
		// &states[i] 取第 i 个元素的地址,得到指针;之后对 state 的读写就直接作用在原元素上。见 GO入门笔记『复合类型』。
		state := &states[i]
		// Count、重建和激活共享 family 锁，避免两个实例启动时互相删建同一套入口。
		err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if state.ActiveWorkflowDefinitionID == nil {
				return nil
			}
			if _, err := lockWorkflowDefinitionFamily(tx, *state.ActiveWorkflowDefinitionID); err != nil {
				return err
			}
			current, err := findRuntimeStateByCodeWithDB(tx, state.OwnerUserID, state.WorkflowCode)
			if err != nil {
				return err
			}
			if current == nil || current.ActiveWorkflowDefinitionID == nil {
				return nil
			}
			var entryCount int64
			if err := tx.Model(&db.WorkflowRuntimeEntry{}).Where("workflow_runtime_state_id = ?", current.ID).Count(&entryCount).Error; err != nil {
				return err
			}
			if entryCount > 0 {
				return nil
			}
			return a.reconcileRuntimeEntriesForStateWithDB(tx, current, false)
		})
		if err != nil {
			slog.ErrorContext(ctx, "runtime entry bootstrap failed", "workflow_code", state.WorkflowCode, "error_category", "database")
		}
	}
}

// ---------- 调度循环 ----------

// 后台循环之一【调度】:定时扫描"到点该跑"的定时任务并入队。
// schedulerLoop 扫描到期的 schedule 入口并入队执行。
func (a *App) schedulerLoop(ctx context.Context) {
	// time.Duration 是时长类型;毫秒配置值 × time.Millisecond 得到一个真正的时长。见 GO入门笔记『复合类型』。
	pollInterval := time.Duration(a.Cfg.Workflow.PollIntervalMs) * time.Millisecond
	reconcileInterval := time.Duration(a.Cfg.Workflow.ScheduleReconcileIntervalSeconds) * time.Second
	lastReconcile := time.Now()
	// for a.sleeping(pollInterval) 是本项目所有后台循环的通用骨架:
	// 每轮先睡一个轮询间隔;睡够返回 true 就继续下一轮,收到停止信号返回 false 则循环自然结束、goroutine 退出。见 GO入门笔记『并发』。
	for a.sleeping(ctx, pollInterval) {
		a.fireDueScheduleEntries(ctx)
		// 每隔 reconcileInterval 再对齐一次调度注册状态(而不是每一轮都做)。
		if time.Since(lastReconcile) >= reconcileInterval {
			a.reconcileScheduleRegistrations(ctx)
			lastReconcile = time.Now()
		}
	}
}

// fireDueScheduleEntries 找出所有 next_run_at 已到期的调度入口,逐个"抢占并触发"。
// 这里体现"数据库即队列":不依赖 Redis,靠一条带条件的 UPDATE 来抢触发权(见下方乐观锁注释)。
func (a *App) fireDueScheduleEntries(ctx context.Context) {
	now := time.Now()
	var entries []db.WorkflowRuntimeEntry
	// 等价一条带 JOIN 的 SELECT:取出已启用、已注册、next_run_at 已到期、且绑定在当前激活定义上的 schedule 入口。
	// GORM 里 ? 是占位符,值单独传(下面几行末尾的 "schedule", true, "registered", now),由驱动转义、防 SQL 注入。见 GO入门笔记『框架:GORM』。
	a.DB.WithContext(ctx).Joins("JOIN workflow_runtime_states ON workflow_runtime_entries.workflow_runtime_state_id = workflow_runtime_states.id").
		Where(
			"workflow_runtime_entries.start_type = ? AND workflow_runtime_entries.is_enabled = ? "+
				"AND workflow_runtime_entries.registration_status = ? "+
				"AND workflow_runtime_entries.next_run_at IS NOT NULL AND workflow_runtime_entries.next_run_at <= ? "+
				"AND workflow_runtime_states.active_workflow_definition_id = workflow_runtime_entries.workflow_definition_id",
			"schedule", true, "registered", now,
		).
		Limit(a.Cfg.Workflow.OutboxBatchSize).
		Find(&entries)

	for i := range entries {
		if ctx.Err() != nil {
			return
		}
		entry := &entries[i]
		// *entry.NextRunAt:entry.NextRunAt 是指针(*time.Time),用 * 顺着指针取出它指向的时间值。见 GO入门笔记『复合类型』。
		dueAt := *entry.NextRunAt
		// 先推进 next_run_at 抢占触发权(乐观锁),避免重复触发。
		next := a.computeEntryNextRun(entry, now)
		// 关键的乐观锁认领:UPDATE ... WHERE id=? AND next_run_at=<刚刚读到的到期时间>。
		// 谁的这条 UPDATE 真正改到了行(RowsAffected==1),谁就"抢到"了这次触发;
		// WHERE 里带上旧的 next_run_at 作条件,保证只有第一个抢到的会成功——这就是不用 Redis 锁也能防重复触发的原理。
		claim := a.DB.Model(&db.WorkflowRuntimeEntry{}).
			Where("id = ? AND next_run_at = ?", entry.ID, dueAt)
		var updated int64
		if next != nil {
			// RowsAffected 是这条 UPDATE 实际改动的行数:1=抢到,0=被别人抢先。
			updated = claim.Update("next_run_at", *next).RowsAffected
		} else {
			updated = claim.Update("next_run_at", nil).RowsAffected
		}
		if updated == 0 {
			// 没抢到(别的调度已把 next_run_at 推进过),放弃本条,交给抢到的那一方去触发。
			continue
		}
		timestamp := int64Text(dueAt.Unix())
		// idempotencyKey 用"入口ID + 到期秒数"拼成:即使因并发/重试导致同一时刻被触发两次,
		// 入队时也会因幂等键相同而被去重成同一条执行(详见 runtime.go 的 enqueueStartNodeExecution)。
		// _, err := ... 里的 _ 丢弃第一个返回值(执行体),只关心 err。Go 函数可返回多个值。见 GO入门笔记『变量、函数、错误』。
		_, err := a.RunRuntimeEntry(entry.ID, M{
			"triggerType":    "schedule",
			"triggerKey":     "schedule:" + int64Text(entry.ID) + ":" + timestamp,
			"idempotencyKey": "schedule:" + int64Text(entry.ID) + ":" + timestamp,
			"payload":        M{},
		})
		// 忽略积压超限；其他错误记录分类并回写入口。
		if err != nil && !errors.Is(err, ErrBacklogExceeded) {
			category, _ := classifyFailure(err, err.Error())
			slog.WarnContext(ctx, "scheduler entry failed", "entry_id", entry.ID, "error_category", category)
			a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", entry.ID).
				Updates(map[string]any{"last_error_message": err.Error(), "updated_at": time.Now()})
		}
	}
}

// computeEntryNextRun 读取该入口的调度配置,算出"这次之后的下一次触发时间"。
// 返回 *time.Time(时间指针):算不出来时返回 nil,表示"不再有下一次"。指针能用 nil 表达"没有值"。见 GO入门笔记『复合类型』。
func (a *App) computeEntryNextRun(entry *db.WorkflowRuntimeEntry, after time.Time) *time.Time {
	definition, err := a.requireDefinition(entry.WorkflowDefinitionID)
	if err != nil {
		return nil
	}
	graph := loadJSONObject(definition.GraphJSON)
	startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.schedule")
	if startNode == nil {
		return nil
	}
	// x.(map[string]any) 是"类型断言":startNode["config"] 的静态类型是 any,断言它其实是个 map 再用;
	// 第二个返回值(这里用 _ 丢弃)表示断言是否成功。见 GO入门笔记『复合类型』。
	config, _ := startNode["config"].(map[string]any)
	next, err := computeNextScheduleTime(config, after)
	if err != nil {
		return nil
	}
	return next
}

// reconcileScheduleRegistrations 对齐 schedule 入口注册状态。
func (a *App) reconcileScheduleRegistrations(ctx context.Context) {
	var states []db.WorkflowRuntimeState
	a.DB.WithContext(ctx).Find(&states)
	for i := range states {
		if ctx.Err() != nil {
			return
		}
		state := &states[i]
		activeDefinitionID := int64(0)
		if state.ActiveWorkflowDefinitionID != nil {
			activeDefinitionID = *state.ActiveWorkflowDefinitionID
		}
		var entries []db.WorkflowRuntimeEntry
		a.DB.Where("workflow_runtime_state_id = ?", state.ID).Find(&entries)
		for j := range entries {
			entry := &entries[j]
			if entry.StartType != "schedule" {
				status := "disabled"
				if entry.IsEnabled {
					status = "registered"
				}
				if entry.RegistrationStatus != status {
					// Updates(map[string]any{...}) 只更新给出的这几列(局部 UPDATE),不动其它列;map 是键值对集合。见 GO入门笔记『复合类型』『框架:GORM』。
					a.DB.Model(entry).Updates(map[string]any{"registration_status": status, "updated_at": time.Now()})
				}
				continue
			}
			if !entry.IsEnabled || entry.WorkflowDefinitionID != activeDefinitionID {
				if entry.RegistrationStatus != "disabled" {
					a.DB.Model(entry).Updates(map[string]any{
						"registration_status": "disabled", "schedule_job_id": "", "next_run_at": nil, "updated_at": time.Now(),
					})
				}
				continue
			}
			// 缺少 next_run_at 的已启用调度入口重新注册。
			if entry.NextRunAt == nil || entry.RegistrationStatus != "registered" {
				if err := a.registerScheduleEntry(entry); err != nil {
					a.DB.Model(entry).Updates(map[string]any{
						"registration_status": "error", "last_error_message": err.Error(), "updated_at": time.Now(),
					})
				}
			}
		}
	}
}

// ---------- 派发与执行 ----------

// 后台循环之二【派发】:把"排队中"的执行认领下来,交给 worker 池并发执行。
// dispatchLoop 提升到期重试 + 认领 queued 执行,交给 worker 池。
func (a *App) dispatchLoop(ctx context.Context) {
	pollInterval := time.Duration(a.Cfg.Workflow.PollIntervalMs) * time.Millisecond
	// make(chan struct{}, N) 建一个"带缓冲"的 channel,容量 N = 最大并发数。
	// 它当"令牌桶 / 信号量"用:桶里有 N 个空位,占到一个才能开跑,跑完再还回来(见 claimAndRun)。见 GO入门笔记『并发』。
	slots := make(chan struct{}, a.Cfg.Workflow.ExecutorConcurrency)
	// for {} 是无限循环(没有条件),靠里面的 case <-ctx.Done(): return 来退出。
	for {
		// select 在这里同时等三件事,谁先来走谁:
		select {
		// 1) 收到停止信号 → return 结束本 goroutine。
		case <-ctx.Done():
			return
		// 2) 有人调用 wakeDispatcher 塞了信号 → 立刻醒来干活(新任务入队时用,免得空等)。
		case <-a.dispatcherWake:
		// 3) 前两者都没发生也没关系:睡满 pollInterval 后照样醒来轮询一次(兜底)。
		case <-time.After(pollInterval):
		}
		if ctx.Err() != nil {
			return
		}
		a.promoteDueRetries(ctx)
		a.claimAndRun(ctx, slots)
	}
}

// promoteDueRetries 把"到点该重试"的执行从 retry_waiting 批量改回 queued,让它们重新可被认领。
// 等价 SQL:UPDATE workflow_executions SET status='queued', next_retry_at=NULL WHERE status='retry_waiting' AND next_retry_at<=now。
func (a *App) promoteDueRetries(ctx context.Context) {
	now := time.Now()
	a.DB.WithContext(ctx).Model(&db.WorkflowExecution{}).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", "retry_waiting", now).
		Updates(map[string]any{"status": "queued", "next_retry_at": nil})
}

// claimAndRun 在并发上限内尽量多地认领待执行任务,每认领一条就开一条 goroutine 去跑。
func (a *App) claimAndRun(ctx context.Context, slots chan struct{}) {
	for {
		if ctx.Err() != nil {
			return
		}
		// 非阻塞地"占一个并发槽":能往 slots 塞进一个令牌就继续;塞不进(桶满)就走 default 直接返回,不阻塞。
		select {
		case slots <- struct{}{}:
		default:
			return // 并发槽已满。
		}
		execution := a.claimNextExecution(ctx)
		if execution == nil {
			// 没有可认领的任务:把刚占的槽还回来(<-slots 从 channel 取出一个令牌)再退出。
			<-slots
			return
		}
		a.wg.Add(1)
		// 为这条执行单独开一条 goroutine 并发跑。把 execution 作为参数传进匿名函数,
		// 是为了让每条 goroutine 拿到自己的那份 execution,避免循环变量被共享/覆盖。
		go func(execution *db.WorkflowExecution) {
			// 两个 defer 按"后进先出"执行:goroutine 结束时先归还并发槽(<-slots),再让等待组计数 -1。见 GO入门笔记『defer』『并发』。
			defer a.wg.Done()
			defer func() { <-slots }()
			a.processExecution(ctx, execution)
		}(execution)
	}
}

// claimNextExecution 乐观认领一条 queued 执行(跳过并发键被占用的)。
func (a *App) claimNextExecution(ctx context.Context) *db.WorkflowExecution {
	var candidates []db.WorkflowExecution
	// 先按入队时间捞出最多 20 条 queued 候选(SELECT ... WHERE status='queued' ORDER BY queued_at LIMIT 20)。
	// 注意:这一步只是"看看有哪些",还没认领——真正的认领靠下面那条带条件的 UPDATE。
	a.DB.WithContext(ctx).Where("status = ?", "queued").Order("queued_at ASC, id ASC").Limit(20).Find(&candidates)
	for i := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		candidate := &candidates[i]
		// 先在进程内抢并发键信号量;抢不到说明同键任务已在跑,跳过看下一条。
		if !a.tryAcquireKey(candidate.ConcurrencyKey) {
			continue
		}
		now := time.Now()
		// 数据库即队列的核心认领动作:UPDATE ... SET status='running',... WHERE id=? AND status='queued'。
		// WHERE 里再次要求 status 仍是 'queued':只有把它从 queued 改成 running 的那一个 worker 会成功,
		// 天然互斥,不需要 Redis 分布式锁——这就是"用一条带条件的 UPDATE 当队列出队"的设计。
		result := a.DB.WithContext(ctx).Model(&db.WorkflowExecution{}).
			Where("id = ? AND status = ?", candidate.ID, "queued").
			Updates(map[string]any{
				"status": "running", "claimed_at": now, "started_at": now,
				"finished_at": nil, "last_heartbeat_at": now, "worker_id": a.WorkerID,
				"next_retry_at": nil, "failure_category": "", "error_message": "",
				"attempt_count": candidate.AttemptCount + 1,
			})
		// RowsAffected==0 表示没改到(被别的 worker 抢先认领了):归还并发键,试下一条。
		if result.Error != nil || result.RowsAffected == 0 {
			a.releaseKey(candidate.ConcurrencyKey)
			continue
		}
		candidate.Status = "running"
		candidate.ClaimedAt = &now
		candidate.StartedAt = &now
		candidate.FinishedAt = nil
		candidate.LastHeartbeatAt = &now
		candidate.WorkerID = &a.WorkerID
		candidate.NextRetryAt = nil
		candidate.FailureCategory = ""
		candidate.ErrorMessage = ""
		candidate.AttemptCount++
		// UPDATE 已提交后直接交给执行 goroutine;取消会由其 detached cleanup 收尾。
		return candidate
	}
	return nil
}

// tryAcquireKey 进程内并发键信号量(limit 默认 1)。
func (a *App) tryAcquireKey(key string) bool {
	if key == "" {
		return true
	}
	limit := a.Cfg.Workflow.SemaphoreLimitPerKey
	if limit < 1 {
		limit = 1
	}
	// runningKeys 这个 map 会被多条 goroutine 同时读写,必须加锁保护,否则会发生数据竞争。
	// Lock() 上锁;defer Unlock() 保证函数返回前一定解锁(即使中途 return)。见 GO入门笔记『defer』『并发』。
	a.runningKeysMu.Lock()
	defer a.runningKeysMu.Unlock()
	// map[key] 取值:若 key 不存在,得到该值类型的"零值"(int 的零值是 0)。见 GO入门笔记『复合类型』。
	if a.runningKeys[key] >= limit {
		return false
	}
	// key++ 让该并发键的在跑计数 +1,表示占用一个名额。
	a.runningKeys[key]++
	return true
}

// releaseKey 归还并发键名额:计数减到 0 就从 map 里删掉这个 key。与 tryAcquireKey 成对使用。
func (a *App) releaseKey(key string) {
	if key == "" {
		return
	}
	a.runningKeysMu.Lock()
	defer a.runningKeysMu.Unlock()
	if a.runningKeys[key] <= 1 {
		// delete(m, key) 从 map 中删除一个键。见 GO入门笔记『复合类型』。
		delete(a.runningKeys, key)
	} else {
		a.runningKeys[key]--
	}
}

// processExecution 执行一条已认领的 execution:attempt 记录、心跳、跑图、终态回写。
func (a *App) processExecution(ctx context.Context, execution *db.WorkflowExecution) {
	// defer:整条执行处理结束前一定归还并发键,和 tryAcquireKey 配对。见 GO入门笔记『defer』。
	defer a.releaseKey(execution.ConcurrencyKey)
	attempt := execution.AttemptCount
	startedAt := time.Now()
	if execution.StartedAt != nil {
		startedAt = *execution.StartedAt
	}
	a.DB.Create(&db.WorkflowExecutionAttempt{
		WorkflowExecutionID: execution.ID, Attempt: attempt,
		WorkerID: a.WorkerID, StartedAt: startedAt, Status: "running",
	})
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// 心跳 goroutine:证明本执行仍在运行,供 stale 恢复判定。
	// heartbeatStop 是一个只用来发"停止"信号的 channel(struct{} 零字节)。见 GO入门笔记『并发』。
	heartbeatStop := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		interval := time.Duration(a.Cfg.Workflow.HeartbeatIntervalSeconds) * time.Second
		if interval < time.Second {
			interval = time.Second
		}
		// Ticker 是"周期定时器":每隔 interval 就往 ticker.C 这个 channel 送一个时间值。
		ticker := time.NewTicker(interval)
		// defer ticker.Stop() 收尾:goroutine 退出前停掉定时器,释放它的资源。见 GO入门笔记『defer』。
		defer ticker.Stop()
		for {
			select {
			// 收到停止信号(主流程 close 了 heartbeatStop)→ return 结束心跳 goroutine。
			case <-heartbeatStop:
				return
			case <-ctx.Done():
				return
			// 每当 ticker 到点 → 刷新一次 last_heartbeat_at 时间戳(带 worker_id/attempt 条件,确保只更新自己这一轮)。
			case <-ticker.C:
				requested, err := a.executionCancellationRequested(ctx, execution.ID)
				if err == nil && requested {
					cancelRun()
					return
				}
				a.DB.WithContext(ctx).Model(&db.WorkflowExecution{}).
					Where("id = ? AND status IN ? AND attempt_count = ? AND worker_id = ?", execution.ID, []string{"running", "cancel_requested"}, attempt, a.WorkerID).
					Update("last_heartbeat_at", time.Now())
			}
		}
	}()

	// 真正执行工作流图;返回 (结果, 错误)。这一行会阻塞到执行结束。
	result, runErr := a.runExecutionGraph(runCtx, execution.ID)
	close(heartbeatStop)
	<-heartbeatDone
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	cancelRequested, cancelCheckErr := a.executionCancellationRequested(cleanupCtx, execution.ID)
	if cancelCheckErr == nil && (cancelRequested || errors.Is(runErr, ErrWorkflowCanceled)) {
		a.finalizeCanceled(cleanupCtx, execution, attempt, result)
		return
	}
	if errors.Is(runErr, ErrWorkflowWaiting) {
		if err := a.finalizeWaiting(cleanupCtx, execution, attempt, result); errors.Is(err, ErrWorkflowCanceled) {
			a.finalizeCanceled(cleanupCtx, execution, attempt, result)
		} else if err != nil {
			a.finalizeFailure(cleanupCtx, execution, attempt, result, err)
		}
		return
	}
	if runErr != nil {
		a.finalizeFailure(cleanupCtx, execution, attempt, result, runErr)
		return
	}
	a.finalizeSuccess(cleanupCtx, execution, attempt, result)
}

func (a *App) finalizeCanceled(ctx context.Context, execution *db.WorkflowExecution, attempt int, result *runResult) {
	finishedAt := time.Now().UTC()
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	updates := map[string]any{
		"status": "canceled", "finished_at": finishedAt,
		"duration_ms": finishedAt.Sub(startedAt).Milliseconds(), "next_retry_at": nil,
		"failure_category": "", "error_message": "",
	}
	if result != nil {
		if !result.FinishedAt.IsZero() {
			finishedAt = result.FinishedAt
			updates["finished_at"] = finishedAt
			updates["duration_ms"] = finishedAt.Sub(result.StartedAt).Milliseconds()
		}
		updates["context_snapshot_json"] = serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes)
		updates["result_snapshot_json"] = serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes)
	}
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		terminal := tx.Model(&db.WorkflowExecution{}).
			Where("id = ? AND status IN ? AND attempt_count = ? AND worker_id = ?", execution.ID, []string{"running", "cancel_requested"}, attempt, a.WorkerID).
			Updates(updates)
		if terminal.Error != nil || terminal.RowsAffected == 0 {
			return terminal.Error
		}
		if err := tx.Model(&db.WorkflowExecutionWait{}).
			Where("workflow_execution_id = ? AND status = ?", execution.ID, "pending").
			Updates(map[string]any{"status": "canceled", "resolved_at": finishedAt, "updated_at": finishedAt}).Error; err != nil {
			return err
		}
		return a.closeAttemptWithDB(tx, execution.ID, attempt, a.WorkerID, "canceled", startedAt, finishedAt, "", "")
	})
	if err != nil {
		slog.ErrorContext(ctx, "execution terminal transaction failed", "execution_id", execution.ID, "status", "canceled", "error_category", "database")
	}
}

// finalizeSuccess 把执行写成 success 终态。
// 下面 UPDATE 的 WHERE 里带 status='running' AND attempt_count=? AND worker_id=?:
// 只有"仍是本 worker、本次尝试还在跑"时才写得进去,防止已被 stale 恢复抢走的执行又被旧 worker 覆盖(乐观保护)。
func (a *App) finalizeSuccess(ctx context.Context, execution *db.WorkflowExecution, attempt int, result *runResult) {
	finishedAt := result.FinishedAt
	durationMs := finishedAt.Sub(result.StartedAt).Milliseconds()
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		terminal := tx.Model(&db.WorkflowExecution{}).
			Where("id = ? AND status = ? AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
			Updates(map[string]any{
				"status": "success", "finished_at": finishedAt, "duration_ms": durationMs,
				"context_snapshot_json": serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes),
				"result_snapshot_json":  serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes),
				"error_message":         "",
			})
		if terminal.Error != nil || terminal.RowsAffected == 0 {
			return terminal.Error
		}
		if err := a.closeAttemptWithDB(tx, execution.ID, attempt, a.WorkerID, "success", result.StartedAt, finishedAt, "", ""); err != nil {
			return err
		}
		if err := a.publishExecutionSucceededWithDB(tx, result); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "execution terminal transaction failed", "execution_id", execution.ID, "status", "success", "error_category", "database")
		return
	}
}

// finalizeFailure 把执行写成失败终态:可重试且未超次数则转 retry_waiting 并排下次重试时间,否则判 failed。
// runErr 是跑图抛出的原始错误对象(不只是文本):可重试性优先由错误自身携带的标注决定,见 failure.go。
func (a *App) finalizeFailure(ctx context.Context, execution *db.WorkflowExecution, attempt int, result *runResult, runErr error) {
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	failureCategory, retriable := classifyFailure(runErr, errorMessage)
	finishedAt := time.Now()
	startedAt := finishedAt
	if result != nil {
		finishedAt = result.FinishedAt
		startedAt = result.StartedAt
	}
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	maxAttempts := execution.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = a.Cfg.Workflow.MaxAttempts
	}
	nextStatus := "failed"
	var nextRetryAt *time.Time
	if retriable && attempt < maxAttempts {
		nextStatus = "retry_waiting"
		retryAt := a.computeNextRetryAt(attempt)
		nextRetryAt = &retryAt
	}
	updates := map[string]any{
		"status": nextStatus, "finished_at": finishedAt, "duration_ms": durationMs,
		"error_message":    truncateRunes(errorMessage, 4000),
		"failure_category": truncateRunes(failureCategory, 64),
	}
	if nextRetryAt != nil {
		updates["next_retry_at"] = *nextRetryAt
	} else {
		updates["next_retry_at"] = nil
	}
	if result != nil {
		updates["context_snapshot_json"] = serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes)
		updates["result_snapshot_json"] = serializeSnapshot(orEmptyMap(result.SharedState["nodeOutputs"]), a.Cfg.Workflow.MaxOutputSnapshotBytes)
	}
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		terminal := tx.Model(&db.WorkflowExecution{}).
			Where("id = ? AND status = ? AND attempt_count = ? AND worker_id = ?", execution.ID, "running", attempt, a.WorkerID).
			Updates(updates)
		if terminal.Error != nil || terminal.RowsAffected == 0 {
			return terminal.Error
		}
		if err := a.closeAttemptWithDB(tx, execution.ID, attempt, a.WorkerID, nextStatus, startedAt, finishedAt, failureCategory, errorMessage); err != nil {
			return err
		}
		if nextStatus == "failed" {
			terminalResult := result
			if terminalResult == nil {
				var err error
				terminalResult, err = a.loadTerminalRunResult(tx, execution, finishedAt)
				if err != nil {
					return err
				}
			}
			if err := a.publishExecutionFailedWithDB(tx, terminalResult, errorMessage); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "execution terminal transaction failed", "execution_id", execution.ID, "status", nextStatus, "error_category", failureCategory)
		return
	}
}

func (a *App) closeAttemptWithDB(database *gorm.DB, executionID int64, attempt int, workerID, status string, startedAt, finishedAt time.Time, failureCategory, errorSummary string) error {
	failureCategory = truncateRunes(failureCategory, 64)
	errorSummary = truncateRunes(errorSummary, 4000)
	updated := database.Model(&db.WorkflowExecutionAttempt{}).
		Where("workflow_execution_id = ? AND attempt = ?", executionID, attempt).
		Updates(map[string]any{
			"status": status, "finished_at": finishedAt,
			"failure_category": failureCategory,
			"error_summary":    errorSummary,
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected == 1 {
		return nil
	}
	if updated.RowsAffected > 1 {
		return fmt.Errorf("close workflow execution attempt: updated %d rows", updated.RowsAffected)
	}
	// 认领 UPDATE 提交后进程可能在创建 attempt 前取消或崩溃；终态事务补建缺失账本，
	// 使取消善后和 stale 恢复仍能把 execution、attempt 与标准事件原子收敛。
	return database.Create(&db.WorkflowExecutionAttempt{
		WorkflowExecutionID: executionID,
		Attempt:             attempt,
		WorkerID:            workerID,
		StartedAt:           startedAt,
		FinishedAt:          &finishedAt,
		FailureCategory:     failureCategory,
		ErrorSummary:        errorSummary,
		Status:              status,
	}).Error
}

// computeNextRetryAt 按"退避序列"算下次重试时刻:第 1/2/3 次失败分别等 30/120/600 秒(可配置)。
// len(backoffs) 取切片长度;backoffs[index] 按下标取元素;末尾把秒数 × time.Second 变成时长再 Add 到当前时间。见 GO入门笔记『复合类型』。
func (a *App) computeNextRetryAt(attempt int) time.Time {
	return time.Now().Add(a.retryBackoff(attempt))
}

// retryBackoff 返回当前尝试对应的退避时长，工作流重试和 Outbox 重排共用同一配置。
func (a *App) retryBackoff(attempt int) time.Duration {
	backoffs := a.Cfg.Workflow.RetryBackoffSeconds
	if len(backoffs) == 0 {
		backoffs = []int{30, 120, 600}
	}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(backoffs) {
		index = len(backoffs) - 1
	}
	seconds := backoffs[index]
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds) * time.Second
}

// classifyFailure 已迁到 failure.go:改为"错误类型驱动 + 文本兜底",不再纯靠子串匹配。

// ---------- 事件 outbox 循环 ----------

// 后台循环之三【事件】:定时把 outbox 表里待发的领域事件冲刷出去(可靠事件投递)。
// 同样用 for a.sleeping(interval) 骨架:睡够一轮就处理一批,收到停止信号就退出。见 GO入门笔记『并发』。
func (a *App) eventOutboxLoop(ctx context.Context) {
	interval := time.Duration(a.Cfg.Workflow.OutboxPollIntervalMs) * time.Millisecond
	for a.sleeping(ctx, interval) {
		a.drainPendingEvents(ctx, a.Cfg.Workflow.OutboxBatchSize)
	}
}

// ---------- Stale 恢复循环 ----------

// 后台循环之四【恢复】:把"心跳早已超时"的 running 执行判成 worker 丢失并善后。
// staleRecoveryLoop 心跳超时的 running 执行判定为 worker_lost。
// 也覆盖进程崩溃重启后的孤儿执行恢复。
func (a *App) staleRecoveryLoop(ctx context.Context) {
	interval := time.Duration(a.Cfg.Workflow.StaleRecoveryIntervalSeconds) * time.Second
	for a.sleeping(ctx, interval) {
		staleBefore := time.Now().Add(-time.Duration(a.Cfg.Workflow.ExecutionStaleTimeoutSeconds) * time.Second)
		var staleRows []db.WorkflowExecution
		// Preload("WorkflowDefinition"):GORM 顺带把关联的定义一起查出来(类似预加载 JOIN),省得后面再查一次。见 GO入门笔记『框架:GORM』。
		a.DB.WithContext(ctx).Preload("WorkflowDefinition").
			Where("status IN ? AND last_heartbeat_at IS NOT NULL AND last_heartbeat_at < ?", []string{"running", "cancel_requested"}, staleBefore).
			Order("last_heartbeat_at ASC, id ASC").Limit(a.Cfg.Workflow.OutboxBatchSize).
			Find(&staleRows)
		for i := range staleRows {
			if ctx.Err() != nil {
				return
			}
			a.recoverStaleExecution(ctx, &staleRows[i])
		}
	}
}

// recoverStaleExecution 处理单条超时执行:未超重试次数则转 retry_waiting 等待重试,否则判 failed;并归还并发键。
func (a *App) recoverStaleExecution(ctx context.Context, execution *db.WorkflowExecution) {
	if execution.WorkerID == nil || execution.LastHeartbeatAt == nil {
		return
	}
	finishedAt := time.Now()
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	durationMs := finishedAt.Sub(startedAt).Milliseconds()
	attempt := execution.AttemptCount
	maxAttempts := execution.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = a.Cfg.Workflow.MaxAttempts
	}
	nextStatus := "failed"
	var nextRetryAt *time.Time
	if execution.Status == "cancel_requested" || execution.CancelRequestedAt != nil {
		nextStatus = "canceled"
	} else if attempt < maxAttempts {
		nextStatus = "retry_waiting"
		retryAt := a.computeNextRetryAt(attempt)
		nextRetryAt = &retryAt
	}
	updates := map[string]any{
		"status": nextStatus, "finished_at": finishedAt, "duration_ms": durationMs,
		"error_message": "Worker heartbeat timed out", "failure_category": "worker_lost",
	}
	if nextRetryAt != nil {
		updates["next_retry_at"] = *nextRetryAt
	} else {
		updates["next_retry_at"] = nil
	}
	attemptWorkerID := ""
	if execution.WorkerID != nil {
		attemptWorkerID = *execution.WorkerID
	}
	var updated bool
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 扫描和写入之间健康 worker 可能已经续过心跳；worker、attempt 与扫描时 heartbeat
		// 共同充当 fencing 条件，旧快照不得回收仍健康或已被重新认领的执行。
		terminal := tx.Model(&db.WorkflowExecution{}).
			Where(
				"id = ? AND status = ? AND attempt_count = ? AND worker_id = ? AND last_heartbeat_at = ?",
				execution.ID,
				execution.Status,
				attempt,
				*execution.WorkerID,
				*execution.LastHeartbeatAt,
			).
			Updates(updates)
		if terminal.Error != nil || terminal.RowsAffected == 0 {
			return terminal.Error
		}
		if err := a.closeAttemptWithDB(tx, execution.ID, attempt, attemptWorkerID, nextStatus, startedAt, finishedAt, "worker_lost", "Worker heartbeat timed out"); err != nil {
			return err
		}
		if nextStatus == "failed" {
			result, err := a.loadTerminalRunResult(tx, execution, finishedAt)
			if err != nil {
				return err
			}
			if err := a.publishExecutionFailedWithDB(tx, result, "Worker heartbeat timed out"); err != nil {
				return err
			}
		}
		updated = true
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "stale execution transaction failed", "execution_id", execution.ID, "next_status", nextStatus, "error_category", "database")
		return
	}
	if !updated {
		return
	}
	a.releaseKey(execution.ConcurrencyKey)
	slog.InfoContext(ctx, "stale execution recovered", "execution_id", execution.ID, "next_status", nextStatus)
}

// ---------- 清理循环 ----------

// 后台循环之五【清理】:每天凌晨 3 点后,批量删除超过保留期的历史执行,控制表体积。
// cleanupLoop 每天 03:00 之后清理超保留期的终态执行。
func (a *App) cleanupLoop(ctx context.Context) {
	// 记住"上次清理是哪一天",保证一天只清一次。
	lastCleanupDate := ""
	// 每分钟醒来看一眼(很轻量),真正干活受下面两个条件闸门控制。
	for a.sleeping(ctx, time.Minute) {
		now := time.Now()
		if now.Hour() < 3 {
			continue
		}
		// Go 用这个固定的参考时间 "2006-01-02" 当日期格式模板(不是随便写的数字,而是约定俗成的写法)。
		cleanupDate := now.Format("2006-01-02")
		if lastCleanupDate == cleanupDate {
			continue
		}
		deletedTotal := 0
		// 内层无限 for:一批一批地删,直到某批删的数量不足一整批,说明删完了才 break 跳出。
		for {
			if ctx.Err() != nil {
				return
			}
			deleted := a.cleanupTerminalHistory()
			deletedTotal += deleted
			if deleted < a.Cfg.Workflow.RetentionDeleteBatchSize {
				break
			}
		}
		lastCleanupDate = cleanupDate
		if deletedTotal > 0 {
			slog.InfoContext(ctx, "execution history cleanup completed", "deleted", deletedTotal)
		}
	}
}
