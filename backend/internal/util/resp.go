package util

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const requestIDContextKey = "ctx_request_id"

type errorResponse struct {
	Code              int    `json:"code"`
	ErrorCode         string `json:"error_code"`
	Message           string `json:"message"`
	RequestID         string `json:"request_id,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(200, gin.H{"code": 0, "data": data})
}

func Fail(c *gin.Context, status int, msg string) {
	FailCode(c, status, errorCodeFor(status, msg), msg)
}

// FailCode returns the common error envelope while keeping the legacy numeric
// code and message fields intact for existing API clients.
func FailCode(c *gin.Context, status int, code, msg string) {
	writeError(c, status, code, msg, 0)
}

// FailRetry adds an actionable retry delay to both the JSON body and standard
// Retry-After header. Clients can render an exact countdown instead of treating
// every HTTP 429 as the same error.
func FailRetry(c *gin.Context, status int, code, msg string, retryAfter time.Duration) {
	writeError(c, status, code, msg, retryAfter)
}

func writeError(c *gin.Context, status int, code, msg string, retryAfter time.Duration) {
	if strings.TrimSpace(code) == "" {
		code = errorCodeFor(status, msg)
	}
	c.Set("ctx_error_code", code)
	c.Set("ctx_error_message", msg)
	seconds := int64(0)
	if retryAfter > 0 {
		seconds = int64(math.Ceil(retryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	requestID, _ := c.Get(requestIDContextKey)
	requestIDText, _ := requestID.(string)
	c.JSON(status, errorResponse{
		Code:              status,
		ErrorCode:         code,
		Message:           msg,
		RequestID:         requestIDText,
		RetryAfterSeconds: seconds,
	})
}

func errorCodeFor(status int, message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if code := exactErrorCodes[normalized]; code != "" {
		return code
	}

	switch {
	case strings.Contains(normalized, "oauth"):
		return "auth.oauth_failed"
	case strings.Contains(normalized, "authenticator") || strings.Contains(normalized, "totp"):
		return "auth.totp_invalid"
	case strings.Contains(normalized, "password reset"):
		return "auth.password_reset_failed"
	case strings.Contains(normalized, "verification code"):
		return "auth.verification_code_invalid"
	case strings.Contains(normalized, "email") && (strings.Contains(normalized, "send") || strings.Contains(normalized, "service") || strings.Contains(normalized, "smtp")):
		return "email.unavailable"
	case strings.Contains(normalized, "payment") || strings.Contains(normalized, "order"):
		return "payment.failed"
	case strings.Contains(normalized, "proxy"):
		return "proxy.failed"
	case strings.Contains(normalized, "referral"):
		return "referral.invalid"
	case strings.Contains(normalized, "backup"):
		return "backup.failed"
	case strings.Contains(normalized, "update") && status >= http.StatusConflict:
		return "update.failed"
	case strings.Contains(normalized, "payload too large") || strings.Contains(normalized, "request body too large"):
		return "request.too_large"
	case strings.Contains(normalized, "concurrency") || strings.Contains(normalized, "accounts are busy"):
		return "request.concurrency_limited"
	case strings.Contains(normalized, "rate limit") || strings.Contains(normalized, "too many"):
		return "request.rate_limited"
	case strings.Contains(normalized, "quota") || strings.Contains(normalized, "balance") || strings.Contains(normalized, "额度"):
		return "quota.unavailable"
	case strings.Contains(normalized, "no available upstream") || strings.Contains(normalized, "upstream group is unavailable"):
		return "upstream.unavailable"
	case strings.Contains(normalized, "upstream"):
		return "upstream.failed"
	case strings.Contains(normalized, "group") && strings.Contains(normalized, "disabled"):
		return "group.disabled"
	case strings.Contains(normalized, "key") && strings.Contains(normalized, "disabled"):
		return "api_key.disabled"
	case strings.Contains(normalized, "not found"):
		return "resource.not_found"
	case strings.Contains(normalized, "already") || strings.Contains(normalized, "conflict"):
		return "resource.conflict"
	}

	switch status {
	case http.StatusBadRequest:
		return "request.invalid"
	case http.StatusUnauthorized:
		return "auth.required"
	case http.StatusForbidden:
		return "permission.denied"
	case http.StatusNotFound:
		return "resource.not_found"
	case http.StatusConflict:
		return "resource.conflict"
	case http.StatusRequestEntityTooLarge:
		return "request.too_large"
	case http.StatusTooManyRequests:
		return "request.rate_limited"
	case http.StatusBadGateway:
		return "upstream.failed"
	case http.StatusServiceUnavailable:
		return "service.unavailable"
	default:
		if status >= http.StatusInternalServerError {
			return "server.internal"
		}
		return "operation.failed"
	}
}

var exactErrorCodes = map[string]string{
	"invalid request":                                      "request.invalid",
	"invalid json body":                                    "request.invalid_json",
	"missing api key":                                      "api_key.missing",
	"invalid api key":                                      "api_key.invalid",
	"api key disabled":                                     "api_key.disabled",
	"api key expired":                                      "api_key.expired",
	"api key source ip is not allowed":                     "api_key.ip_denied",
	"api key rate limit reached":                           "api_key.rate_limited",
	"user rate limit reached":                              "user.rate_limited",
	"incorrect email or password":                          "auth.invalid_credentials",
	"too many failed attempts, try again later":            "auth.too_many_attempts",
	"account disabled":                                     "auth.account_disabled",
	"user disabled":                                        "auth.account_disabled",
	"invalid or expired token":                             "auth.session_expired",
	"session expired, please sign in again":                "auth.session_expired",
	"session device changed, please sign in again":         "auth.session_changed",
	"authenticator code is required or invalid":            "auth.totp_invalid",
	"authenticator code is invalid":                        "auth.totp_invalid",
	"latest terms must be accepted":                        "auth.terms_required",
	"admin only":                                           "permission.admin_required",
	"recent two-factor verification is required":           "permission.step_up_required",
	"key not found":                                        "api_key.not_found",
	"key secret is unavailable":                            "api_key.secret_unavailable",
	"key secret does not match":                            "api_key.secret_mismatch",
	"group not found":                                      "group.not_found",
	"group disabled":                                       "group.disabled",
	"account not found":                                    "account.not_found",
	"user not found":                                       "user.not_found",
	"payment order not found":                              "payment.order_not_found",
	"online payment is not enabled":                        "payment.disabled",
	"email already registered":                             "auth.email_registered",
	"invalid or expired verification code":                 "auth.verification_code_invalid",
	"invalid or expired password reset code":               "auth.password_reset_invalid",
	"please wait before requesting another code":           "auth.code_rate_limited",
	"concurrency limit reached; retry later":               "request.concurrency_limited",
	"upstream accounts are busy; retry later":              "upstream.busy",
	"no available upstream account in the selected groups": "upstream.unavailable",
}
