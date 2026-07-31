// failure.go —— 执行失败的"可否重试"分类。
//
// 原来判断一个失败该不该重试,靠在错误文本里搜 "timeout" / "429" / "connection" 这些词。
// 问题很实在:业务数据里恰好出现 "429" 就会被误判成基础设施故障而白白重试三轮;
// 反过来,节点明知道"这次失败重试也没用"(比如 HTTP 400)却没有办法说出来。
//
// 这里改成"错误类型驱动":节点用 retryableErr / permanentErr 包一层,把结论写进错误本身;
// 调度侧(loops.go 的 finalizeFailure)先用 errors.As 问错误,问不出来才退回原来的文本启发式。
// errors.As 会顺着 %w 包裹的错误链一路找,所以中间层用 fmt.Errorf("...: %w", err) 再包也不影响判定。

package service

import (
	"context"
	"errors"
	"strings"
)

// 失败分类编码,落进 workflow_executions.failure_category 与 attempt 记录。
const (
	failureInfraRetryable = "infra_retryable" // 基础设施类,过一会儿重试可能就好了
	failureBusiness       = "business_failed" // 业务/请求本身的问题,重试也是同样的结果
)

// classifiedError 带"是否可重试"结论的错误包装。
// 它实现了 Unwrap,所以 errors.Is / errors.As 仍能穿透到被包裹的原始错误。
type classifiedError struct {
	err       error
	category  string
	retryable bool
}

func (e *classifiedError) Error() string { return e.err.Error() }
func (e *classifiedError) Unwrap() error { return e.err }

// retryableErr 标记"这次失败值得重试"(网络抖动、被限流、对端 5xx 等)。
func retryableErr(err error) error {
	if err == nil {
		return nil
	}
	return &classifiedError{err: err, category: failureInfraRetryable, retryable: true}
}

// permanentErr 标记"重试也是白搭"(请求参数错、鉴权失败、业务规则不通过等)。
func permanentErr(err error) error {
	if err == nil {
		return nil
	}
	return &classifiedError{err: err, category: failureBusiness, retryable: false}
}

// classifyFailure 判定一次失败的分类与可重试性。
//
// 优先级:① 节点显式标注的分类 → ② context 取消/超时(整图被中断,属于基础设施) →
// ③ 退回文本启发式(兼容没有标注的老路径)。
// err 允许为 nil(比如只剩一条错误文本的恢复路径),此时直接走 ③。
func classifyFailure(err error, errorMessage string) (string, bool) {
	var classified *classifiedError
	if err != nil && errors.As(err, &classified) {
		return classified.category, classified.retryable
	}
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		return failureInfraRetryable, true
	}
	// 兜底启发式:没有被标注过的错误只能看文本。这条路会随着各节点补齐标注而逐渐用不上。
	text := strings.ToLower(errorMessage)
	for _, marker := range []string{"timeout", "connection refused", "connection reset", "no such host", "eof"} {
		if strings.Contains(text, marker) {
			return failureInfraRetryable, true
		}
	}
	return failureBusiness, false
}
