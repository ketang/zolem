package runtimecfg

import "context"

const (
	ErrorTypeAuthentication = "authentication"
	ErrorTypePermission     = "permission"
	ErrorTypeInvalidRequest = "invalid_request"
	ErrorTypeRateLimit      = "rate_limit"
	ErrorTypeServerError    = "server_error"
)

func ForcedErrorTypeForRequest(ctx context.Context) (string, bool) {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok || rt.Profile.Backend != BackendError || rt.Profile.ErrorType == "" {
		return "", false
	}
	return rt.Profile.ErrorType, true
}
