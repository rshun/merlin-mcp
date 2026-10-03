package shell

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"abc":  "'abc'",
		"":     "''",
		"it's": `'it'\''s'`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// 通过真实的 sh 验证转义后的字符串能原样还原，命令通过 stdin 传入，避免 Windows 命令行转义干扰。
func TestQuoteRoundTripThroughSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("没有 sh，跳过")
	}
	inputs := []string{"plain", "it's", "$(echo pwned)", "`id`", "a;b|c&d", `back\slash`, "多字节 中文", "'''", "a\tb"}
	for _, s := range inputs {
		cmd := exec.Command(sh)
		cmd.Stdin = strings.NewReader("printf %s " + Quote(s))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh 执行失败 %q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("Quote(%q) 经 sh 还原为 %q", s, out)
		}
	}
}

func TestValidHost(t *testing.T) {
	good := []string{"example.com", "router.asus.com", "localhost", "a-b.example.", "192.0.2.1", "2001:db8::1"}
	bad := []string{"", "-oProxyCommand=x", "a b", "$(id)", "a;b", "exa_mple.com", "a..b", "-a.com", "fe80::1%eth0", strings.Repeat("a", 64) + ".com"}
	for _, s := range good {
		if !ValidHost(s) {
			t.Errorf("ValidHost(%q) 应为 true", s)
		}
	}
	for _, s := range bad {
		if ValidHost(s) {
			t.Errorf("ValidHost(%q) 应为 false", s)
		}
	}
}

func TestValidIP(t *testing.T) {
	if !ValidIP("127.0.0.1") || !ValidIP("::1") || ValidIP("example.com") || ValidIP("1.2.3") {
		t.Fatal("ValidIP 结果不正确")
	}
}

func TestValidProcessName(t *testing.T) {
	for _, s := range []string{"dnsmasq", "dnsmasq-dhcp", "rc_service", "kernel"} {
		if !ValidProcessName(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range []string{"", "a b", "x;y", "$(id)", strings.Repeat("a", 65)} {
		if ValidProcessName(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}

func TestValidIfName(t *testing.T) {
	for _, s := range []string{"eth6", "wl0.1", "br0", "eth7"} {
		if !ValidIfName(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range []string{"", "-i", "eth 0", "eth0;reboot", "abcdefghijklmnop"} {
		if ValidIfName(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}
