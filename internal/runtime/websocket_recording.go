package runtimecfg

import (
	"context"
	"sync/atomic"
)

type webSocketStatsKey struct{}

// WebSocketStats carries per-request upgrade/frame counters from provider
// handlers back to the local recording middleware.
type WebSocketStats struct {
	upgraded       atomic.Bool
	framesSent     atomic.Int64
	framesReceived atomic.Int64
}

func WithWebSocketStats(ctx context.Context) (context.Context, *WebSocketStats) {
	stats := &WebSocketStats{}
	return context.WithValue(ctx, webSocketStatsKey{}, stats), stats
}

func MarkWebSocketUpgraded(ctx context.Context) {
	if stats, ok := WebSocketStatsFromContext(ctx); ok {
		stats.upgraded.Store(true)
	}
}

func RecordWebSocketFrameSent(ctx context.Context) {
	if stats, ok := WebSocketStatsFromContext(ctx); ok {
		stats.framesSent.Add(1)
	}
}

func RecordWebSocketFrameReceived(ctx context.Context) {
	if stats, ok := WebSocketStatsFromContext(ctx); ok {
		stats.framesReceived.Add(1)
	}
}

func WebSocketStatsFromContext(ctx context.Context) (*WebSocketStats, bool) {
	stats, ok := ctx.Value(webSocketStatsKey{}).(*WebSocketStats)
	return stats, ok
}

func (s *WebSocketStats) Upgraded() bool {
	return s != nil && s.upgraded.Load()
}

func (s *WebSocketStats) FramesSent() int {
	if s == nil {
		return 0
	}
	return int(s.framesSent.Load())
}

func (s *WebSocketStats) FramesReceived() int {
	if s == nil {
		return 0
	}
	return int(s.framesReceived.Load())
}
