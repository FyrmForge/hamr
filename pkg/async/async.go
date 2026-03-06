package async

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// All runs fns concurrently and returns the first error (or nil).
// First error cancels remaining work via a derived context.
// Functions capture output via closures to pre-declared outer variables.
// Panics are recovered and converted to errors.
func All(ctx context.Context, fns ...func(context.Context) error) error {
	if len(fns) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)

	wg.Add(len(fns))
	for i, fn := range fns {
		go func(i int, fn func(context.Context) error) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("async: panic in job %d: %v\n%s", i, r, debug.Stack())
					once.Do(func() { firstErr = err; cancel() })
				}
			}()

			if err := fn(ctx); err != nil {
				once.Do(func() { firstErr = err; cancel() })
			}
		}(i, fn)
	}

	wg.Wait()
	return firstErr
}

// Settle runs fns concurrently and returns every error.
// Never short-circuits — all functions run to completion.
// Functions capture output via closures to pre-declared outer variables.
// Panics are recovered and converted to errors.
func Settle(ctx context.Context, fns ...func(context.Context) error) []error {
	if len(fns) == 0 {
		return []error{}
	}

	errs := make([]error, len(fns))
	var wg sync.WaitGroup

	wg.Add(len(fns))
	for i, fn := range fns {
		go func(i int, fn func(context.Context) error) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs[i] = fmt.Errorf("async: panic in job %d: %v\n%s", i, r, debug.Stack())
				}
			}()

			errs[i] = fn(ctx)
		}(i, fn)
	}

	wg.Wait()
	return errs
}

// Map applies fn to every item concurrently and returns results in order.
// First error cancels remaining work.
// Panics are recovered and converted to errors.
func Map[T, R any](ctx context.Context, items []T, fn func(context.Context, T) (R, error)) ([]R, error) {
	if len(items) == 0 {
		return []R{}, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]R, len(items))
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)

	wg.Add(len(items))
	for i, item := range items {
		go func(i int, item T) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("async: panic in job %d: %v\n%s", i, r, debug.Stack())
					once.Do(func() { firstErr = err; cancel() })
				}
			}()

			v, err := fn(ctx, item)
			if err != nil {
				once.Do(func() { firstErr = err; cancel() })
				return
			}
			results[i] = v
		}(i, item)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
