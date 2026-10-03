// Package diagnose 实现从路由器发起的 ping 和 nslookup。
package diagnose

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// Request 是 network_diagnose 的参数。
type Request struct {
	Action string // ping 或 nslookup
	Target string
	Count  int    // ping 次数，默认 3，范围 1-5
	Server string // nslookup 使用的 DNS 服务器，默认 127.0.0.1
}

// Result 是诊断结果。ping 全部丢包、nslookup 解析失败都不算错误，体现在字段里。
type Result struct {
	Action            string   `json:"action"`
	Target            string   `json:"target"`
	Raw               string   `json:"raw"`
	ExitCode          int      `json:"exit_code"`
	PacketLossPercent *float64 `json:"packet_loss_percent,omitempty"`
	AvgRTTMs          *float64 `json:"avg_rtt_ms,omitempty"`
	Resolved          []string `json:"resolved,omitempty"`
}

func invalid(msg string) error { return apperr.New(apperr.InvalidArgument, msg, "") }

// Run 校验参数后在路由器上执行诊断命令。
func Run(ctx context.Context, r runner.Runner, req Request) (Result, error) {
	if !shell.ValidHost(req.Target) {
		return Result{}, invalid("target 必须是合法的域名或 IP 地址")
	}
	var cmd string
	switch req.Action {
	case "ping":
		if req.Count == 0 {
			req.Count = 3
		}
		if req.Count < 1 || req.Count > 5 {
			return Result{}, invalid("count 必须在 1-5 之间")
		}
		cmd = runner.Op("ping", fmt.Sprintf("ping -c %d -W 2 %s 2>&1", req.Count, shell.Quote(req.Target)))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	case "nslookup":
		if req.Server == "" {
			req.Server = "127.0.0.1"
		}
		if !shell.ValidIP(req.Server) {
			return Result{}, invalid("server 必须是 IP 地址")
		}
		cmd = runner.Op("nslookup", fmt.Sprintf("nslookup %s %s 2>&1", shell.Quote(req.Target), shell.Quote(req.Server)))
	default:
		return Result{}, invalid("action 只能是 ping 或 nslookup")
	}

	res, err := r.Run(ctx, cmd, nil)
	if err != nil {
		return Result{}, err
	}
	out := Result{Action: req.Action, Target: req.Target, Raw: strings.TrimSpace(string(res.Stdout)), ExitCode: res.ExitCode}
	if req.Action == "ping" {
		out.PacketLossPercent, out.AvgRTTMs = ParsePing(out.Raw)
	} else {
		out.Resolved = ParseNslookup(out.Raw)
	}
	return out, nil
}

var (
	lossRe = regexp.MustCompile(`([\d.]+)% packet loss`)
	rttRe  = regexp.MustCompile(`= [\d.]+/([\d.]+)/[\d.]+`)
)

// ParsePing 从 busybox ping 输出中取丢包率和平均延迟。
func ParsePing(out string) (loss, avg *float64) {
	if m := lossRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			loss = &v
		}
	}
	if m := rttRe.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			avg = &v
		}
	}
	return loss, avg
}

// ParseNslookup 取出 "Name:" 之后各 "Address" 行中的 IP，兼容新旧两种 busybox 输出格式。
func ParseNslookup(out string) []string {
	var ips []string
	afterName := false
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "Name:") {
			afterName = true
			continue
		}
		if !afterName || !strings.HasPrefix(t, "Address") {
			continue
		}
		_, rest, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 && shell.ValidIP(f[0]) {
			ips = append(ips, f[0])
		}
	}
	return ips
}
