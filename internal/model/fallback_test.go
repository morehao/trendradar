package model

import (
	"context"
	"errors"
	"testing"

	"github.com/morehao/golib/gllm"
)

// stubProvider is an offline fake: it returns a preset Result/error and counts
// how many times Complete was called.
type stubProvider struct {
	name  string
	res   Result
	err   error
	calls int
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(_ context.Context, _, _ string) (Result, error) {
	s.calls++
	return s.res, s.err
}

// TestFallback_PrimarySucceeds_BackupNotCalled: 主成功即返回，不调备选。
func TestFallback_PrimarySucceeds_BackupNotCalled(t *testing.T) {
	primary := &stubProvider{name: "primary", res: Result{Text: "ok"}}
	backup := &stubProvider{name: "backup", res: Result{Text: "backup"}}

	p := NewFallback(primary, backup)
	res, err := p.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "ok" {
		t.Fatalf("want primary result, got %q", res.Text)
	}
	if backup.calls != 0 {
		t.Fatalf("backup should not be called, got %d calls", backup.calls)
	}
	if got := p.Used(); got != "primary" {
		t.Fatalf("Used() = %q, want primary", got)
	}
}

// TestFallback_PrimaryAuthFails_BackupSucceeds: 主鉴权失败 → 切备成功。
func TestFallback_PrimaryAuthFails_BackupSucceeds(t *testing.T) {
	primary := &stubProvider{name: "primary", err: gllm.ErrAuth.Wrap(errors.New("401"))}
	backup := &stubProvider{name: "backup", res: Result{Text: "backup-ok"}}

	p := NewFallback(primary, backup)
	res, err := p.Complete(context.Background(), "s", "u")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "backup-ok" {
		t.Fatalf("want backup result, got %q", res.Text)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls primary=%d backup=%d, want 1/1", primary.calls, backup.calls)
	}
	if got := p.Used(); got != "backup" {
		t.Fatalf("Used() = %q, want backup", got)
	}
	if got := p.UsedIndex(); got != 1 {
		t.Fatalf("UsedIndex() = %d, want 1", got)
	}
}

// TestFallback_RateLimited_DoesNotFallBack: 限流默认不切。
func TestFallback_RateLimited_DoesNotFallBack(t *testing.T) {
	primary := &stubProvider{name: "primary", err: gllm.ErrRateLimit.Wrap(errors.New("429"))}
	backup := &stubProvider{name: "backup", res: Result{Text: "should-not-reach"}}

	p := NewFallback(primary, backup)
	res, err := p.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, gllm.ErrRateLimit) {
		t.Fatalf("want rate-limit error, got %v", err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup should not be called on rate limit, got %d", backup.calls)
	}
	if res.Text != "" {
		t.Fatalf("want empty result on failure, got %q", res.Text)
	}
	if got := p.Used(); got != "" {
		t.Fatalf("Used() = %q, want empty (nothing succeeded)", got)
	}
}

// TestFallback_AllFail_ReturnsLastError: 全败返回最后错误。
func TestFallback_AllFail_ReturnsLastError(t *testing.T) {
	first := &stubProvider{name: "first", err: gllm.ErrAuth.Wrap(errors.New("401"))}
	second := &stubProvider{name: "second", err: gllm.ErrTimeout.Wrap(errors.New("timeout"))}
	third := &stubProvider{name: "third", err: gllm.ErrUpstream.Wrap(errors.New("502"))}

	p := NewFallback(first, second, third)
	_, err := p.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, gllm.ErrUpstream) {
		t.Fatalf("want last error (upstream 502), got %v", err)
	}
	if first.calls != 1 || second.calls != 1 || third.calls != 1 {
		t.Fatalf("calls = %d/%d/%d, want 1/1/1", first.calls, second.calls, third.calls)
	}
	if got := p.Used(); got != "" {
		t.Fatalf("Used() = %q, want empty", got)
	}
}

// TestFallback_UsedTracksMostRecentSuccess: 两次调用后 Used() 跟随最近一次成功。
func TestFallback_UsedTracksMostRecentSuccess(t *testing.T) {
	// 用一个可变 provider 模拟“先失败后成功”。
	flaky := &flakyProvider{name: "primary", err: gllm.ErrAuth.Wrap(errors.New("401"))}
	backup := &stubProvider{name: "backup", res: Result{Text: "backup-ok"}}
	p := NewFallback(flaky, backup)

	// 第一次：主失败(鉴权) → 备成功。
	if _, err := p.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if got := p.Used(); got != "backup" {
		t.Fatalf("after call1 Used() = %q, want backup", got)
	}

	// 第二次：主成功 → 后备不再被调用，Used 变为 primary。
	flaky.err = nil
	if _, err := p.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if got := p.Used(); got != "primary" {
		t.Fatalf("after call2 Used() = %q, want primary", got)
	}
}

// flakyProvider 返回固定的 error/res，可在测试中修改。
type flakyProvider struct {
	name string
	err  error
}

func (f *flakyProvider) Name() string { return f.name }
func (f *flakyProvider) Complete(_ context.Context, _, _ string) (Result, error) {
	return Result{Text: "primary-ok"}, f.err
}

// TestShouldFallback pins the error→switch mapping (§6).
func TestShouldFallback(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"auth", gllm.ErrAuth.Wrap(errors.New("401")), true},
		{"timeout", gllm.ErrTimeout.Wrap(errors.New("deadline")), true},
		{"upstream-5xx", gllm.ErrUpstream.Wrap(errors.New("502")), true},
		{"bad-request-404", gllm.ErrBadRequest.Wrap(errors.New("404")), true},
		{"rate-limit", gllm.ErrRateLimit.Wrap(errors.New("429")), false},
		{"plain-error", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldFallback(c.err); got != c.want {
				t.Fatalf("shouldFallback(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestFallback_EmptyChain ensures an empty chain returns an error instead of panicking.
func TestFallback_EmptyChain(t *testing.T) {
	p := NewFallback()
	_, err := p.Complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected error for empty chain")
	}
	if got := p.Used(); got != "" {
		t.Fatalf("Used() = %q, want empty", got)
	}
}
