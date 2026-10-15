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

// MaxRetryAfterSeconds caps retry_after_seconds so retry-after-ms cannot
// overflow and a typo cannot stall a client for days.
const MaxRetryAfterSeconds = 3600

// SetRetryHeaders stamps the retry hints SDKs read on rate_limit and
// overloaded responses: retry-after (seconds) and retry-after-ms. They are
// sent only when the profile sets a positive retry_after_seconds, so profiles
// that do not opt in keep their header-free behavior. Call it before writing
// the response status.
func SetRetryHeaders(ctx context.Context, w http.ResponseWriter) {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok || rt.Profile.RetryAfterSeconds <= 0 {
		return
	}
	seconds := rt.Profile.RetryAfterSeconds
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Retry-After-Ms", strconv.Itoa(seconds*1000))
}
