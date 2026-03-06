package server

import "context"

// HookFunc is a lifecycle callback invoked at a specific server event.
type HookFunc func(ctx context.Context) error

// WithOnBeforeMigrate registers a hook that runs before database migration.
func WithOnBeforeMigrate(fn HookFunc) Option {
	return func(s *Server) { s.onBeforeMigrate = append(s.onBeforeMigrate, fn) }
}

// WithOnAfterMigrate registers a hook that runs after database migration.
func WithOnAfterMigrate(fn HookFunc) Option {
	return func(s *Server) { s.onAfterMigrate = append(s.onAfterMigrate, fn) }
}

// RunBeforeMigrate executes all registered before-migrate hooks in order.
// It stops on the first error.
func (s *Server) RunBeforeMigrate(ctx context.Context) error {
	return runHooks(ctx, s.onBeforeMigrate)
}

// RunAfterMigrate executes all registered after-migrate hooks in order.
// It stops on the first error.
func (s *Server) RunAfterMigrate(ctx context.Context) error {
	return runHooks(ctx, s.onAfterMigrate)
}

// runHooks executes hooks in order, stopping on the first error.
func runHooks(ctx context.Context, hooks []HookFunc) error {
	for _, fn := range hooks {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return nil
}
