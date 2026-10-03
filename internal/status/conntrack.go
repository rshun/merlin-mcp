package status

import (
	"context"
	"strconv"
	"strings"

	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Conntrack 是 conntrack_status 的返回内容。
type Conntrack struct {
	Count        int     `json:"count"`
	Max          int     `json:"max"`
	UsagePercent float64 `json:"usage_percent"`
	Warning      string  `json:"warning,omitempty"`
}

func conntrackCmd() string {
	return runner.Op("conntrack", strings.Join([]string{
		routercmd.Marker("count"), "(cat /proc/sys/net/netfilter/nf_conntrack_count || cat /proc/sys/net/ipv4/netfilter/ip_conntrack_count) 2>/dev/null",
		routercmd.Marker("max"), "(cat /proc/sys/net/netfilter/nf_conntrack_max || cat /proc/sys/net/ipv4/netfilter/ip_conntrack_max) 2>/dev/null",
	}, "; "))
}

// FetchConntrack 读取连接跟踪表使用情况。
func FetchConntrack(ctx context.Context, r runner.Runner) (Conntrack, error) {
	out, err := runner.Output(ctx, r, conntrackCmd())
	if err != nil {
		return Conntrack{}, err
	}
	return ParseConntrack(out)
}

// ParseConntrack 解析 conntrackCmd 的输出，使用率 >= 80% 时附加提示。
func ParseConntrack(out string) (Conntrack, error) {
	sec := routercmd.Sections(out)
	count, err1 := strconv.Atoi(strings.TrimSpace(sec["count"]))
	limit, err2 := strconv.Atoi(strings.TrimSpace(sec["max"]))
	if err1 != nil || err2 != nil || limit <= 0 {
		return Conntrack{}, parseErr("conntrack")
	}
	c := Conntrack{Count: count, Max: limit, UsagePercent: round1(100 * float64(count) / float64(limit))}
	if c.UsagePercent >= 80 {
		c.Warning = "连接跟踪表使用率已超过 80%，接近上限时新连接会被丢弃，表现为部分网站打不开或时断时续"
	}
	return c, nil
}
