package runtimecfg_test

import (
	"context"
	"testing"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func TestWebSocketStatsContextLifecycle(t *testing.T) {
	ctx, stats := runtimecfg.WithWebSocketStats(context.Background())

	if got, ok := runtimecfg.WebSocketStatsFromContext(ctx); !ok || got != stats {
		t.Fatalf("WebSocketStatsFromContext = (%v, %v), want original stats and true", got, ok)
	}
	if stats.Upgraded() {
		t.Fatal("new stats should not start upgraded")
	}
	if stats.FramesSent() != 0 || stats.FramesReceived() != 0 {
		t.Fatalf("new frame counts = sent %d received %d, want zero", stats.FramesSent(), stats.FramesReceived())
	}

	runtimecfg.MarkWebSocketUpgraded(ctx)
	runtimecfg.RecordWebSocketFrameSent(ctx)
	runtimecfg.RecordWebSocketFrameSent(ctx)
	runtimecfg.RecordWebSocketFrameReceived(ctx)

	if !stats.Upgraded() {
		t.Fatal("MarkWebSocketUpgraded did not mark stats upgraded")
	}
	if stats.FramesSent() != 2 {
		t.Fatalf("FramesSent = %d, want 2", stats.FramesSent())
	}
	if stats.FramesReceived() != 1 {
		t.Fatalf("FramesReceived = %d, want 1", stats.FramesReceived())
	}
}

func TestWebSocketStatsNoopWithoutContext(t *testing.T) {
	ctx := context.Background()
	runtimecfg.MarkWebSocketUpgraded(ctx)
	runtimecfg.RecordWebSocketFrameSent(ctx)
	runtimecfg.RecordWebSocketFrameReceived(ctx)

	if got, ok := runtimecfg.WebSocketStatsFromContext(ctx); ok || got != nil {
		t.Fatalf("WebSocketStatsFromContext without stats = (%v, %v), want nil false", got, ok)
	}

	var stats *runtimecfg.WebSocketStats
	if stats.Upgraded() {
		t.Fatal("nil stats should not be upgraded")
	}
	if stats.FramesSent() != 0 || stats.FramesReceived() != 0 {
		t.Fatalf("nil frame counts = sent %d received %d, want zero", stats.FramesSent(), stats.FramesReceived())
	}
}
