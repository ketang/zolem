package runtimecfg

import "context"

type listenerRuntimeKey struct{}
type profileCountersKey struct{}
type profileRequestSequenceKey struct{}

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

func WithProfileCounters(ctx context.Context, counters *ProfileCounters) context.Context {
	return context.WithValue(ctx, profileCountersKey{}, counters)
}

func ProfileCountersFromContext(ctx context.Context) (*ProfileCounters, bool) {
	v := ctx.Value(profileCountersKey{})
	if v == nil {
		return nil, false
	}
	counters, ok := v.(*ProfileCounters)
	return counters, ok
}

func WithProfileRequestSequence(ctx context.Context, seq uint64) context.Context {
	return context.WithValue(ctx, profileRequestSequenceKey{}, seq)
}

func ProfileRequestSequenceFromContext(ctx context.Context) uint64 {
	v := ctx.Value(profileRequestSequenceKey{})
	if v == nil {
		return 0
	}
	seq, ok := v.(uint64)
	if !ok {
		return 0
	}
	return seq
}

func IncrementTemplateRenderForRequest(ctx context.Context) uint64 {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok {
		return 0
	}
	counters, ok := ProfileCountersFromContext(ctx)
	if !ok {
		return 0
	}
	return counters.IncrementTemplateRender(rt.Profile.Name)
}
