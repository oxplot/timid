package timid

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
)

// Component is an interface that defines a method to render a component.
type Component interface {
	// Render is called to render a component and its children.
	Render(ctx context.Context, children []*Node) *Node
}

// Fragment is a component that renders its children without a wrapper.
var Fragment Component = fragment{}

type fragment struct{}

func (f fragment) Render(ctx context.Context, children []*Node) *Node {
	return N(nil, children...)
}

// Node is a tree node that represents two types of nodes:
//  1. An unrendered node. An unrendered node return false for IsRendered.
//  2. A rendered node. A rendered node return true for IsRendered.
type Node struct {
	// Component and its props.
	Component Component
	// Key prop, used by the renderer to match up nodes across renders.
	Key string
	// Children prop, used by the renderer to render children.
	Children []*Node
	memoized bool

	Rendered RenderedNode
}

func MemoNode(n *Node) *Node {
	n.memoized = true
	return n
}

type RenderedNode struct {
	Children        []*Node
	childKeyToIndex map[string]int
	state           *nodeState
}

func (n *Node) IsRendered() bool {
	return n.Rendered.state != nil
}

// N is a short hand function to create a Node.
func N(c Component, children ...*Node) *Node {
	return &Node{Component: c, Children: children}
}

// NK is a short hand function to create a Node with a key.
func NK(c Component, key string, children ...*Node) *Node {
	return &Node{Component: c, Key: key, Children: children}
}

// Displayer is an interface that defines a method to display a Node tree.
type Displayer interface {
	// Display is called to display a Node tree on a host platform.
	Display(context.Context, *Node)
}

// contextKey is a type to define keys for context values.
type contextKey string

var (
	renderContextKey contextKey = "renderContext"
	dirtyChKey       contextKey = "dirtyCh"
	terminateKey     contextKey = "terminate"
)

// Run starts the render loop and will block.
// Canceling the context will stop the render loop.
func Run(ctx context.Context, root *Node, host Displayer) error {
	if ctx == nil {
		return errors.New("timid: nil context")
	}
	if root == nil {
		return errors.New("timid: nil root node")
	}
	root = &Node{
		Component: root.Component,
		// Root must have a key and it doesn't matter what it is.
		Key:      "root-key",
		Children: root.Children,
	}
	var renderedRoot *Node
	// The reason the we receive node state is to determine if we actually need to
	// trigger a re-render or not depending on whether the state object is
	// disposed. In the future where we only re-render the node that has changed,
	// this channel will most likely be a channel of nodes.
	termCh := make(chan struct{}, 1)
	dirtyCh := make(chan *Node, 10)
	ctx = context.WithValue(ctx, dirtyChKey, dirtyCh)
	ctx = context.WithValue(ctx, terminateKey, func() {
		select {
		case termCh <- struct{}{}:
		default:
		}
	})

	// Do the first render.
	renderedRoot = render(ctx, root, renderedRoot)
	if host != nil {
		host.Display(ctx, renderedRoot)
	}

	needRenderCh := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case dirtyNode := <-dirtyCh:
				if !dirtyNode.Rendered.state.disposed.Load() {
					select {
					case needRenderCh <- struct{}{}:
					default:
						// Don't block if there's already a need to render.
					}
				}
			}
		}
	}()

MainLoop:
	for {
		select {

		case <-ctx.Done():
			return context.Canceled

		case <-termCh:
			break MainLoop

		case <-needRenderCh:
			renderedRoot = render(ctx, root, renderedRoot)
			if host != nil {
				host.Display(ctx, renderedRoot)
			}
		}
	}

	// Unmount the rendered root node (it's rendered at least once).
	unmountTree(ctx, renderedRoot)

	return nil
}

// Terminate stops the render loop.
// Terminate must be called from within a loop or it will panic.
func Terminate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("timid: nil context")
	}
	terminate, ok := ctx.Value(terminateKey).(func())
	if !ok {
		return errors.New("timid: Terminate must be called within a render loop")
	}
	terminate()
	return nil
}

type renderContext struct {
	node       *Node
	state      *nodeState
	stateIndex int
}

