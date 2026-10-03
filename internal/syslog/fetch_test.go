package syslog

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const fetchOutput = "@@MERLINMCP:path@@\n/jffs/syslog.log\n" +
	"@@MERLINMCP:date@@\n1791000000 +0800\n" +
	"@@MERLINMCP:rotated@@\nOct  3 09:00:00 kernel: from rotated\n" +
	"@@MERLINMCP:current@@\nOct  3 10:00:00 dnsmasq[1]: from current\n"

func TestFetchAutoDetectAndRotated(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	snap, err := Fetch(context.Background(), f, "auto", true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Path != "/jffs/syslog.log" || snap.Now.Unix() != 1791000000 {
		t.Fatalf("snap = %+v", snap)
	}
	if len(snap.Lines) != 2 || !strings.Contains(snap.Lines[0].Raw, "rotated") || !strings.Contains(snap.Lines[1].Raw, "current") {
		t.Fatalf("lines = %+v", snap.Lines)
	}
	cmd := f.CallsFor("syslog_fetch")[0].Cmd
	if !strings.Contains(cmd, "/jffs/syslog.log /tmp/syslog.log") || !strings.Contains(cmd, `"$p-1"`) {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestFetchWithoutRotatedDoesNotReadIt(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	_, _ = Fetch(context.Background(), f, "auto", false)
	if strings.Contains(f.CallsFor("syslog_fetch")[0].Cmd, `"$p-1"`) {
		t.Fatal("include_rotated=false 时不应读取轮转文件")
	}
}

func TestFetchExplicitPathIsQuoted(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Stdout(fetchOutput))
	_, _ = Fetch(context.Background(), f, "/opt/var/log/messages", false)
	if !strings.Contains(f.CallsFor("syslog_fetch")[0].Cmd, "p='/opt/var/log/messages'") {
		t.Fatalf("cmd = %s", f.CallsFor("syslog_fetch")[0].Cmd)
	}
}

func TestFetchMissingFile(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", runnertest.Exit(3, "", "找不到 syslog 文件"))
	_, err := Fetch(context.Background(), f, "auto", false)
	e := apperr.From(err)
	if e.Code != apperr.CommandFailed || !strings.Contains(e.Hint, "paths.syslog") {
		t.Fatalf("err = %+v", e)
	}
}

func TestFetchKernel(t *testing.T) {
	f := runnertest.New().On("dmesg", runnertest.Stdout("[1.0] a\n\n[2.0] b\n"))
	lines, err := FetchKernel(context.Background(), f)
	if err != nil || len(lines) != 2 || lines[1] != "[2.0] b" {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}
