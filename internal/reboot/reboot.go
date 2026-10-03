// Package reboot 实现每日重启额度和重启操作。
package reboot

import (
	"context"
	"fmt"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/state"
)

const historyLimit = 30

// 后台延迟 2 秒执行，让 SSH 会话先正常返回。
const rebootScript = "nohup sh -c 'sleep 2; reboot' >/dev/null 2>&1 </dev/null &"

// Quota 按配置时区的自然日统计由 MCP 发起的重启次数。
type Quota struct {
	store *state.Store
	loc   *time.Location
	max   int
	now   func() time.Time
}

// NewQuota 创建额度检查器。now 在测试中可替换。
func NewQuota(store *state.Store, loc *time.Location, maxPerDay int, now func() time.Time) *Quota {
	return &Quota{store: store, loc: loc, max: maxPerDay, now: now}
}

// Status 是 system_status 中的重启额度信息。
type Status struct {
	AllowedToday    bool    `json:"reboot_allowed_today"`
	UsedToday       int     `json:"used_today"`
	MaxPerDay       int     `json:"max_per_day"`
	LastMCPRebootAt *string `json:"last_mcp_reboot_at"`
}

func (q *Quota) usedOn(history []time.Time, now time.Time) int {
	y, m, d := now.In(q.loc).Date()
	n := 0
	for _, t := range history {
		ty, tm, td := t.In(q.loc).Date()
		if ty == y && tm == m && td == d {
			n++
		}
	}
	return n
}

// Status 返回今天的额度使用情况。
func (q *Quota) Status() Status {
	h := q.store.Get().Reboot.History
	used := q.usedOn(h, q.now())
	s := Status{AllowedToday: used < q.max, UsedToday: used, MaxPerDay: q.max}
	if len(h) > 0 {
		v := h[len(h)-1].In(q.loc).Format(time.RFC3339)
		s.LastMCPRebootAt = &v
	}
	return s
}

// Reserve 检查额度，未超限时立即记录一次重启并落盘，返回记录的时间。
func (q *Quota) Reserve() (time.Time, error) {
	now := q.now()
	err := q.store.Update(func(s *state.State) error {
		h := s.Reboot.History
		if used := q.usedOn(h, now); used >= q.max {
			return apperr.New(apperr.RebootQuotaExceeded,
				fmt.Sprintf("今天（%s）已由 MCP 发起 %d 次重启，达到每日上限 %d 次", now.In(q.loc).Format("2006-01-02"), used, q.max),
				"如确需重启，请人工在路由器管理界面操作").
				With("last_reboot_at", h[len(h)-1].In(q.loc).Format(time.RFC3339))
		}
		h = append(h, now)
		if len(h) > historyLimit {
			h = h[len(h)-historyLimit:]
		}
		s.Reboot.History = h
		return nil
	})
	return now, err
}

// Rebooter 先占用额度，再在路由器上执行重启。
type Rebooter struct {
	quota *Quota
	r     runner.Runner
}

// NewRebooter 创建 Rebooter。r 必须是原始 Runner（不能带重试）。
func NewRebooter(q *Quota, r runner.Runner) *Rebooter { return &Rebooter{quota: q, r: r} }

// Result 是 router_reboot 成功时的返回内容。
type Result struct {
	RequestedAt string `json:"requested_at"`
	Message     string `json:"message"`
}

// Reboot 记录落盘后才执行命令；命令失败也计入额度。完成后断开 SSH 连接。
func (b *Rebooter) Reboot(ctx context.Context) (Result, error) {
	at, err := b.quota.Reserve()
	if err != nil {
		return Result{}, err
	}
	if rs, ok := b.r.(interface{ Reset() }); ok {
		defer rs.Reset()
	}
	res, err := b.r.Run(ctx, runner.Op("reboot", rebootScript), nil)
	if err == nil && res.ExitCode != 0 {
		err = runner.Failed(res)
	}
	if err != nil {
		return Result{}, apperr.From(err).With("quota_consumed", true).With("note", "重启额度已计入今天，即使命令执行结果不明确")
	}
	return Result{
		RequestedAt: at.In(b.quota.loc).Format(time.RFC3339),
		Message:     "已发出重启指令，路由器约 2 秒后重启，通常 1-3 分钟后恢复",
	}, nil
}
