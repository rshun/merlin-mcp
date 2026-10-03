package status

import (
	"context"
	"regexp"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

// WAN 是 wan_status 的返回内容。
type WAN struct {
	State       string   `json:"state"`
	StateCode   string   `json:"state_code"`
	SubState    string   `json:"sub_state_code"`
	AuxState    string   `json:"aux_state_code"`
	Proto       string   `json:"proto"`
	IP          string   `json:"ip"`
	Gateway     string   `json:"gateway"`
	DNS         []string `json:"dns"`
	Events      []string `json:"recent_events"`
	EventsError string   `json:"events_error,omitempty"`
}

// 只读白名单内的键，不读取 PPPoE 密码等字段。
var wanKeys = []string{"wan0_state_t", "wan0_sbstate_t", "wan0_auxstate_t", "wan0_proto", "wan0_ipaddr", "wan0_gateway", "wan0_dns"}

var wanStates = map[string]string{
	"0": "initializing", "1": "connecting", "2": "connected", "3": "disconnected",
	"4": "stopped", "5": "disabled", "6": "stopping",
}

// ParseWAN 解析 nvram 输出。
func ParseWAN(out string) WAN {
	kv := routercmd.ParseKV(out)
	w := WAN{
		StateCode: kv["wan0_state_t"],
		SubState:  kv["wan0_sbstate_t"],
		AuxState:  kv["wan0_auxstate_t"],
		Proto:     kv["wan0_proto"],
		IP:        kv["wan0_ipaddr"],
		Gateway:   kv["wan0_gateway"],
		DNS:       strings.Fields(kv["wan0_dns"]),
	}
	if w.State = wanStates[w.StateCode]; w.State == "" {
		w.State = "unknown"
	}
	if w.DNS == nil {
		w.DNS = []string{}
	}
	return w
}

// WAN 相关日志的匹配模式。根据真实日志样本（Task 17）可能需要调整。
var wanEventRe = regexp.MustCompile(`(?i)(\bwan[0-9]*\b|wan_|wan connection|\bppp|udhcpc|dhcp client)`)

// WANEvents 返回最近 n 条 WAN 相关日志。
func WANEvents(lines []syslog.Line, n int) []string {
	ev := []string{}
	for _, l := range lines {
		if wanEventRe.MatchString(l.Raw) {
			ev = append(ev, l.Raw)
		}
	}
	if len(ev) > n {
		ev = ev[len(ev)-n:]
	}
	return ev
}

// FetchWAN 读取 WAN 状态和最近 20 条 WAN 事件。日志读取失败不影响状态返回。
func FetchWAN(ctx context.Context, r runner.Runner, syslogPath string) (WAN, error) {
	out, err := runner.Output(ctx, r, runner.Op("wan_status", routercmd.MustNvramScript(wanKeys...)))
	if err != nil {
		return WAN{}, err
	}
	w := ParseWAN(out)
	snap, err := syslog.Fetch(ctx, r, syslogPath, false)
	if err != nil {
		w.Events = []string{}
		w.EventsError = apperr.From(err).Message
		return w, nil
	}
	w.Events = WANEvents(snap.Lines, 20)
	return w, nil
}
