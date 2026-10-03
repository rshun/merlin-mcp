package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestFromWrapsUnknownErrorAsInternal(t *testing.T) {
	e := From(errors.New("boom"))
	if e.Code != Internal || e.Message != "boom" {
		t.Fatalf("got %+v", e)
	}
}

func TestFromUnwrapsWrappedError(t *testing.T) {
	orig := New(Timeout, "命令执行超时", "")
	wrapped := fmt.Errorf("调用失败: %w", orig)
	if CodeOf(wrapped) != Timeout {
		t.Fatalf("CodeOf = %q", CodeOf(wrapped))
	}
	if From(wrapped) != orig {
		t.Fatal("From 应返回原始 *Error")
	}
}

func TestWithAddsDetails(t *testing.T) {
	e := New(CommandFailed, "失败", "").With("stderr", "bad").With("code", 2)
	if e.Details["stderr"] != "bad" || e.Details["code"] != 2 {
		t.Fatalf("details = %v", e.Details)
	}
}

func TestCodeOfNil(t *testing.T) {
	if CodeOf(nil) != "" {
		t.Fatal("nil 错误应返回空错误码")
	}
}

func TestErrorString(t *testing.T) {
	if got := New(Timeout, "慢", "").Error(); got != "TIMEOUT: 慢" {
		t.Fatalf("Error() = %q", got)
	}
}
