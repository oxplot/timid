package timid

import (
	"context"
	"slices"
)

type effectState struct {
	// We differentiate between the nil value and zero length slice.
	deps        []any
	lastCleanup func()
}

// Effect is a hook that runs the effect function when the component mounts and
// whenever the dependencies change. The effect function can return a cleanup
// function that will be called when the component unmounts or when the effect
// is re-run.
// nil dependencies will cause the effect to run on every render, first running
// the cleanup function.
func Effect(ctx context.Context, fn func() func(), deps []any) {

	if ctx == nil {
		panic("timid: Effect must be called with a non-nil context")
	}

	if fn == nil {
		panic("timid: Effect must be called with a non-nil effect function")
	}

	renderCtx, ok := ctx.Value(renderContextKey).(*renderContext)
	if !ok {
		panic("timid: Effect must be called within a component's Render method.")
	}

	isDisposed := renderCtx.node.Rendered.state.disposed.Load()
	if isDisposed {
		// If this is the last Render call for this component, pretend deps is nil
		// to force a cleanup.
		deps = nil
	}

	// FIXME: we allocate a new effectState on every render, which is not great.
	// Better to use another mechanism instead of State hook.
	// The reason we don't start with nil and use setState is because it will
	// cause a re-render on component mount.
	state, _, _ := State(ctx, &effectState{})

	if deps != nil && state.deps != nil && slices.Equal(state.deps, deps) {
		// Ignore if dependencies haven't changed.
		return
	}
	state.deps = slices.Clone(deps)

	if state.lastCleanup != nil {
		state.lastCleanup()
	}
	state.lastCleanup = fn()
}
