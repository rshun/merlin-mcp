package diagnose

import (
	"os"
	"path/filepath"
	"testing"
)

// 真实样本来自 RT-AX86U / Merlin 388.12_2（busybox），已脱敏。
func readReal(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", name))
	if err != nil {
		t.Fatalf("读取真实样本 %s 失败: %v", name, err)
	}
	return string(data)
}

func TestParsePingRealSample(t *testing.T) {
	loss, avg := ParsePing(readReal(t, "ping.txt"))
	if loss == nil || *loss != 0 || avg == nil || *avg != 0.197 {
		t.Fatalf("loss=%v avg=%v", loss, avg)
	}
}

func TestParseNslookupRealSample(t *testing.T) {
	if got := ParseNslookup(readReal(t, "nslookup.txt")); len(got) != 1 || got[0] != "192.0.2.1" {
		t.Fatalf("got = %v", got)
	}
}
