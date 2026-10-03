package dnsmasq

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/state"
)

// ApplyResult 是 dnsmasq_apply 成功时的返回内容。
type ApplyResult struct {
	ValidationSkipped string `json:"validation_skipped,omitempty"`
	OldPID            string `json:"old_pid"`
	NewPID            string `json:"new_pid"`
	HealthCheck       string `json:"health_check"`
	KnownGoodBackup   string `json:"known_good_backup"`
	SHA256            string `json:"sha256"`
}

// Apply 让 .add 文件生效：检查外部修改 → 语法校验 → 重启 dnsmasq → 等待新进程 → 解析检查。
// 重启后的任一步失败都会恢复 known good 版本并再次重启（流程见 spec 5.3）。
func (m *Manager) Apply(ctx context.Context, acceptExternal bool) (ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	view, err := m.read(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	st := m.o.Store.Get().Dnsmasq
	if st.LastMCPSHA256 != "" && view.SHA256 != st.LastMCPSHA256 && !acceptExternal {
		return ApplyResult{}, apperr.New(apperr.AddFileChangedExternally, "dnsmasq.conf.add 在 MCP 之外被修改过",
			"先用 dnsmasq_addfile_read 查看当前内容，向用户确认后带 accept_external_changes: true 再次调用").
			With("expected_sha256", st.LastMCPSHA256).With("current_sha256", view.SHA256)
	}
	if st.KnownGoodBackup == "" {
		name, err := m.backup(ctx, view.content)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := m.setState(name, view.SHA256); err != nil {
			return ApplyResult{}, err
		}
		st.KnownGoodBackup = name
	}

	var res ApplyResult
	if view.Exists {
		skipped, err := m.validate(ctx)
		if err != nil {
			return ApplyResult{}, err
		}
		res.ValidationSkipped = skipped
	}

	oldPID, newPID, cause := m.restartAndCheck(ctx)
	if cause != nil {
		return ApplyResult{}, m.rollback(ctx, st.KnownGoodBackup, view.content, cause)
	}
	name, err := m.backup(ctx, view.content)
	if err != nil {
		return ApplyResult{}, apperr.New(apperr.Internal, "dnsmasq 已生效，但保存 known good 备份失败: "+apperr.From(err).Message, "")
	}
	if err := m.setState(name, view.SHA256); err != nil {
		return ApplyResult{}, err
	}
	_ = m.prune(ctx, name)
	res.OldPID, res.NewPID, res.HealthCheck, res.KnownGoodBackup, res.SHA256 = oldPID, newPID, "ok", name, view.SHA256
	return res, nil
}

func (m *Manager) setState(knownGood, sha string) error {
	err := m.o.Store.Update(func(s *state.State) error {
		s.Dnsmasq.KnownGoodBackup = knownGood
		s.Dnsmasq.LastMCPSHA256 = sha
		return nil
	})
	if err != nil {
		return apperr.New(apperr.Internal, "状态文件写入失败: "+err.Error(), "检查 state_dir 是否可写")
	}
	return nil
}

// validate 执行 dnsmasq --test；路由器的 dnsmasq 不支持 --test 时返回跳过说明。
func (m *Manager) validate(ctx context.Context) (string, error) {
	res, err := m.r.Run(ctx, runner.Op("dnsmasq_test", "dnsmasq --test -C "+shell.Quote(m.o.AddPath)+" 2>&1"), nil)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(res.Stdout))
	lower := strings.ToLower(out)
	switch {
	case res.ExitCode == 0:
		return "", nil
	case strings.Contains(lower, "--test") && (strings.Contains(lower, "unrecognized") || strings.Contains(lower, "invalid") || strings.Contains(lower, "illegal")):
		return "路由器上的 dnsmasq 不支持 --test，已跳过语法校验", nil
	default:
		return "", apperr.New(apperr.DnsmasqValidationFailed, "dnsmasq 语法校验未通过，没有重启 dnsmasq，线上配置不受影响",
			"根据 details.output 修正配置后重试").With("output", out)
	}
}

func (m *Manager) pid(ctx context.Context) (string, error) {
	out, err := runner.Output(ctx, m.r, runner.Op("pidof_dnsmasq", "pidof dnsmasq || true"))
	return strings.TrimSpace(out), err
}

