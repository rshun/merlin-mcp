package dnsmasq

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/state"
)

// Options 是 Manager 的配置。路径都是路由器上的路径。
type Options struct {
	AddPath      string
	BackupDir    string
	HealthDomain string
	Keep         int
	Store        *state.Store
	Sleep        func(time.Duration) // 测试时替换为空函数
	PIDWait      time.Duration       // 等待 dnsmasq 新进程出现的上限，默认 10s
}

// Manager 管理 dnsmasq.conf.add。所有读改写操作共用一把锁。
type Manager struct {
	r  runner.Runner
	o  Options
	mu sync.Mutex
}

// New 创建 Manager。
func New(r runner.Runner, o Options) *Manager {
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.PIDWait <= 0 {
		o.PIDWait = 10 * time.Second
	}
	return &Manager{r: r, o: o}
}

// NumberedLine 是带行号的一行。
type NumberedLine struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// FileView 是 dnsmasq_addfile_read 的返回内容。
type FileView struct {
	Path    string         `json:"path"`
	Exists  bool           `json:"exists"`
	SHA256  string         `json:"sha256"`
	Lines   []NumberedLine `json:"lines"`
	content string
}

func number(content string) []NumberedLine {
	lines := splitContent(content)
	out := make([]NumberedLine, 0, len(lines))
	for i, l := range lines {
		out = append(out, NumberedLine{N: i + 1, Text: l})
	}
	return out
}

// Read 读取 .add 文件；文件不存在时 Exists 为 false，不报错。
func (m *Manager) Read(ctx context.Context) (FileView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read(ctx)
}

func (m *Manager) read(ctx context.Context) (FileView, error) {
	p := shell.Quote(m.o.AddPath)
	out, err := runner.Output(ctx, m.r, runner.Op("addfile_read",
		fmt.Sprintf("if [ -f %s ]; then echo EXISTS; cat %s; else echo MISSING; fi", p, p)))
	if err != nil {
		return FileView{}, err
	}
	head, body, _ := strings.Cut(out, "\n")
	v := FileView{Path: m.o.AddPath, Exists: strings.TrimSpace(head) == "EXISTS"}
	if v.Exists {
		v.content = body
	}
	v.SHA256 = SHA256(v.content)
	v.Lines = number(v.content)
	return v, nil
}

// write 通过 stdin 写入内容，先写临时文件再 mv，保证原子替换。
func (m *Manager) write(ctx context.Context, content string) error {
	tmp := shell.Quote(m.o.AddPath + ".mcp-tmp")
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && mv %s %s",
		shell.Quote(path.Dir(m.o.AddPath)), tmp, tmp, shell.Quote(m.o.AddPath))
	res, err := m.r.Run(ctx, runner.Op("addfile_write", cmd), []byte(content))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return runner.Failed(res)
	}
	return nil
}

var backupNameRe = regexp.MustCompile(`^dnsmasq\.conf\.add\.(\d{8}-\d{6})(?:-(\d+))?$`)

// backup 把 content 写成一份新的备份，返回备份文件名（不含目录）。
// 文件名使用路由器本地时间，同一秒内重复时追加 -2、-3。
func (m *Manager) backup(ctx context.Context, content string) (string, error) {
	script := fmt.Sprintf(`d=%s; mkdir -p "$d" && ts=$(date +%%Y%%m%%d-%%H%%M%%S) && f="$d/dnsmasq.conf.add.$ts" && n=2 && `+
		`while [ -e "$f" ]; do f="$d/dnsmasq.conf.add.$ts-$n"; n=$((n+1)); done && cat > "$f" && echo "$f"`,
		shell.Quote(m.o.BackupDir))
	res, err := m.r.Run(ctx, runner.Op("addfile_backup", script), []byte(content))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", runner.Failed(res)
	}
	name := path.Base(strings.TrimSpace(string(res.Stdout)))
	if !backupNameRe.MatchString(name) {
		return "", apperr.New(apperr.Internal, "备份文件名异常: "+name, "")
	}
	return name, nil
}

func (m *Manager) readBackup(ctx context.Context, name string) (string, error) {
	if !backupNameRe.MatchString(name) {
		return "", fmt.Errorf("非法的备份文件名 %q", name)
	}
	return runner.Output(ctx, m.r, runner.Op("backup_read", "cat "+shell.Quote(path.Join(m.o.BackupDir, name))))
}

// prune 只保留最新的 Keep 份备份，protect 指向的备份（known good）永不删除。
func (m *Manager) prune(ctx context.Context, protect string) error {
	out, err := runner.Output(ctx, m.r, runner.Op("backup_list", fmt.Sprintf("ls -1 %s 2>/dev/null || true", shell.Quote(m.o.BackupDir))))
	if err != nil {
		return err
	}
	victims := PruneList(strings.Fields(out), m.o.Keep, protect)
	if len(victims) == 0 {
		return nil
	}
	quoted := make([]string, len(victims))
	for i, v := range victims {
		quoted[i] = shell.Quote(v)
	}
	_, err = runner.Output(ctx, m.r, runner.Op("backup_prune",
		fmt.Sprintf("cd %s && rm -f -- %s", shell.Quote(m.o.BackupDir), strings.Join(quoted, " "))))
	return err
}

