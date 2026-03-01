// Package bus defines interfaces for event-based inter-service communication.
//
// The bus provides a publish/subscribe abstraction that decouples services.
// For now only a no-op implementation is provided. Future implementations
// will include NATS and PG LISTEN/NOTIFY.
package bus

import "context"

// Publisher publishes events to a subject.
type Publisher interface {
	Publish(ctx context.Context, subject string, data any) error
}

// Subscriber subscribes to events on a subject.
type Subscriber interface {
	Subscribe(subject string, handler func(ctx context.Context, data []byte)) error
}