// render renders a node and its children.
// node is the node to render. curRendered is the corresponding node in the
// currently rendered tree. If curRendered is nil, it means that the node is not
// in the currently rendered tree and a new state object should be created for
// the node.
// node cannot be nil.
func render(ctx context.Context, node *Node, curRendered *Node) *Node {
	// We either have the corresponding currently rendered node that's matched up
	// by the caller of this function. Either the caller is the Run function and
	// is calling render on the root node OR it's a recursive call from this
	// function that has matched up a currently rendered child node.
	// - If the root node is passed in, it's either nil on the first render, or
	// non-nil on subsequent ones.
	// - If a child node somewhere on the render tree is passed in, it's either
	//   nil if we couldn't match up a child with the same key and type in the
	//   currently rendered tree, or non-nil if we did.
	// - If a nil curRendered node is passed in, nil is passed in to the recursive
	//   calls.
	// - If curRendered node is not nil, we re-use its state object for the to be
	//   rendered node, thus preserving the state.

	if node == nil || node.Key == "" {
		panic("timid: node must not be nil and must have a key - bug in renderer")
	}

	// rendered is node that is rendered and has its state object and rendered
	// children set.
	rendered := &Node{
		Component: node.Component,
		Key:       node.Key,
		Children:  node.Children,
	}

	// List of children returned by component render OR Children prop of the node
	// with if component is nil. These will each be rendered recursively.
	var children []*Node

	var state *nodeState
	if curRendered == nil {
		// This is a new node.
		state = &nodeState{}
	} else {
		// node and curRendered must have the same key and type at this stage. If
		// not, something's messed up. This is ensured by the parent render call,
		// or the Run function.
		if node.Key != curRendered.Key || reflect.TypeOf(node.Component) != reflect.TypeOf(curRendered.Component) {
			panic("timid: type/key mismatch - bug in renderer")
		}
		state = curRendered.Rendered.state
	}
	// FIXME: This is a waste of memory if dealing with fragments. But for
	// simplicity of checking if a component is rendered, we need to set the
	// state.
	rendered.Rendered.state = state

	if node.Component == nil {
		// If the node has no component, it's a fragment node and we just render its
		// children prop.
		children = node.Children
	} else {
		renderContext := &renderContext{
			node: rendered,
		}
		// This allows State hook to access the state object of the node.
		withRenderContext := context.WithValue(ctx, renderContextKey, renderContext)

		sub := node.Component.Render(withRenderContext, node.Children)
		children = []*Node{sub}
	}

	// Remove nil children.
	children = slices.DeleteFunc(children, func(n *Node) bool {
		return n == nil
	})

	const timidGenKeyPrefix = "timid-gen-key-"

	// Assign keys to children who don't have one based on their index in the
	// list.
	for i, child := range children {
		if child.Key == "" {
			if len(children) > 1 {
				slog.Warn("timid: child with no key")
			}
			children[i].Key = timidGenKeyPrefix + strconv.Itoa(i)
		}
	}

	// Create a mapping of node keys to children indexes.
	// This allows us to match up nodes from the currently rendered tree with the
	// new children list in order to preserve node state across renders.
	ckti := make(map[string]int, len(node.Children))
	rendered.Rendered.childKeyToIndex = ckti
	for i, child := range children {
		if _, ok := ckti[child.Key]; ok {
			slog.Warn("timid: child with duplicate key", "key", child.Key)
			indexStr := timidGenKeyPrefix + strconv.Itoa(i)
			child.Key = indexStr
		}
		ckti[child.Key] = i
	}

	// Unmount all nodes in curRendered node that are not in the newly rendered
	// children list of the rendered node based on their keys and types.
	if curRendered != nil {
		for key, i := range curRendered.Rendered.childKeyToIndex {
			curChild := curRendered.Rendered.Children[i]
			childIdx, ok := rendered.Rendered.childKeyToIndex[key]
			if ok {
				child := children[childIdx]
				if reflect.TypeOf(curChild.Component) == reflect.TypeOf(child.Component) {
					continue
				}
			}
			// If the child is not in the new children list, we unmount the tree.
			unmountTree(ctx, curChild)
		}
	}

	// Render all children.
	renderedChildren := make([]*Node, len(children))
	// TODO: This loop can be parallelized.
	for key, i := range rendered.Rendered.childKeyToIndex {
		// If the child is not in the currently rendered tree, we pass in nil.
		// This is because we want to create a new state object for the child.
		// If the child is in the currently rendered tree, we pass in the currently
		// rendered child node so that we can re-use its state object.
		var curChild *Node
		if curRendered != nil {
			if i, ok := curRendered.Rendered.childKeyToIndex[key]; ok {
				curChildWithSameKey := curRendered.Rendered.Children[i]
				if reflect.TypeOf(curChildWithSameKey.Component) == reflect.TypeOf(children[i].Component) {
					curChild = curChildWithSameKey
				}
			}
		}
		renderedChildren[i] = render(ctx, children[i], curChild)
	}

	rendered.Rendered.Children = renderedChildren
	return rendered
}

// unmountTree unmounts a tree of nodes recursively.
// - It disposes of the state object of the nodes in the tree.
// - It calls lifecycle methods on the nodes in the tree.
func unmountTree(ctx context.Context, node *Node) {
	if node == nil || !node.IsRendered() {
		panic("timid: node to unmount must not be nil and must have been rendered - bug in renderer")
	}
	for _, child := range node.Rendered.Children {
		unmountTree(ctx, child)
	}
	// It's important to mark the state as disposed BEFORE the final render to ensure no
	// state changes in the final render cause a re-render.
	node.Rendered.state.disposed.Store(true)
	// Render the node one last time.
	// This is to allow the component to clean up any resources it might have
	// allocated.
	if node.Component != nil {
		renderContext := &renderContext{
			node: node,
		}
		withStateContext := context.WithValue(ctx, renderContextKey, renderContext)
		// We don't care about the return value.
		_ = node.Component.Render(withStateContext, node.Children)
	}
}
