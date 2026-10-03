package dnsmasq

import (
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

func TestNormalizeInput(t *testing.T) {
	got, err := NormalizeInput([]string{"address=/a.example/0.0.0.0  ", "address=/a.example/0.0.0.0", "# 注释"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "address=/a.example/0.0.0.0" || got[1] != "# 注释" {
		t.Fatalf("got = %q", got)
	}
	bad := [][]string{
		nil,
		{"a\nb"},
		{"a\rb"},
		{"a\x00b"},
		{"   "},
		make([]string, 101),
	}
	for _, in := range bad {
		if _, err := NormalizeInput(in); apperr.CodeOf(err) != apperr.InvalidArgument {
			t.Errorf("NormalizeInput(%q) 应返回 INVALID_ARGUMENT，得到 %v", in, err)
		}
	}
}

func TestAddAppendsAndSkips(t *testing.T) {
	next, added, skipped := Add("dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\n", []string{"dhcp-mac=set:openwrt,AA:BB:CC:00:00:01", "address=/a.example/0.0.0.0"})
	if next != "dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\naddress=/a.example/0.0.0.0\n" {
		t.Fatalf("next = %q", next)
	}
	if len(added) != 1 || len(skipped) != 1 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	if next, _, _ := Add("", []string{"x=1"}); next != "x=1\n" {
		t.Fatalf("空文件添加 = %q", next)
	}
}

func TestRemove(t *testing.T) {
	next, removed, notFound := Remove("a=1\nb=2\na=1\nc=3\n", []string{"a=1", "z=9"})
	if next != "b=2\nc=3\n" {
		t.Fatalf("应删除所有匹配的行，next = %q", next)
	}
	if len(removed) != 1 || removed[0] != "a=1" || len(notFound) != 1 || notFound[0] != "z=9" {
		t.Fatalf("removed=%v notFound=%v", removed, notFound)
	}
	if next, _, _ := Remove("a=1\n", []string{"a=1"}); next != "" {
		t.Fatalf("删光后应为空，得到 %q", next)
	}
}

func TestRemoveMatchesCRLFFile(t *testing.T) {
	next, removed, _ := Remove("a=1\r\nb=2  \r\n\r\n", []string{"b=2"})
	if next != "a=1\n" || len(removed) != 1 {
		t.Fatalf("next=%q removed=%v", next, removed)
	}
}

func TestBlankLinesInMiddleArePreserved(t *testing.T) {
	next, _, _ := Add("a=1\n\nb=2\n\n\n", []string{"c=3"})
	if next != "a=1\n\nb=2\nc=3\n" {
		t.Fatalf("next = %q", next)
	}
}

func TestDiff(t *testing.T) {
	d := Diff("a\nb\nc\n", "a\nc\nd\n")
	want := " a\n-b\n c\n+d\n"
	if d != want {
		t.Fatalf("diff =\n%s\nwant\n%s", d, want)
	}
	if Diff("a\r\n", "a\n") != " a\n" {
		t.Fatal("只有换行符不同时不应产生差异")
	}
}

func TestSHA256(t *testing.T) {
	if got := SHA256(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("got = %s", got)
	}
	if strings.ToLower(SHA256("x")) != SHA256("x") {
		t.Fatal("应为小写十六进制")
	}
}
