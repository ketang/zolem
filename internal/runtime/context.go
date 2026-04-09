package runtimecfg

import "context"

type listenerRuntimeKey struct{}

// WithListenerRuntime attaches listener runtime metadata to the context.
func WithListenerRuntime(ctx context.Context, rt ListenerRuntime) context.Context {
	return context.WithValue(ctx, listenerRuntimeKey{}, rt)
}

// ListenerRuntimeFromContext retrieves listener runtime metadata from context.
func ListenerRuntimeFromContext(ctx context.Context) (ListenerRuntime, bool) {
	v := ctx.Value(listenerRuntimeKey{})
	if v == nil {
		return ListenerRuntime{}, false
	}
	rt, ok := v.(ListenerRuntime)
	return rt, ok
}
