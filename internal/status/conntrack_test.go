package status

import (
	"context"
	"testing"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestParseConntrack(t *testing.T) {
	c, err := ParseConntrack("@@MERLINMCP:count@@\n1500\n@@MERLINMCP:max@@\n30000\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Count != 1500 || c.Max != 30000 || c.UsagePercent != 5 || c.Warning != "" {
		t.Fatalf("c = %+v", c)
	}
	c, _ = ParseConntrack("@@MERLINMCP:count@@\n25000\n@@MERLINMCP:max@@\n30000\n")
	if c.Warning == "" {
		t.Fatal("使用率 >= 80% 应给出提示")
	}
	if _, err := ParseConntrack("@@MERLINMCP:count@@\n\n@@MERLINMCP:max@@\n0\n"); err == nil {
		t.Fatal("无法解析时应报错")
	}
}

func TestFetchConntrack(t *testing.T) {
	f := runnertest.New().On("conntrack", runnertest.Stdout("@@MERLINMCP:count@@\n10\n@@MERLINMCP:max@@\n100\n"))
	c, err := FetchConntrack(context.Background(), f)
	if err != nil || c.UsagePercent != 10 {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}
