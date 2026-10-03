package runner_test

import (
	"context"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestOpRoundTrip(t *testing.T) {
	cmd := runner.Op("syslog_fetch", "cat /tmp/syslog.log")
	if cmd != ": mcp-op=syslog_fetch; cat /tmp/syslog.log" {
		t.Fatalf("Op = %q", cmd)
	}
	if got := runner.OpOf(cmd); got != "syslog_fetch" {
		t.Fatalf("OpOf = %q", got)
	}
	if got := runner.OpOf("cat /etc/hosts"); got != "" {
		t.Fatalf("无标记的命令应返回空字符串，得到 %q", got)
	}
}

func TestOutputReturnsStdout(t *testing.T) {
	f := runnertest.New().On("x", runnertest.Stdout("hello\n"))
	out, err := runner.Output(context.Background(), f, runner.Op("x", "echo hello"))
	if err != nil || out != "hello\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestOutputReturnsCommandFailedOnNonZeroExit(t *testing.T) {
	f := runnertest.New().On("x", runnertest.Exit(2, "", "boom"))
	_, err := runner.Output(context.Background(), f, runner.Op("x", "false"))
	if apperr.CodeOf(err) != apperr.CommandFailed {
		t.Fatalf("code = %q", apperr.CodeOf(err))
	}
	if apperr.From(err).Details["stderr"] != "boom" {
		t.Fatalf("details = %v", apperr.From(err).Details)
	}
}

func TestWithRetryRetriesOnceOnUnreachable(t *testing.T) {
	n := 0
	f := runnertest.New().On("x", func(string, []byte) (runner.Result, error) {
		n++
		if n == 1 {
			return runner.Result{}, apperr.New(apperr.SSHUnreachable, "连接断开", "")
		}
		return runner.Result{Stdout: []byte("ok")}, nil
	})
	out, err := runner.Output(context.Background(), runner.WithRetry(f), runner.Op("x", "true"))
	if err != nil || out != "ok" || n != 2 {
		t.Fatalf("out=%q err=%v n=%d", out, err, n)
	}
}

func TestWithRetryDoesNotRetryOtherErrors(t *testing.T) {
	n := 0
	f := runnertest.New().On("x", func(string, []byte) (runner.Result, error) {
		n++
		return runner.Result{}, apperr.New(apperr.Timeout, "超时", "")
	})
	_, err := runner.WithRetry(f).Run(context.Background(), runner.Op("x", "true"), nil)
	if apperr.CodeOf(err) != apperr.Timeout || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestFakeRejectsUnknownOp(t *testing.T) {
	_, err := runnertest.New().Run(context.Background(), "ls", nil)
	if err == nil {
		t.Fatal("未注册的操作应返回错误")
	}
}

func TestFakeRecordsCallsAndStdin(t *testing.T) {
	f := runnertest.New().On("w", runnertest.Stdout(""))
	_, _ = f.Run(context.Background(), runner.Op("w", "cat > /tmp/x"), []byte("data"))
	calls := f.CallsFor("w")
	if len(calls) != 1 || string(calls[0].Stdin) != "data" {
		t.Fatalf("calls = %+v", calls)
	}
}
