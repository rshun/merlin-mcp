package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/clients"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/status"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

type SyslogArgs struct {
	Lines          int    `json:"lines,omitempty" jsonschema:"返回的最大行数，1-2000，默认 200"`
	Keyword        string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
	Process        string `json:"process,omitempty" jsonschema:"进程名，例如 dnsmasq，精确匹配 name: 或 name[pid]:"`
	Since          string `json:"since,omitempty" jsonschema:"只看最近一段时间，例如 30m、2h、1d，最大 7d"`
	IncludeRotated bool   `json:"include_rotated,omitempty" jsonschema:"是否同时读取轮转出去的旧日志"`
	Date           string `json:"date,omitempty" jsonschema:"只看某一天的日志，格式 YYYY-MM-DD；今天从路由器读取，之前的日期从本机历史归档读取。不能与 since 同时使用"`
}

type KernelLogArgs struct {
	Lines   int    `json:"lines,omitempty" jsonschema:"返回的最大行数，1-2000，默认 200"`
	Keyword string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
}

type NoArgs struct{}

type DiagnoseArgs struct {
	Action string `json:"action" jsonschema:"ping 或 nslookup"`
	Target string `json:"target" jsonschema:"目标域名或 IP 地址"`
	Count  int    `json:"count,omitempty" jsonschema:"ping 次数，1-5，默认 3"`
	Server string `json:"server,omitempty" jsonschema:"nslookup 使用的 DNS 服务器 IP，默认 127.0.0.1（路由器自己的 dnsmasq）"`
}

type KeywordArgs struct {
	Keyword string `json:"keyword,omitempty" jsonschema:"不区分大小写的子串过滤"`
}

type logResult struct {
	Source     string `json:"source,omitempty"` // router 或 archive
	Date       string `json:"date,omitempty"`
	Path       string `json:"path,omitempty"`
	RouterTime string `json:"router_time,omitempty"`
	Returned   int    `json:"returned_lines"`
	Truncated  bool   `json:"truncated"`
	Log        string `json:"log"`
	Note       string `json:"note,omitempty"`
}

var readOnlyAnn = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}

func registerReadOnly(s *mcp.Server, d Deps) {
	ro := runner.WithRetry(d.Runner)

	add(s, d, &mcp.Tool{
		Name:        "syslog_read",
		Description: "读取路由器系统日志（syslog），用于分析问题。可按时间（since）、进程名（process）、关键字（keyword）过滤，返回最新的 lines 行，单次返回不超过 64KB。用 date（YYYY-MM-DD）查看某一天的日志：今天从路由器读取，之前的日期从本机历史归档读取。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in SyslogArgs) (any, error) {
		n, err := lineLimit(in.Lines)
		if err != nil {
			return nil, err
		}
		since, err := syslog.ParseSince(in.Since)
		if err != nil {
			return nil, invalid(err.Error(), "since 示例：30m、2h、1d")
		}
		if in.Process != "" && !shell.ValidProcessName(in.Process) {
			return nil, invalid("process 只能包含字母、数字和 _ . -", "")
		}
		filter := syslog.Filter{Since: since, Process: in.Process, Keyword: in.Keyword, Lines: n}
		if in.Date != "" {
			if in.Since != "" {
				return nil, invalid("date 和 since 不能同时使用", "")
			}
			return readSyslogDay(ctx, ro, d.Cfg, in.Date, filter)
		}
		snap, err := syslog.Fetch(ctx, ro, d.Cfg.Paths.Syslog, in.IncludeRotated)
		if err != nil {
			return nil, err
		}
		lines := syslog.Apply(snap.Lines, filter, snap.Now)
		text, truncated := syslog.Render(syslog.Raws(lines), syslog.MaxOutputBytes)
		return logResult{Source: "router", Path: snap.Path, RouterTime: snap.Now.Format(time.RFC3339), Returned: len(lines), Truncated: truncated, Log: text}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "kernel_log_read",
		Description: "读取路由器内核日志（dmesg），用于排查驱动报错、Wi-Fi 芯片异常、内存耗尽（OOM）等 syslog 中看不到的问题。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in KernelLogArgs) (any, error) {
		n, err := lineLimit(in.Lines)
		if err != nil {
			return nil, err
		}
		lines, err := syslog.FetchKernel(ctx, ro)
		if err != nil {
			return nil, err
		}
		lines = syslog.FilterText(lines, in.Keyword, n)
		text, truncated := syslog.Render(lines, syslog.MaxOutputBytes)
		return logResult{Returned: len(lines), Truncated: truncated, Log: text}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "system_status",
		Description: "查看路由器系统状态：型号、固件版本、运行时长、负载、CPU 使用率、内存、CPU 温度，以及今天是否还能通过 MCP 重启。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		sys, err := status.FetchSystem(ctx, ro)
		if err != nil {
			return nil, err
		}
		return struct {
			status.System
			Reboot reboot.Status `json:"reboot"`
		}{sys, d.Quota.Status()}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "wan_status",
		Description: "查看 WAN 状态：连接状态、协议、WAN IP、网关、DNS，以及 syslog 中最近 20 条 WAN 相关事件（掉线、重连等）。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return status.FetchWAN(ctx, ro, d.Cfg.Paths.Syslog)
	})

	add(s, d, &mcp.Tool{
		Name:        "clients_list",
		Description: "列出客户端：合并 DHCP 租约、ARP 表、静态 IP 分配和无线关联列表，给出每台设备的 IP、主机名、连接方式（wired/2.4G/5G/6G/unknown）、是否静态分配和租约剩余时间。无线设备另有：信号强度 rssi_dbm（越接近 0 越强）、tx_rate_mbps / rx_rate_mbps（路由器发往设备 / 设备发往路由器的最近一个包的速率，设备空闲时可能偏低）、connected_sec（本次无线连接已持续的秒数）。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		list, err := clients.Fetch(ctx, ro)
		if err != nil {
			return nil, err
		}
		return struct {
			Count   int              `json:"count"`
			Clients []clients.Client `json:"clients"`
		}{len(list), list}, nil
	})

	add(s, d, &mcp.Tool{
		Name:        "conntrack_status",
		Description: "查看连接跟踪表（conntrack）使用率。使用率过高时新连接会被丢弃，表现为部分网站打不开或时断时续。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return status.FetchConntrack(ctx, ro)
	})

	add(s, d, &mcp.Tool{
		Name:        "network_diagnose",
		Description: "从路由器上发起 ping 或 nslookup，用于区分是路由器本身的网络问题还是某个客户端的问题。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, false, func(ctx context.Context, in DiagnoseArgs) (any, error) {
		return diagnose.Run(ctx, ro, diagnose.Request{Action: in.Action, Target: in.Target, Count: in.Count, Server: in.Server})
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_addfile_read",
		Description: "读取 dnsmasq.conf.add（Merlin 追加到 dnsmasq 主配置后面的自定义配置），每行带行号，并返回文件 sha256。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, _ NoArgs) (any, error) {
		return d.Dnsmasq.Read(ctx)
	})

	add(s, d, &mcp.Tool{
		Name:        "dnsmasq_effective_config",
		Description: "读取 /etc/dnsmasq.conf，即 Merlin 合并 dnsmasq.conf.add 之后实际生效的 dnsmasq 配置，可按关键字过滤，用于确认自定义配置是否已加载。",
		Annotations: readOnlyAnn,
	}, false, func(ctx context.Context, in KeywordArgs) (any, error) {
		lines, err := d.Dnsmasq.Effective(ctx, in.Keyword)
		if err != nil {
			return nil, err
		}
		return struct {
			Lines any `json:"lines"`
		}{lines}, nil
	})
}

