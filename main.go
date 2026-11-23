// Command herdr-nsgo is a small mobile client for herdr-core: it lists the
// terminals a core runs, shows one as text, sends into it, and separates the
// ones that wait on a person.
package main

import (
	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
)

// Route names one page of the stack.
type Route struct {
	Kind string // home, session, inbox, add
	Addr string
}

func main() { nsgo.Run(Root) }

// Root builds the navigation stack once; every page reads the same App.
func Root(cx *core.Scope) core.Node {
	app := NewApp(cx)
	nav := base.NewNav(cx, Route{Kind: "home"})
	return nav.Stack(
		core.Style{Width: core.Pct(100), Height: core.Pct(100)},
		func(cx *core.Scope, r Route, depth func() int) base.NavPage {
			switch r.Kind {
			case "session":
				return app.sessionPage(cx, nav, r.Addr)
			case "inbox":
				return app.inboxPage(cx, nav)
			case "add":
				return app.addPage(cx, nav)
			default:
				return app.homePage(cx, nav)
			}
		},
	)
}
