package engine

import (
	"sync"

	"github.com/costinul/bwai/bwaiclient"
	"github.com/google/uuid"
)

// UsageRecorder is the seam future per-account billing implementations plug into.
// All recorders are driven from a single bwai logger so adding a new sink
// (e.g. Postgres-backed billing) requires no changes to engine or bwai call sites.
type UsageRecorder interface {
	RecordUsage(refID uuid.UUID, callType uint8, model string, inputTokens, outputTokens int)
}

// CompositeUsageLogger fans LogUsage out to every registered UsageRecorder.
// Implements bwaiclient.AIUssageLogger.
type CompositeUsageLogger struct {
	recorders []UsageRecorder
}

func NewCompositeUsageLogger(recorders ...UsageRecorder) *CompositeUsageLogger {
	return &CompositeUsageLogger{recorders: recorders}
}

func (c *CompositeUsageLogger) LogUsage(refID uuid.UUID, callType uint8, model string, in, out int) {
	for _, r := range c.recorders {
		r.RecordUsage(refID, callType, model, in, out)
	}
}

// trackerRegistry maps a call refID to the per-request CallTracker.
// The bwai AIUssageLogger callback does not receive context.Context, so we
// maintain this side table to route tokens to the right in-flight tracker.
type trackerRegistry struct {
	m sync.Map // uuid.UUID -> *CallTracker
}

func NewTrackerRegistry() *trackerRegistry {
	return &trackerRegistry{}
}

func (r *trackerRegistry) bind(refID uuid.UUID, t *CallTracker) {
	r.m.Store(refID, t)
}

func (r *trackerRegistry) unbind(refID uuid.UUID) {
	r.m.Delete(refID)
}

func (r *trackerRegistry) get(refID uuid.UUID) *CallTracker {
	if v, ok := r.m.Load(refID); ok {
		return v.(*CallTracker)
	}
	return nil
}

// trackerUsageRecorder routes LogUsage calls into the per-request CallTracker.
type trackerUsageRecorder struct {
	reg *trackerRegistry
}

func NewTrackerUsageRecorder(reg *trackerRegistry) *trackerUsageRecorder {
	return &trackerUsageRecorder{reg: reg}
}

func (t *trackerUsageRecorder) RecordUsage(refID uuid.UUID, _ uint8, model string, in, out int) {
	if tr := t.reg.get(refID); tr != nil {
		tr.addTokens(model, in, out)
	}
}

// compile-time check that CompositeUsageLogger satisfies the bwai interface.
var _ bwaiclient.AIUssageLogger = (*CompositeUsageLogger)(nil)
