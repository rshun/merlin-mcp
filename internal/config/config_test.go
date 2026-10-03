package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fixture struct{ dir, key, kh string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, key: filepath.Join(dir, "id_ed25519"), kh: filepath.Join(dir, "known_hosts")}
	if err := os.WriteFile(f.key, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.kh, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// yaml 返回只包含必填项的配置，extra 追加在末尾（只能追加顶层键）。
func (f fixture) yaml(extra string) string {
	return fmt.Sprintf(`router:
  host: 192.0.2.1
  user: admin
  key_file: %s
  known_hosts: %s
reboot:
  timezone: Asia/Shanghai
state_dir: %s
audit_log: %s
%s`, filepath.ToSlash(f.key), filepath.ToSlash(f.kh), filepath.ToSlash(f.dir),
		filepath.ToSlash(filepath.Join(f.dir, "audit.jsonl")), extra)
}

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	c, err := load(t, f.yaml(""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8765" || c.Router.Port != 22 || c.CommandTimeout != 15*time.Second {
		t.Errorf("默认值不正确: %+v", c)
	}
	if c.Paths.DnsmasqAdd != "/jffs/configs/dnsmasq.conf.add" || c.Paths.BackupDir != "/jffs/merlin-mcp/backups" || c.Paths.Syslog != "auto" {
		t.Errorf("paths 默认值不正确: %+v", c.Paths)
	}
	if c.Dnsmasq.HealthCheckDomain != "router.asus.com" || c.Dnsmasq.BackupKeep != 20 || c.Reboot.MaxPerDay != 1 {
		t.Errorf("默认值不正确: %+v %+v", c.Dnsmasq, c.Reboot)
	}
	if c.AllowMutations {
		t.Error("allow_mutations 默认应为 false")
	}
	if c.Location == nil || c.Location.String() != "Asia/Shanghai" {
		t.Errorf("Location = %v", c.Location)
	}
}

func TestLoadRejectsNonLoopbackListen(t *testing.T) {
	f := newFixture(t)
	for _, l := range []string{"0.0.0.0:8765", "192.0.2.5:8765", "localhost:8765"} {
		_, err := load(t, f.yaml("listen: \""+l+"\"\n"))
		if err == nil || !strings.Contains(err.Error(), "回环地址") {
			t.Errorf("listen=%s 应被拒绝，err=%v", l, err)
		}
	}
	if _, err := load(t, f.yaml("listen: \"[::1]:8765\"\n")); err != nil {
		t.Errorf("::1 应被接受: %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	f := newFixture(t)
	if _, err := load(t, f.yaml("allow_mutation: true\n")); err == nil {
		t.Fatal("拼错的字段应被拒绝")
	}
}

func TestLoadRequiresTimezone(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), "  timezone: Asia/Shanghai\n", "  max_per_day: 1\n", 1)
	_, err := load(t, content)
	if err == nil || !strings.Contains(err.Error(), "reboot.timezone") {
		t.Fatalf("缺少时区应报错，err=%v", err)
	}
}

func TestLoadRejectsInvalidTimezone(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), "Asia/Shanghai", "Mars/Olympus", 1)
	if _, err := load(t, content); err == nil {
		t.Fatal("无效时区应报错")
	}
}

func TestLoadRejectsUnsafeRouterPaths(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/jffs/configs/x;reboot", "relative/path", "/jffs/../etc/passwd", "/jffs/a b"} {
		if _, err := load(t, f.yaml("paths:\n  dnsmasq_add: \""+p+"\"\n")); err == nil {
			t.Errorf("路径 %q 应被拒绝", p)
		}
	}
}

func TestLoadRejectsMissingKeyFile(t *testing.T) {
	f := newFixture(t)
	content := strings.Replace(f.yaml(""), filepath.ToSlash(f.key), filepath.ToSlash(f.key)+".missing", 1)
	_, err := load(t, content)
	if err == nil || !strings.Contains(err.Error(), "router.key_file") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsKeyFileTooOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位")
	}
	f := newFixture(t)
	if err := os.Chmod(f.key, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := load(t, f.yaml(""))
	if err == nil || !strings.Contains(err.Error(), "权限过宽") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsBadHealthDomainAndKeep(t *testing.T) {
	f := newFixture(t)
	_, err := load(t, f.yaml("dnsmasq:\n  health_check_domain: \"a b\"\n  backup_keep: 0\n"))
	if err == nil || !strings.Contains(err.Error(), "health_check_domain") || !strings.Contains(err.Error(), "backup_keep") {
		t.Fatalf("err = %v", err)
	}
}
