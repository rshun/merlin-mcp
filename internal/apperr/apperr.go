// Package apperr 定义工具返回给 AI 的错误码和错误结构。
package apperr

import (
	"errors"
	"fmt"
)

// Code 是返回给 AI 的错误码，取值见 spec 第 8 节。
type Code string

const (
	InvalidArgument          Code = "INVALID_ARGUMENT"
	ConfirmRequired          Code = "CONFIRM_REQUIRED"
	SSHUnreachable           Code = "SSH_UNREACHABLE"
	SSHAuthFailed            Code = "SSH_AUTH_FAILED"
	HostKeyMismatch          Code = "HOSTKEY_MISMATCH"
	Timeout                  Code = "TIMEOUT"
	CommandFailed            Code = "COMMAND_FAILED"
	RebootQuotaExceeded      Code = "REBOOT_QUOTA_EXCEEDED"
	AddFileChangedExternally Code = "ADDFILE_CHANGED_EXTERNALLY"
	DnsmasqValidationFailed  Code = "DNSMASQ_VALIDATION_FAILED"
	DnsmasqRolledBack        Code = "DNSMASQ_ROLLED_BACK"
	DnsmasqRollbackFailed    Code = "DNSMASQ_ROLLBACK_FAILED"
	Internal                 Code = "INTERNAL"
)

// Error 是工具失败时返回的结构，序列化后放进 MCP 结果的文本内容中。
type Error struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New 创建一个错误。message 和 hint 使用中文。
func New(code Code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint}
}

// With 附加一项细节，返回自身以便链式调用。
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// From 把任意错误转换成 *Error；不是 *Error 的错误归为 INTERNAL。
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(Internal, err.Error(), "")
}

// CodeOf 返回错误码；err 为 nil 时返回空字符串。
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	return From(err).Code
}
