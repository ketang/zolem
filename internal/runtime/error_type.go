package runtimecfg

import (
	"context"
	"net/http"
	"strconv"
)

const (
	ErrorTypeAuthentication = "authentication"
	ErrorTypePermission     = "permission"
	ErrorTypeInvalidRequest = "invalid_request"
	ErrorTypeRateLimit      = "rate_limit"
	ErrorTypeServerError    = "server_error"
	ErrorTypeOverloaded     = "overloaded"
)

func ForcedErrorTypeForRequest(ctx context.Context) (string, bool) {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok || rt.Profile.Backend != BackendError || rt.Profile.ErrorType == "" {
		return "", false
	}
	return rt.Profile.ErrorType, true
}

// SetRetryHeaders stamps the retry hints SDKs read on rate_limit and
// overloaded responses: retry-after (seconds) and retry-after-ms. Both are
// always sent; the value is the profile's retry_after_seconds (0 when unset).
// Call it before writing the response status.
func SetRetryHeaders(ctx context.Context, w http.ResponseWriter) {
	seconds := 0
	if rt, ok := ListenerRuntimeFromContext(ctx); ok {
		seconds = rt.Profile.RetryAfterSeconds
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Retry-After-Ms", strconv.Itoa(seconds*1000))
}
