// Package tools 把各业务模块注册为 MCP 工具，统一处理参数校验、错误格式和审计日志。
package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Deps 是工具层依赖的全部组件。Runner 必须是原始 Runner（不带重试），只读工具内部自行包装。
type Deps struct {
	Cfg      *config.Config
	Runner   runner.Runner
	Quota    *reboot.Quota
	Rebooter *reboot.Rebooter
	Dnsmasq  *dnsmasq.Manager
	Audit    *audit.Logger
}

// Register 注册全部工具。allow_mutations 为 false 时修改类工具不注册。
func Register(s *mcp.Server, d Deps) {
	registerReadOnly(s, d)
	if d.Cfg.AllowMutations {
		registerMutations(s, d)
	}
}

func boolPtr(b bool) *bool { return &b }

type toolFunc[In any] func(ctx context.Context, in In) (any, error)

// dryRunner 由支持 dry_run 的参数结构实现；dry_run 调用不写审计日志。
type dryRunner interface{ isDryRun() bool }

// add 注册一个工具。audited 为 true 时，调用结束后写审计日志（dry_run 除外）。
func add[In any](s *mcp.Server, d Deps, tool *mcp.Tool, audited bool, fn toolFunc[In]) {
	mcp.AddTool[In, any](s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		out, err := fn(ctx, in)
		if dr, ok := any(in).(dryRunner); audited && !(ok && dr.isDryRun()) {
			d.audit(tool.Name, in, out, err, time.Since(start))
		}
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}

func jsonResult(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(apperr.New(apperr.Internal, "序列化结果失败: "+err.Error(), ""))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func errorResult(err error) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(apperr.From(err), "", "  ")
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

// 这些错误码表示请求被规则拒绝，而不是执行出错。
var rejectedCodes = map[apperr.Code]bool{
	apperr.InvalidArgument:          true,
	apperr.ConfirmRequired:          true,
	apperr.RebootQuotaExceeded:      true,
	apperr.AddFileChangedExternally: true,
}

func (d Deps) audit(tool string, in, out any, err error, dur time.Duration) {
	e := audit.Entry{TS: time.Now(), Tool: tool, Args: toMap(in), DurationMS: dur.Milliseconds(), Outcome: "ok"}
	if err != nil {
		ae := apperr.From(err)
		e.ErrorCode, e.Summary, e.Outcome = string(ae.Code), ae.Message, "error"
		if rejectedCodes[ae.Code] {
			e.Outcome = "rejected"
		}
	} else if data, mErr := json.Marshal(out); mErr == nil {
		e.Summary = truncate(string(data), 300)
	}
	if werr := d.Audit.Write(e); werr != nil {
		slog.Warn("写入审计日志失败", "err", werr)
	}
}

func toMap(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

func invalid(msg, hint string) error { return apperr.New(apperr.InvalidArgument, msg, hint) }

func requireConfirm(confirm bool) error {
	if !confirm {
		return apperr.New(apperr.ConfirmRequired, "该操作需要 confirm: true", "先向用户说明影响并获得同意，再带 confirm: true 重新调用")
	}
	return nil
}

// lineLimit 处理 lines 参数：0 表示默认 200，范围 1-2000。
func lineLimit(n int) (int, error) {
	if n == 0 {
		return 200, nil
	}
	if n < 1 || n > 2000 {
		return 0, invalid("lines 必须在 1-2000 之间", "")
	}
	return n, nil
}
