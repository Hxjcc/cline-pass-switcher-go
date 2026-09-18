package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Details is the normalized error shape shared by upstream adapters.
type Details struct {
	Status  int
	Type    string
	Code    any
	Param   any
	Message string
}

func asMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func StringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}

func firstNonEmpty(values ...any) string {
	for _, value := range values {
		if text := StringValue(value); text != "" {
			return text
		}
	}
	return ""
}

// InferType maps HTTP status and common upstream messages to an OpenAI-style
// error type without discarding the original text.
func InferType(status int, message string, code any) string {
	text := strings.ToLower(message + " " + StringValue(code))
	switch {
	case status == http.StatusUnauthorized || strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "invalid api key") || strings.Contains(text, "authentication"):
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound || strings.Contains(text, "model not found"):
		return "not_found_error"
	case status == http.StatusTooManyRequests || strings.Contains(text, "rate limit") ||
		strings.Contains(text, "rate_limit") || strings.Contains(text, "rate-limited") ||
		strings.Contains(text, "rate_limited") || strings.Contains(text, "too many requests"):
		return "rate_limit_error"
	case strings.Contains(text, "insufficient balance") || strings.Contains(text, "insufficient quota") ||
		strings.Contains(text, "quota exceeded") || strings.Contains(text, "billing"):
		return "insufficient_quota"
	case strings.Contains(text, "context length") || strings.Contains(text, "context_length") ||
		strings.Contains(text, "maximum context") || strings.Contains(text, "too many tokens"):
		return "context_length_exceeded"
	case strings.Contains(text, "content filter") || strings.Contains(text, "content_filter") ||
		strings.Contains(text, "safety"):
		return "content_filter"
	case status == http.StatusRequestTimeout || strings.Contains(text, "timeout") ||
		strings.Contains(text, "timed out"):
		return "timeout_error"
	case status == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case status == http.StatusBadRequest || strings.Contains(text, "invalid_request") ||
		strings.Contains(text, "invalid request") || strings.Contains(text, "invalid payload"):
		return "invalid_request_error"
	case status == http.StatusServiceUnavailable || strings.Contains(text, "overloaded") ||
		strings.Contains(text, "unavailable"):
		return "server_error"
	case status >= 500:
		return "server_error"
	default:
		return "upstream_error"
	}
}

func StatusForType(errorType string) int {
	switch errorType {
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "insufficient_quota":
		return http.StatusTooManyRequests
	case "context_length_exceeded", "content_filter", "invalid_request_error":
		return http.StatusBadRequest
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "timeout_error":
		return http.StatusGatewayTimeout
	case "server_error":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func resolveStatus(details Details, fallback int) int {
	if fallback >= 400 && fallback <= 599 {
		return fallback
	}
	if inferred := StatusForType(details.Type); inferred >= 400 {
		return inferred
	}
	if details.Status >= 400 && details.Status <= 599 {
		return details.Status
	}
	return http.StatusBadGateway
}

// FromBody extracts an error object from the common OpenAI-compatible shapes.
// found is true for non-2xx responses or bodies carrying an explicit error.
func FromBody(root map[string]any, status int) (Details, bool) {
	details := Details{Status: status}
	found := status < 200 || status >= 300
	if root == nil {
		if found {
			details.Message = http.StatusText(status)
			if details.Message == "" {
				details.Message = "upstream error"
			}
			details.Type = InferType(status, details.Message, nil)
			details.Status = resolveStatus(details, status)
		}
		return details, found
	}

	if raw, exists := root["error"]; exists {
		found = true
		switch value := raw.(type) {
		case string:
			details.Message = strings.TrimSpace(value)
		case map[string]any:
			details.Message = firstNonEmpty(value["message"], value["detail"], value["msg"], value["error"])
			details.Type = StringValue(value["type"])
			details.Code = value["code"]
			details.Param = value["param"]
		default:
			details.Message = StringValue(value)
		}
	}
	if details.Message == "" {
		details.Message = firstNonEmpty(root["message"], root["detail"], root["msg"], root["error_description"])
	}
	if details.Type == "" {
		details.Type = StringValue(root["type"])
	}
	if details.Code == nil {
		details.Code = root["code"]
	}
	if details.Param == nil {
		details.Param = root["param"]
	}
	if base := asMap(root["base_resp"]); base != nil {
		found = true
		if details.Message == "" {
			details.Message = firstNonEmpty(base["status_msg"], base["message"])
		}
		if details.Code == nil {
			details.Code = base["status_code"]
		}
	}
	if !found {
		return details, false
	}
	if details.Message == "" {
		details.Message = http.StatusText(status)
		if details.Message == "" {
			details.Message = "upstream error"
		}
	}
	if details.Type == "" {
		details.Type = InferType(status, details.Message, details.Code)
	}
	details.Status = resolveStatus(details, status)
	return details, true
}

func Body(details Details) map[string]any {
	errorType := details.Type
	if errorType == "" {
		errorType = "upstream_error"
	}
	errorObject := map[string]any{
		"message": details.Message,
		"type":    errorType,
		"code":    details.Code,
		"param":   details.Param,
	}
	return map[string]any{"error": errorObject}
}

func Status(message string) int {
	details, _ := FromBody(map[string]any{"message": message}, 0)
	return details.Status
}

func Network(err error, code string) Details {
	message := "upstream request failed"
	if err != nil {
		message = err.Error()
	}
	errorType := "upstream_error"
	lower := strings.ToLower(message)
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") {
		errorType = "timeout_error"
	} else if errors.Is(err, context.Canceled) {
		errorType = "request_cancelled"
	}
	return Details{
		Status:  StatusForType(errorType),
		Type:    errorType,
		Code:    code,
		Message: message,
	}
}
