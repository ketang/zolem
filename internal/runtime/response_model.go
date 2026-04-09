package runtimecfg

import "context"

const (
	ResponseModelEchoRequest  = "echo_request"
	ResponseModelForceLiteral = "force_literal"
	ResponseModelForceBackend = "force_backend"
)

func ResponseModelForRequest(ctx context.Context, requestModel string) string {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok {
		return requestModel
	}

	switch rt.Profile.ResponseModelPolicy {
	case "", ResponseModelEchoRequest:
		return requestModel
	case ResponseModelForceLiteral:
		if rt.Profile.ResponseModel != "" {
			return rt.Profile.ResponseModel
		}
		return requestModel
	case ResponseModelForceBackend:
		if rt.Profile.BackendModel != "" {
			return rt.Profile.BackendModel
		}
		return requestModel
	default:
		return requestModel
	}
}
