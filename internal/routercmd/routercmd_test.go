package routercmd

import (
	"strings"
	"testing"
)

func TestMarker(t *testing.T) {
	if got := Marker("date"); got != "echo '@@MERLINMCP:date@@'" {
		t.Fatalf("Marker = %q", got)
	}
}

func TestSections(t *testing.T) {
	out := "噪音\n@@MERLINMCP:a@@\nline1\nline2\n@@MERLINMCP:empty@@\n@@MERLINMCP:b@@\nlast-no-newline"
	sec := Sections(out)
	if sec["a"] != "line1\nline2\n" {
		t.Errorf("a = %q", sec["a"])
	}
	if v, ok := sec["empty"]; !ok || v != "" {
		t.Errorf("empty = %q, ok=%v", v, ok)
	}
	if sec["b"] != "last-no-newline" {
		t.Errorf("b = %q", sec["b"])
	}
	if _, ok := sec[""]; ok {
		t.Error("标记之前的内容不应成为一个分段")
	}
}

func TestSectionsHandlesCRLF(t *testing.T) {
	sec := Sections("@@MERLINMCP:a@@\r\nx\r\n")
	if sec["a"] != "x\r\n" {
		t.Fatalf("a = %q", sec["a"])
	}
}

func TestNvramScript(t *testing.T) {
	s, err := NvramScript("productid", "buildno")
	if err != nil {
		t.Fatal(err)
	}
	want := `printf '%s=%s\n' productid "$(nvram get productid)"; printf '%s=%s\n' buildno "$(nvram get buildno)"`
	if s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
}

func TestNvramScriptRejectsSensitiveOrInvalidKeys(t *testing.T) {
	for _, k := range []string{"wan0_pppoe_passwd", "http_passwd", "wl0_wpa_psk", "vpn_secret", "wl0_key1", "Upper", "a;b", ""} {
		if _, err := NvramScript(k); err == nil {
			t.Errorf("键 %q 应被拒绝", k)
		}
	}
}

func TestParseKV(t *testing.T) {
	kv := ParseKV("productid=RT-AX86U\r\ndhcp_staticlist=<AA:BB:CC:00:00:01>192.0.2.10>>\nempty=\nnoequals\n")
	if kv["productid"] != "RT-AX86U" || kv["dhcp_staticlist"] != "<AA:BB:CC:00:00:01>192.0.2.10>>" {
		t.Fatalf("kv = %v", kv)
	}
	if v, ok := kv["empty"]; !ok || v != "" {
		t.Fatalf("empty = %q ok=%v", v, ok)
	}
	if _, ok := kv["noequals"]; ok {
		t.Fatal("没有等号的行应忽略")
	}
}

func TestParseDate(t *testing.T) {
	got, err := ParseDate("1791000000 +0800\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != 1791000000 {
		t.Fatalf("unix = %d", got.Unix())
	}
	if _, off := got.Zone(); off != 8*3600 {
		t.Fatalf("offset = %d", off)
	}
	got, err = ParseDate("0 -0330")
	if err != nil {
		t.Fatal(err)
	}
	if _, off := got.Zone(); off != -(3*3600 + 30*60) {
		t.Fatalf("offset = %d", off)
	}
	for _, bad := range []string{"", "abc +0800", "1 0800", "1 +08", strings.Repeat("9", 30) + " +0000"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) 应失败", bad)
		}
	}
}
