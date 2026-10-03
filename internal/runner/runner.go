// Package runner 定义在路由器上执行命令的抽象。
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

// Result 是一次远程命令的执行结果。
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner 在路由器上执行一条 shell 命令。stdin 为 nil 时不提供输入。
type Runner interface {
	Run(ctx context.Context, cmd string, stdin []byte) (Result, error)
}

const opPrefix = ": mcp-op="

// Op 给命令加上操作名标记。`:` 是 shell 的空操作，不影响执行；
// 测试用的假 Runner 按操作名分发调用。
func Op(name, script string) string { return opPrefix + name + "; " + script }

// OpOf 取出命令的操作名，没有标记时返回空字符串。
func OpOf(cmd string) string {
	if !strings.HasPrefix(cmd, opPrefix) {
		return ""
	}
	rest := cmd[len(opPrefix):]
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// Output 执行命令并返回 stdout；退出码非 0 时返回 COMMAND_FAILED。
func Output(ctx context.Context, r Runner, cmd string) (string, error) {
	res, err := r.Run(ctx, cmd, nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", Failed(res)
	}
	return string(res.Stdout), nil
}

// Failed 把非 0 退出码转换成 COMMAND_FAILED 错误，附带 stderr 末尾 2KB。
func Failed(res Result) *apperr.Error {
	stderr := string(res.Stderr)
	if len(stderr) > 2048 {
		stderr = stderr[len(stderr)-2048:]
	}
	return apperr.New(apperr.CommandFailed,
		fmt.Sprintf("路由器命令执行失败，退出码 %d", res.ExitCode),
		"查看 details.stderr 了解原因").
		With("stderr", strings.ToValidUTF8(strings.TrimSpace(stderr), ""))
}

// WithRetry 返回在 SSH_UNREACHABLE 时自动重试一次的 Runner。
// 只能给只读操作使用：修改类操作重试可能导致重复执行。
func WithRetry(r Runner) Runner { return retryRunner{inner: r} }

type retryRunner struct{ inner Runner }

func (x retryRunner) Run(ctx context.Context, cmd string, stdin []byte) (Result, error) {
	res, err := x.inner.Run(ctx, cmd, stdin)
	if apperr.CodeOf(err) == apperr.SSHUnreachable && ctx.Err() == nil {
		return x.inner.Run(ctx, cmd, stdin)
	}
	return res, err
}