// PruneList 计算需要删除的备份：只考虑符合命名规则的文件，排除 protect，保留最新的 keep 份。
func PruneList(names []string, keep int, protect string) []string {
	type item struct {
		name, ts string
		seq      int
	}
	var items []item
	for _, n := range names {
		mm := backupNameRe.FindStringSubmatch(n)
		if mm == nil || n == protect {
			continue
		}
		seq := 1
		if mm[2] != "" {
			seq, _ = strconv.Atoi(mm[2])
		}
		items = append(items, item{n, mm[1], seq})
	}
	if len(items) <= keep {
		return nil
	}
	sort.Slice(items, func(i, j int) bool { // 新的在前
		if items[i].ts != items[j].ts {
			return items[i].ts > items[j].ts
		}
		return items[i].seq > items[j].seq
	})
	var out []string
	for _, it := range items[keep:] {
		out = append(out, it.name)
	}
	sort.Strings(out)
	return out
}

// EditOp 是 Edit 的操作类型。
type EditOp string

const (
	OpAdd    EditOp = "add"
	OpRemove EditOp = "remove"
)

// EditResult 是 dnsmasq_addfile_add / remove 的返回内容。
type EditResult struct {
	DryRun                 bool     `json:"dry_run"`
	Changed                bool     `json:"changed"`
	Added                  []string `json:"added,omitempty"`
	Skipped                []string `json:"skipped,omitempty"`
	Removed                []string `json:"removed,omitempty"`
	NotFound               []string `json:"not_found,omitempty"`
	Diff                   string   `json:"diff"`
	SHA256                 string   `json:"sha256"`
	Backup                 string   `json:"backup,omitempty"`
	ExternalChangeDetected bool     `json:"external_change_detected"`
	PendingApply           bool     `json:"pending_apply"`
	Warning                string   `json:"warning,omitempty"`
}

// Edit 添加或删除行。只修改文件，不让 dnsmasq 生效。
// 第一次修改时，修改前的内容同时作为初始的 known good（假设它就是当前生效的版本）。
func (m *Manager) Edit(ctx context.Context, op EditOp, lines []string, dryRun bool) (EditResult, error) {
	norm, err := NormalizeInput(lines)
	if err != nil {
		return EditResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	view, err := m.read(ctx)
	if err != nil {
		return EditResult{}, err
	}
	st := m.o.Store.Get().Dnsmasq
	res := EditResult{DryRun: dryRun, ExternalChangeDetected: st.LastMCPSHA256 != "" && st.LastMCPSHA256 != view.SHA256}

	var next string
	switch op {
	case OpAdd:
		next, res.Added, res.Skipped = Add(view.content, norm)
	case OpRemove:
		next, res.Removed, res.NotFound = Remove(view.content, norm)
	default:
		return EditResult{}, apperr.New(apperr.InvalidArgument, "未知操作: "+string(op), "")
	}
	res.Changed = next != join(splitContent(view.content))
	res.Diff = Diff(view.content, next)
	res.SHA256 = view.SHA256
	if res.Changed {
		res.SHA256 = SHA256(next)
	}
	if dryRun || !res.Changed {
		return res, nil
	}

	name, err := m.backup(ctx, view.content)
	if err != nil {
		return EditResult{}, err
	}
	if err := m.write(ctx, next); err != nil {
		return EditResult{}, err
	}
	err = m.o.Store.Update(func(s *state.State) error {
		if s.Dnsmasq.KnownGoodBackup == "" {
			s.Dnsmasq.KnownGoodBackup = name
		}
		s.Dnsmasq.LastMCPSHA256 = res.SHA256
		return nil
	})
	if err != nil {
		return EditResult{}, apperr.New(apperr.Internal, "文件已修改，但状态文件写入失败: "+err.Error(), "检查 state_dir 是否可写")
	}
	res.Backup, res.PendingApply = name, true
	if err := m.prune(ctx, m.o.Store.Get().Dnsmasq.KnownGoodBackup); err != nil {
		res.Warning = "清理旧备份失败: " + apperr.From(err).Message
	}
	return res, nil
}

// Effective 读取 /etc/dnsmasq.conf（Merlin 合并 .add 后实际生成的配置），可按关键字过滤，保留原始行号。
func (m *Manager) Effective(ctx context.Context, keyword string) ([]NumberedLine, error) {
	out, err := runner.Output(ctx, m.r, runner.Op("dnsmasq_effective", "cat /etc/dnsmasq.conf"))
	if err != nil {
		return nil, err
	}
	all := number(out)
	if keyword == "" {
		return all, nil
	}
	kw := strings.ToLower(keyword)
	var hits []NumberedLine
	for _, l := range all {
		if strings.Contains(strings.ToLower(l.Text), kw) {
			hits = append(hits, l)
		}
	}
	return hits, nil
}
