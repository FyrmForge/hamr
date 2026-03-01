package bus

import "context"

// NoopPublisher is a Publisher that discards all events.
// Use it for testing or when the event bus isn't needed.
type NoopPublisher struct{}

// NewNoopPublisher returns a Publisher that silently discards events.
func NewNoopPublisher() *NoopPublisher {
	return &NoopPublisher{}
}

// Publish is a no-op. It always returns nil.
func (n *NoopPublisher) Publish(_ context.Context, _ string, _ any) error {
	return nil
}