// restartAndCheck 重启 dnsmasq，等待出现新的 PID，再做一次解析检查。
// Asus 的 service 命令只是异步通知 rc，所以必须等 PID 变化，不能只看命令返回。
func (m *Manager) restartAndCheck(ctx context.Context) (oldPID, newPID string, err error) {
	if oldPID, err = m.pid(ctx); err != nil {
		return "", "", err
	}
	if _, err = runner.Output(ctx, m.r, runner.Op("restart_dnsmasq", "service restart_dnsmasq")); err != nil {
		return oldPID, "", fmt.Errorf("执行 service restart_dnsmasq 失败: %w", err)
	}
	const step = 500 * time.Millisecond
	for waited := time.Duration(0); waited < m.o.PIDWait; waited += step {
		m.o.Sleep(step)
		cur, err := m.pid(ctx)
		if err != nil {
			return oldPID, "", err
		}
		if isNewProcess(oldPID, cur) {
			newPID = cur
			break
		}
	}
	if newPID == "" {
		return oldPID, "", fmt.Errorf("dnsmasq 在 %s 内没有以新进程重新启动（重启前 PID: %q）", m.o.PIDWait, oldPID)
	}
	if err := m.health(ctx); err != nil {
		return oldPID, newPID, err
	}
	return oldPID, newPID, nil
}

// isNewProcess 判断 pidof 的输出是否代表全新的 dnsmasq：非空，且与重启前的 PID 没有交集。
// Asus 上 dnsmasq 通常有两个进程，只退出一个时剩下的旧 PID 不能算作新进程。
func isNewProcess(oldPIDs, curPIDs string) bool {
	cur := strings.Fields(curPIDs)
	if len(cur) == 0 {
		return false
	}
	old := map[string]bool{}
	for _, p := range strings.Fields(oldPIDs) {
		old[p] = true
	}
	for _, p := range cur {
		if old[p] {
			return false
		}
	}
	return true
}

func (m *Manager) health(ctx context.Context) error {
	res, err := m.r.Run(ctx, runner.Op("dnsmasq_health",
		fmt.Sprintf("nslookup %s 127.0.0.1 2>&1", shell.Quote(m.o.HealthDomain))), nil)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 || len(diagnose.ParseNslookup(string(res.Stdout))) == 0 {
		return fmt.Errorf("健康检查失败：dnsmasq 无法解析 %s，输出: %s", m.o.HealthDomain, strings.TrimSpace(string(res.Stdout)))
	}
	return nil
}

// 回滚使用独立的时间预算，不受调用方取消或 apply 整体超时的影响。
const rollbackTimeout = 40 * time.Second

// rollback 先备份即将被覆盖的内容，再恢复 known good 版本并重启，
// 返回 DNSMASQ_ROLLED_BACK 或 DNSMASQ_ROLLBACK_FAILED。
func (m *Manager) rollback(ctx context.Context, knownGood, failedContent string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()

	backupPath := path.Join(m.o.BackupDir, knownGood)
	// 被覆盖的内容可能包含只存在于 .add 文件中的人工修改；备份失败也继续回滚，优先恢复 DNS
	failedBackup := ""
	if name, err := m.backup(ctx, failedContent); err == nil {
		failedBackup = path.Join(m.o.BackupDir, name)
	}
	withFailed := func(e *apperr.Error) *apperr.Error {
		if failedBackup != "" {
			e.With("failed_content_backup", failedBackup)
		}
		return e
	}
	fail := func(rbErr error) error {
		return withFailed(apperr.New(apperr.DnsmasqRollbackFailed, "dnsmasq 生效失败，自动回滚也失败了，需要人工处理",
			"登录路由器，把 details.backup_path 的内容复制回 dnsmasq.conf.add，然后执行 service restart_dnsmasq").
			With("cause", errText(cause)).With("rollback_error", errText(rbErr)).With("backup_path", backupPath))
	}
	content, err := m.readBackup(ctx, knownGood)
	if err != nil {
		return fail(err)
	}
	if err := m.write(ctx, content); err != nil {
		return fail(err)
	}
	if _, _, err := m.restartAndCheck(ctx); err != nil {
		return fail(err)
	}
	if err := m.o.Store.Update(func(s *state.State) error {
		s.Dnsmasq.LastMCPSHA256 = SHA256(content)
		return nil
	}); err != nil {
		return fail(err)
	}
	return withFailed(apperr.New(apperr.DnsmasqRolledBack, "新配置生效失败，已自动回滚到上一个可用版本",
		"根据 details.cause 修正配置；未生效的内容已备份在 details.failed_content_backup").
		With("cause", errText(cause)).With("restored_backup", backupPath))
}

// errText 优先使用 apperr 的中文 message，其他错误用 Error()。
func errText(err error) string {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
