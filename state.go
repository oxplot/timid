package timid

import (
	"context"
	"sync"
	"sync/atomic"
)

type nodeStateItem[T any] struct {
	value T
	// mu is a mutex to protect the value since the setter functions returned by
	// State hook can be called concurrently from multiple goroutines.
	mu sync.Mutex

	set     func(v T)
	setFunc func(func(T) T)
}

// nodeState is a struct that holds the state of a node.
type nodeState struct {
	items    []any
	disposed atomic.Bool
}

// State is a hook that allows a component to store state.
// The initial value is only used during the first render.
// The returned set function can be used to update the state.
// State is not safe for concurrent use.
func State[T any](ctx context.Context, initial T) (T, func(T), func(func(T) T)) {

	// Retrieve the node state from the context.
	// Node state is only exposed in the context during Component.Render call.
	renderCtx, ok := ctx.Value(renderContextKey).(*renderContext)
	if !ok {
		// Not much we can do here but to panic. We can't return any error or any
		// meaningful value.
		panic("timid: State must be called within a component's Render method.")
	}

	// Get the dirty channel from the context.
	dirtyCh, ok := ctx.Value(dirtyChKey).(chan *Node)
	if !ok {
		panic("timid: dirtyCh missing - State must be called within a render loop.")
	}

	node := renderCtx.node
	state := node.Rendered.state

	idx := renderCtx.stateIndex
	renderCtx.stateIndex += 1

	// If this is first time, store an initial copy
	if len(state.items) <= idx {
		newItem := &nodeStateItem[T]{
			value: initial,
		}
		newItem.set = func(v T) {
			newItem.mu.Lock()
			newItem.value = v
			newItem.mu.Unlock()
			dirtyCh <- node
		}
		newItem.setFunc = func(fn func(T) T) {
			newItem.mu.Lock()
			newItem.value = fn(newItem.value)
			newItem.mu.Unlock()
			dirtyCh <- node
		}
		state.items = append(state.items, newItem)
	}

	item, ok := state.items[idx].(*nodeStateItem[T])
	if !ok {
		// Not much we can do here but to panic. We can't return any error or any
		// meaningful value.
		panic("State hook must be called with the same type.")
	}
	item.mu.Lock()
	current := item.value
	item.mu.Unlock()

	return current, item.set, item.setFunc
}
