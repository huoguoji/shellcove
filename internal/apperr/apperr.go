// Package apperr 定义统一的业务错误码与 HTTP 状态映射。
//
// 原则：密码类错误统一提示“密码错误”，不暴露细节。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error 是可携带错误码与 HTTP 状态的业务错误。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New 构造一个新的业务错误。
func New(code, message string, status int) *Error {
	return &Error{Code: code, Message: message, Status: status}
}

// WithMessage 基于原错误码返回一个替换了提示文案的新错误（不修改原对象）。
func (e *Error) WithMessage(message string) *Error {
	return &Error{Code: e.Code, Message: message, Status: e.Status}
}

// Wrap 在保留错误码的前提下包装底层错误信息，用于服务端日志。
func (e *Error) Wrap(err error) *Error {
	if err == nil {
		return e
	}
	return &Error{Code: e.Code, Message: e.Message, Status: e.Status}
}

// Unwrap 支持 errors.Is / errors.As 链式判断。
func (e *Error) Unwrap() error { return nil }

// As 判断 err 链上是否存在业务错误。
func As(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// CodeOf 返回错误码，非业务错误返回 INTERNAL_ERROR。
func CodeOf(err error) string {
	if e, ok := As(err); ok {
		return e.Code
	}
	return CodeInternal
}

// StatusOf 返回 HTTP 状态码，非业务错误返回 500。
func StatusOf(err error) int {
	if e, ok := As(err); ok && e.Status > 0 {
		return e.Status
	}
	return http.StatusInternalServerError
}

// MessageOf 返回面向用户的提示文案。
func MessageOf(err error) string {
	if e, ok := As(err); ok {
		return e.Message
	}
	return "服务内部错误"
}

// Is 判断错误码是否匹配。
func Is(err error, code string) bool {
	return CodeOf(err) == code
}

// 通用错误码。
const (
	CodeInternal        = "INTERNAL_ERROR"
	CodeBadRequest      = "BAD_REQUEST"
	CodeNotFound        = "NOT_FOUND"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeConflict        = "CONFLICT"
	CodeTooManyRequests = "TOO_MANY_REQUESTS"
	CodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
)

// 认证类错误码。
const (
	CodeAuthFailed          = "AUTH_FAILED"
	CodeInvalidCredentials  = "INVALID_CREDENTIALS"
	CodeTOTPRequired        = "TOTP_REQUIRED"
	CodeTOTPInvalid         = "TOTP_INVALID"
	CodePasswordMustChange  = "PASSWORD_MUST_CHANGE"
	CodeWeakPassword        = "WEAK_PASSWORD"
	CodeAccountDisabled     = "ACCOUNT_DISABLED"
	CodeSessionLimitReached = "SESSION_LIMIT_REACHED"
	CodeNoPermission        = "NO_PERMISSION"
)

// 备份导出错误码（见规格 7.1）。
const (
	CodeExportTokenInvalid  = "EXPORT_TOKEN_INVALID"
	CodeWeakBackupPassword  = "WEAK_BACKUP_PASSWORD"
	CodeNothingToExport     = "NOTHING_TO_EXPORT"
	CodeEncryptionFailed    = "ENCRYPTION_FAILED"
)

// 备份导入错误码（见规格 7.2）。
const (
	CodeInvalidFileFormat    = "INVALID_FILE_FORMAT"
	CodeUnsupportedVersion   = "UNSUPPORTED_VERSION"
	CodeChecksumMismatch     = "CHECKSUM_MISMATCH"
	CodeWrongBackupPassword  = "WRONG_BACKUP_PASSWORD"
	CodeWrongMasterKey       = "WRONG_MASTER_KEY"
	CodeDecryptionFailed     = "DECRYPTION_FAILED"
	CodeImportTokenInvalid   = "IMPORT_TOKEN_INVALID"
	CodeVersionMigrationFail = "VERSION_MIGRATION_FAILED"
	CodeDiskFull             = "DISK_FULL"
)

// 常用错误实例。
var (
	ErrInternal     = New(CodeInternal, "服务内部错误", http.StatusInternalServerError)
	ErrBadRequest   = New(CodeBadRequest, "请求参数不合法", http.StatusBadRequest)
	ErrNotFound     = New(CodeNotFound, "资源不存在", http.StatusNotFound)
	ErrUnauthorized = New(CodeUnauthorized, "未登录或登录已过期", http.StatusUnauthorized)
	ErrForbidden    = New(CodeForbidden, "没有权限执行该操作", http.StatusForbidden)
	ErrNoPermission = New(CodeNoPermission, "没有该资源的访问权限", http.StatusForbidden)
	ErrConflict     = New(CodeConflict, "资源冲突", http.StatusConflict)

	ErrAuthFailed        = New(CodeAuthFailed, "密码错误", http.StatusUnauthorized)
	ErrTOTPRequired      = New(CodeTOTPRequired, "需要两步验证码", http.StatusUnauthorized)
	ErrTOTPInvalid       = New(CodeTOTPInvalid, "动态验证码错误", http.StatusUnauthorized)
	ErrPasswordMustChg   = New(CodePasswordMustChange, "首次登录必须修改密码", http.StatusForbidden)
	ErrWeakPassword      = New(CodeWeakPassword, "密码强度不足", http.StatusBadRequest)
	ErrAccountDisabled   = New(CodeAccountDisabled, "账号已被禁用", http.StatusForbidden)
	ErrSessionLimit      = New(CodeSessionLimitReached, "已达到并发会话数上限", http.StatusTooManyRequests)
	ErrTooManyRequests   = New(CodeTooManyRequests, "操作过于频繁，请稍后再试", http.StatusTooManyRequests)
	ErrPayloadTooLarge   = New(CodePayloadTooLarge, "请求内容过大", http.StatusRequestEntityTooLarge)

	ErrExportTokenInvalid = New(CodeExportTokenInvalid, "导出凭证已失效，请重新验证", http.StatusBadRequest)
	ErrWeakBackupPassword = New(CodeWeakBackupPassword, "备份密码强度不足（至少 12 位）", http.StatusBadRequest)
	ErrNothingToExport    = New(CodeNothingToExport, "没有可导出的数据", http.StatusBadRequest)
	ErrEncryptionFailed   = New(CodeEncryptionFailed, "加密过程出错", http.StatusInternalServerError)

	ErrInvalidFileFormat   = New(CodeInvalidFileFormat, "不是合法的备份文件", http.StatusBadRequest)
	ErrUnsupportedVersion  = New(CodeUnsupportedVersion, "备份版本高于当前程序版本，无法导入", http.StatusBadRequest)
	ErrChecksumMismatch    = New(CodeChecksumMismatch, "校验和不匹配，文件可能已损坏", http.StatusBadRequest)
	ErrWrongBackupPassword = New(CodeWrongBackupPassword, "密码错误", http.StatusBadRequest)
	ErrWrongMasterKey      = New(CodeWrongMasterKey, "密码错误", http.StatusBadRequest)
	ErrDecryptionFailed    = New(CodeDecryptionFailed, "解密失败", http.StatusBadRequest)
	ErrImportTokenInvalid  = New(CodeImportTokenInvalid, "导入凭证已失效，请重新上传", http.StatusBadRequest)
	ErrVersionMigration    = New(CodeVersionMigrationFail, "数据迁移失败", http.StatusInternalServerError)
	ErrDiskFull            = New(CodeDiskFull, "磁盘空间不足", http.StatusInsufficientStorage)
)
