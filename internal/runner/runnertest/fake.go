// Package runnertest 提供按操作名分发的假 Runner，供各模块测试使用。
package runnertest

import (
	"context"
	"fmt"
	"sync"

	"github.com/rshun/merlin-mcp/internal/runner"
)

// Call 记录一次调用。
type Call struct {
	Cmd   string
	Stdin []byte
}

// Handler 处理一次调用。
type Handler func(cmd string, stdin []byte) (runner.Result, error)

// Fake 按 runner.OpOf(cmd) 把调用分发给注册的 Handler；未注册的操作返回错误。
type Fake struct {
	mu       sync.Mutex
	calls    []Call
	handlers map[string]Handler
}

// New 创建一个没有任何处理函数的 Fake。
func New() *Fake { return &Fake{handlers: map[string]Handler{}} }

// On 为操作名注册处理函数，返回自身以便链式调用。
func (f *Fake) On(op string, h Handler) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[op] = h
	return f
}

// Run 实现 runner.Runner。
func (f *Fake) Run(_ context.Context, cmd string, stdin []byte) (runner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, Call{Cmd: cmd, Stdin: append([]byte(nil), stdin...)})
	h := f.handlers[runner.OpOf(cmd)]
	f.mu.Unlock()
	if h == nil {
		return runner.Result{}, fmt.Errorf("runnertest: 未注册的操作 %q，命令: %s", runner.OpOf(cmd), cmd)
	}
	return h(cmd, stdin)
}

// Calls 返回所有调用记录的副本。
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsFor 返回指定操作名的调用记录。
func (f *Fake) CallsFor(op string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if runner.OpOf(c.Cmd) == op {
			out = append(out, c)
		}
	}
	return out
}

// Stdout 返回一个输出 s、退出码为 0 的处理函数。
func Stdout(s string) Handler { return Exit(0, s, "") }

// Exit 返回一个固定输出和退出码的处理函数。
func Exit(code int, stdout, stderr string) Handler {
	return func(string, []byte) (runner.Result, error) {
		return runner.Result{Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: code}, nil
	}
}

// Fail 返回一个总是返回 err 的处理函数（模拟连接错误等）。
func Fail(err error) Handler {
	return func(string, []byte) (runner.Result, error) { return runner.Result{}, err }
}
