package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/dnsmasq"
)

type EditArgs struct {
	Lines  []string `json:"lines" jsonschema:"要添加或删除的配置行，每个元素一行，1-100 行"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"为 true 时只返回 diff，不修改文件"`
}

func (a EditArgs) isDryRun() bool { return a.DryRun }

type ApplyArgs struct {
	Confirm               bool `json:"confirm,omitempty" jsonschema:"必须为 true；调用前需向用户确认"`
	AcceptExternalChanges bool `json:"accept_external_changes,omitempty" jsonschema:"检测到文件在 MCP 之外被修改时，是否仍然继续"`
}

type RebootArgs struct {
	Confirm bool `json:"confirm,omitempty" jsonschema:"必须为 true；调用前需向用户确认"`
}

func registerMutations(s *mcp.Server, d Deps) {
	editAnn := &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: true, OpenWorldHint: boolPtr(false)}

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_add",
		Description: "向 dnsmasq.conf.add 追加配置行（已存在的行会跳过），写入前自动备份。只修改文件，不会生效；修改完成后需要调用 dnsmasq_apply。可先用 dry_run 预览 diff。",
		Annotations: editAnn,
	}, true, func(ctx context.Context, in EditArgs) (any, error) {
		return d.Dnsmasq.Edit(ctx, dnsmasq.OpAdd, in.Lines, in.DryRun)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_remove",
		Description: "从 dnsmasq.conf.add 删除与给定内容完全一致的行，找不到的行会在结果中列出。写入前自动备份。只修改文件，不会生效；修改完成后需要调用 dnsmasq_apply。可先用 dry_run 预览 diff。",
		Annotations: editAnn,
	}, true, func(ctx context.Context, in EditArgs) (any, error) {
		return d.Dnsmasq.Edit(ctx, dnsmasq.OpRemove, in.Lines, in.DryRun)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_apply",
		Description: "让 dnsmasq.conf.add 的修改生效：语法校验 → 重启 dnsmasq → 等待新进程 → 解析检查。任一步失败会自动回滚到上一个可用版本。重启期间局域网 DNS 会中断几秒。必须先向用户确认，再带 confirm: true 调用。",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false), IdempotentHint: false, OpenWorldHint: boolPtr(false)},
	}, true, func(ctx context.Context, in ApplyArgs) (any, error) {
		if err := requireConfirm(in.Confirm); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return d.Dnsmasq.Apply(ctx, in.AcceptExternalChanges)
	})

	add(s, d, &mcp.Tool{
		Name:        "router_reboot",
		Description: "重启路由器。按配置的时区，每个自然日最多通过 MCP 重启 1 次。重启期间网络中断约 1-3 分钟。必须先向用户确认，再带 confirm: true 调用。",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)},
	}, true, func(ctx context.Context, in RebootArgs) (any, error) {
		if err := requireConfirm(in.Confirm); err != nil {
			return nil, err
		}
		return d.Rebooter.Reboot(ctx)
	})
}
