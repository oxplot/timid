package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oxplot/timid"
)

type Text string

func (t Text) Render(ctx context.Context, c []*timid.Node) *timid.Node {
	return timid.N(nil)
}

type ListItem struct {
	Color string
}

func (i ListItem) Render(ctx context.Context, children []*timid.Node) *timid.Node {
	return timid.N(
		Tag{
			Name:  "li",
			Attrs: map[string]string{"style": "color:" + i.Color},
		},
		children...,
	)
}

type List struct{}

func (l List) Render(ctx context.Context, children []*timid.Node) *timid.Node {
	return timid.N(Tag{Name: "ul"}, children...)
}

type Tag struct {
	Name  string
	Attrs map[string]string
}

func (t Tag) Render(ctx context.Context, children []*timid.Node) *timid.Node {
	return timid.N(nil, children...)
}

type App struct {
	Color string
	Count int
}

func (a App) Render(ctx context.Context, _ []*timid.Node) *timid.Node {

	count, _, setCount := timid.State(ctx, a.Count)

	timid.Effect(ctx, func() func() {
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(1 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-stop:
					return
				case <-t.C:
					setCount(func(v int) int {
						if v >= 10 {
							v = 0
						} else {
							v++
						}
						return v
					})
				}
			}
		}()
		return func() { close(stop) }
	}, []any{})

	children := make([]*timid.Node, count)
	for i := range children {
		children[i] = timid.N(
			ListItem{Color: a.Color},
			timid.N(Text(strconv.Itoa(i))),
		)
	}
	return timid.N(List{}, children...)
}

type displayer struct{}

func (d *displayer) Display(ctx context.Context, n *timid.Node) {
	fmt.Printf("\033[2J\x1B[H")
	display(n, 0)
}

func display(n *timid.Node, indent int) {
	switch c := n.Component.(type) {
	case Text:
		fmt.Printf("%s%s\n", strings.Repeat(" ", indent), c)
	case Tag:
		fmt.Printf("%s<%s", strings.Repeat(" ", indent), c.Name)
		for k, v := range c.Attrs {
			fmt.Printf(" %s=\"%s\"", k, v)
		}
		fmt.Println(">")
		for _, child := range n.Rendered.Children {
			display(child, indent+2)
		}
		if c, ok := n.Component.(Tag); ok {
			fmt.Printf("%s</%s>\n", strings.Repeat(" ", indent), c.Name)
		}
	default:
		for _, child := range n.Rendered.Children {
			display(child, indent)
		}
	}
}

func main() {
	go func() {
		for {
			time.Sleep(1 * time.Second)
		}
	}()
	ctx := context.Background()
	timid.Run(ctx, timid.N(App{Color: "red", Count: 0}), &displayer{})
}
