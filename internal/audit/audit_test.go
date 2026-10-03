package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAppendsJSONLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "audit.jsonl")
	l := New(p)
	e := Entry{TS: time.Unix(0, 0).UTC(), Tool: "router_reboot", Args: map[string]any{"confirm": true}, Outcome: "ok", DurationMS: 12}
	if err := l.Write(e); err != nil {
		t.Fatal(err)
	}
	e.Outcome = "rejected"
	e.ErrorCode = "REBOOT_QUOTA_EXCEEDED"
	if err := l.Write(e); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &got); err != nil {
		t.Fatal(err)
	}
	if got["tool"] != "router_reboot" || got["outcome"] != "rejected" || got["error_code"] != "REBOOT_QUOTA_EXCEEDED" {
		t.Fatalf("got = %v", got)
	}
}

func TestRedactMasksSensitiveKeysButNotKeyword(t *testing.T) {
	in := map[string]any{"password": "x", "api_key": "y", "Token": "z", "keyword": "dnsmasq", "lines": []string{"a"}}
	out := Redact(in)
	for _, k := range []string{"password", "api_key", "Token"} {
		if out[k] != "****" {
			t.Errorf("%s 应被脱敏，得到 %v", k, out[k])
		}
	}
	if out["keyword"] != "dnsmasq" {
		t.Errorf("keyword 不应被脱敏，得到 %v", out["keyword"])
	}
	if in["password"] != "x" {
		t.Error("Redact 不应修改输入")
	}
}
