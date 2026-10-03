package syslog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// 每个日志文件最多读取末尾 4MB。
const maxReadBytes = 4 << 20

// DetectScript 返回一段 shell：执行后变量 p 为 syslog 文件路径；找不到时以退出码 3 结束。
// pathSetting 为 "auto" 时依次尝试 /jffs/syslog.log 和 /tmp/syslog.log。
func DetectScript(pathSetting string) string {
	if pathSetting != "auto" {
		return "p=" + shell.Quote(pathSetting) + `; [ -f "$p" ] || { echo "syslog 文件不存在: $p" >&2; exit 3; }`
	}
	return `p=''; for f in /jffs/syslog.log /tmp/syslog.log; do if [ -f "$f" ]; then p="$f"; break; fi; done; ` +
		`[ -n "$p" ] || { echo "找不到 syslog 文件（已尝试 /jffs/syslog.log 和 /tmp/syslog.log）" >&2; exit 3; }`
}

// Snapshot 是一次读取的结果。
type Snapshot struct {
	Path  string
	Now   time.Time
	Lines []Line
}

// Fetch 一次 SSH 调用取回路由器时间和日志内容，在本地解析。
func Fetch(ctx context.Context, r runner.Runner, pathSetting string, includeRotated bool) (Snapshot, error) {
	parts := []string{
		DetectScript(pathSetting),
		routercmd.Marker("path"), `echo "$p"`,
		routercmd.Marker("date"), routercmd.DateCmd,
	}
	if includeRotated {
		parts = append(parts, routercmd.Marker("rotated"), fmt.Sprintf(`tail -c %d "$p-1" 2>/dev/null`, maxReadBytes))
	}
	parts = append(parts, routercmd.Marker("current"), fmt.Sprintf(`tail -c %d "$p"`, maxReadBytes))

	res, err := r.Run(ctx, runner.Op("syslog_fetch", strings.Join(parts, "; ")), nil)
	if err != nil {
		return Snapshot{}, err
	}
	if res.ExitCode == 3 {
		return Snapshot{}, apperr.New(apperr.CommandFailed, strings.TrimSpace(string(res.Stderr)), "在配置文件的 paths.syslog 中指定 syslog 的实际路径")
	}
	if res.ExitCode != 0 {
		return Snapshot{}, runner.Failed(res)
	}
	sec := routercmd.Sections(string(res.Stdout))
	now, err := routercmd.ParseDate(sec["date"])
	if err != nil {
		return Snapshot{}, apperr.New(apperr.Internal, err.Error(), "")
	}
	var b strings.Builder
	for _, name := range []string{"rotated", "current"} {
		s := dropPartial(sec[name])
		b.WriteString(s)
		if s != "" && !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	return Snapshot{Path: strings.TrimSpace(sec["path"]), Now: now, Lines: Parse(b.String(), now)}, nil
}

// dropPartial 在内容达到读取上限时丢弃第一行（tail -c 可能从一行的中间开始）。
func dropPartial(s string) string {
	if len(s) < maxReadBytes {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// FetchKernel 读取 dmesg，返回非空行。
func FetchKernel(ctx context.Context, r runner.Runner) ([]string, error) {
	out, err := runner.Output(ctx, r, runner.Op("dmesg", "dmesg"))
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