// readSyslogDay 读取某一天的 syslog：今天从路由器读取，之前的日期读本机历史归档。
// 昨天的归档要等凌晨定时任务下载后才有，在此之前退回到路由器读取（含轮转日志）。
func readSyslogDay(ctx context.Context, r runner.Runner, cfg *config.Config, date string, f syslog.Filter) (any, error) {
	day, err := time.ParseInLocation("2006-01-02", date, cfg.Location)
	if err != nil {
		return nil, invalid(fmt.Sprintf("date 格式错误 %q，应为 YYYY-MM-DD", date), "示例：2026-10-05")
	}
	now := time.Now().In(cfg.Location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, cfg.Location)
	if day.After(today) {
		return nil, invalid(fmt.Sprintf("date 不能晚于今天（%s）", today.Format("2006-01-02")), "")
	}

	note := ""
	if day.Before(today) {
		dir := cfg.Paths.SyslogArchive
		if dir != "" {
			p := syslog.ArchivePath(dir, day)
			lines, err := syslog.ReadArchive(p, day)
			if err == nil {
				return dayResult(lines, f, logResult{Source: "archive", Date: date, Path: p}), nil
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, apperr.New(apperr.Internal, err.Error(), "")
			}
		}
		if !day.Equal(today.AddDate(0, 0, -1)) {
			if dir == "" {
				return nil, invalid("未配置 paths.syslog_archive，无法读取历史日志", "在配置文件中把 paths.syslog_archive 设为本机历史日志目录")
			}
			return nil, invalid(fmt.Sprintf("本机没有 %s 的日志归档", date), "归档目录："+dir)
		}
		note = "昨天的本机归档尚未生成，已改为从路由器读取（含轮转日志），内容可能不完整"
	}

	// 路由器按大小轮转日志，当天较早的日志可能已经在轮转文件里，所以总是一起读取
	snap, err := syslog.Fetch(ctx, r, cfg.Paths.Syslog, true)
	if err != nil {
		return nil, err
	}
	return dayResult(syslog.OnDay(snap.Lines, day), f, logResult{
		Source: "router", Date: date, Path: snap.Path, RouterTime: snap.Now.Format(time.RFC3339), Note: note,
	}), nil
}

// dayResult 对某一天的日志应用过滤条件，填入 res 的日志部分。
func dayResult(lines []syslog.Line, f syslog.Filter, res logResult) logResult {
	lines = syslog.Apply(lines, f, time.Time{})
	res.Log, res.Truncated = syslog.Render(syslog.Raws(lines), syslog.MaxOutputBytes)
	res.Returned = len(lines)
	return res
}
