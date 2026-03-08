package devserver

import (
	"context"
	"fmt"
	"sync"
)

// Graph tracks dependency relationships and coordinates execution order
// between watch rules using channels for signaling.
type Graph struct {
	mu    sync.Mutex
	nodes map[string]*graphNode
}

type graphNode struct {
	deps []string
	done chan struct{}
	// running tracks whether done is currently open.
	running bool
}

// NewGraph builds a dependency graph from the given watch rules.
// It assumes rules have already been validated (no cycles, no unknown deps).
func NewGraph(rules []WatchRule) *Graph {
	g := &Graph{
		nodes: make(map[string]*graphNode, len(rules)),
	}
	for _, r := range rules {
		done := make(chan struct{})
		close(done) // initially "done" so first run can proceed
		g.nodes[r.Name] = &graphNode{
			deps: append([]string(nil), r.Depends...),
			done: done,
		}
	}
	return g
}

// WaitForDeps blocks until all dependencies of the named rule have completed.
// Returns an error if the context is cancelled.
func (g *Graph) WaitForDeps(ctx context.Context, name string) error {
	g.mu.Lock()
	node, ok := g.nodes[name]
	if !ok {
		g.mu.Unlock()
		return fmt.Errorf("unknown rule %q", name)
	}

	// Snapshot the done channels while holding the lock.
	waitOn := make([]chan struct{}, 0, len(node.deps))
	for _, dep := range node.deps {
		if dn, exists := g.nodes[dep]; exists {
			waitOn = append(waitOn, dn.done)
		}
	}
	g.mu.Unlock()

	for _, ch := range waitOn {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// MarkRunning resets the named rule's done channel so dependees will block.
func (g *Graph) MarkRunning(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, ok := g.nodes[name]; ok {
		if node.running {
			return
		}
		node.done = make(chan struct{})
		node.running = true
	}
}

// MarkDone closes the named rule's done channel, unblocking all dependees.
func (g *Graph) MarkDone(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, ok := g.nodes[name]; ok {
		select {
		case <-node.done:
			// Already closed.
		default:
			close(node.done)
		}
		node.running = false
	}
}

// TopologicalOrder returns rule names in an order that respects dependencies
// (dependencies come before dependees).
func (g *Graph) TopologicalOrder() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	inDegree := make(map[string]int, len(g.nodes))
	dependees := make(map[string][]string)

	for name, node := range g.nodes {
		inDegree[name] = len(node.deps)
		for _, dep := range node.deps {
			dependees[dep] = append(dependees[dep], name)
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var order []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)

		for _, dep := range dependees[name] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	return order
}
