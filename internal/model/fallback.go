package model

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/morehao/golib/gllm"
)

// FallbackProvider is a decorator over a chain of Providers: ["主", "备1", "备2"…].
// Complete tries each member in order and returns the first success. If every
// member fails, it returns the last error. This keeps the summarize layer's
// retry semantics (retry the same provider, only resend missing items) untouched
// — "换模型" is pushed down to the Provider level, one Complete call at a time.
//
// 设计依据见 .dsh/docs/specs/2026-11-14-model-fallback-design.md §3.2 / §6。
type FallbackProvider struct {
	members []Provider
	usedIdx atomic.Int32 // 最近一次成功的成员下标；-1 表示尚未成功过
}

// NewFallback wraps members into a FallbackProvider. members[0] is the primary,
// the rest are backups tried in order. A nil/empty chain yields a provider that
// always fails (there is nothing to call); callers should guard against that.
//
// 返回具体类型 *FallbackProvider（同样满足 Provider 接口），以便上层调用 Used()
// 展示本次实际使用的模型；A3 打包时按需以 model.Provider 接口存放即可。
func NewFallback(members ...Provider) *FallbackProvider {
	p := &FallbackProvider{members: members}
	p.usedIdx.Store(-1)
	return p
}

// Name reports the primary member's name, prefixed to make the chain visible.
func (p *FallbackProvider) Name() string {
	if len(p.members) == 0 {
		return "fallback:empty"
	}
	return "fallback:" + p.members[0].Name()
}

// Complete calls each member in order. On success it records the used index and
// returns the result. On a fallback-worthy error it moves to the next member; on
// a non-fallback error (e.g. RateLimited) it returns that error immediately.
// When all members fail, it returns the last error.
func (p *FallbackProvider) Complete(ctx context.Context, system, user string) (Result, error) {
	var lastErr error
	for i, m := range p.members {
		res, err := m.Complete(ctx, system, user)
		if err == nil {
			p.usedIdx.Store(int32(i))
			return res, nil
		}
		if !shouldFallback(err) {
			// 不切换：RATE_LIMITED（默认）、BAD_OUTPUT（provider 层看不到）等直接返回。
			return res, err
		}
		lastErr = err
	}
	if lastErr == nil {
		// 空链：没有可调用的成员。
		lastErr = gllm.ErrConfigInvalid.Wrap(errors.New("fallback chain is empty"))
	}
	return Result{}, lastErr
}

// Used returns the name of the member that produced the most recent successful
// Complete, or "" if none has succeeded yet. 一期内部用（展示实际使用的模型）。
func (p *FallbackProvider) Used() string {
	i := int(p.usedIdx.Load())
	if i < 0 || i >= len(p.members) {
		return ""
	}
	return p.members[i].Name()
}

// UsedIndex returns the index of the last successful member, or -1.
func (p *FallbackProvider) UsedIndex() int { return int(p.usedIdx.Load()) }

// shouldFallback reports whether an error from one member warrants trying the
// next member, based on gllm error codes (golib/gllm/errors.go).
//
// 切换集合：UNAUTHORIZED(ErrAuth) / NOT_FOUND(见下方说明) /
// KEY_MISSING(ErrAuth，key 为空时 gllm 归为鉴权失败) /
// FAILED(ErrUpstream: 网络/5xx/无法归类) / TIMEOUT(ErrTimeout)。
//
// 不切换：RATE_LIMITED(ErrRateLimit，限流临时，换模型可能同样被限且增加噪声，默认不切)；
// BAD_OUTPUT 由上层 parseAndValidate 判定，provider 层天然看不到，无需在此处理。
//
// 关于 NOT_FOUND：gllm 的 Classify 把 400/404/422 统一归为 ErrBadRequest，
// provider 层无法进一步区分“模型不存在(404)”与“请求非法(400/422)”。此时换一个
// 模型重试是合理的（等同于设计稿 §6 的 NOT_FOUND/FAILED 可切换一侧），故一并切换。
func shouldFallback(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, gllm.ErrRateLimit):
		return false // 默认不切（Q1 开放问题）
	case errors.Is(err, gllm.ErrAuth): // 401/403/key 缺失
		return true
	case errors.Is(err, gllm.ErrTimeout):
		return true
	case errors.Is(err, gllm.ErrUpstream): // 网络 / 5xx / 其他无法归类
		return true
	case errors.Is(err, gllm.ErrBadRequest): // 400/404/422，含 NOT_FOUND
		return true
	default:
		// 非 gllm 错误（例如上游 SDK 原生错误、context deadline 未被 Classify 时）：
		// 保守不切换，交由上层处理。
		return false
	}
}
