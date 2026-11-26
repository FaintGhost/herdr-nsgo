package main

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestSortSessionGroupsByActiveTimeAndManualOrder(t *testing.T) {
	ts := func(v uint64) *uint64 { return &v }
	gOld := SessionGroup{Primary: AggregatedTerminal{
		Key:      TermKey{InstanceID: "mac", Addr: "t1"},
		Terminal: Terminal{Address: "t1", Name: "oldest-active", CreatedMS: 1000, StatusMS: ts(2000)},
	}}
	gMid := SessionGroup{Primary: AggregatedTerminal{
		Key:      TermKey{InstanceID: "mac", Addr: "t2"},
		Terminal: Terminal{Address: "t2", Name: "mid-active", CreatedMS: 500, StatusMS: ts(5000)},
	}}
	gNew := SessionGroup{Primary: AggregatedTerminal{
		Key:      TermKey{InstanceID: "mac", Addr: "t3"},
		Terminal: Terminal{Address: "t3", Name: "newest-active", CreatedMS: 100, StatusMS: ts(9000)},
	}}
	in := []SessionGroup{gOld, gMid, gNew}

	newest := SortSessionGroups(in, SortActiveNewest, nil, nil)
	if newest[0].Primary.Key.Addr != "t3" || newest[1].Primary.Key.Addr != "t2" || newest[2].Primary.Key.Addr != "t1" {
		t.Fatalf("SortActiveNewest order = [%s, %s, %s], want [t3, t2, t1]",
			newest[0].Primary.Key.Addr, newest[1].Primary.Key.Addr, newest[2].Primary.Key.Addr)
	}

	oldest := SortSessionGroups(in, SortActiveOldest, nil, nil)
	if oldest[0].Primary.Key.Addr != "t1" || oldest[1].Primary.Key.Addr != "t2" || oldest[2].Primary.Key.Addr != "t3" {
		t.Fatalf("SortActiveOldest order = [%s, %s, %s], want [t1, t2, t3]",
			oldest[0].Primary.Key.Addr, oldest[1].Primary.Key.Addr, oldest[2].Primary.Key.Addr)
	}

	// Live activity timestamp overrides older StatusMS.
	live := map[TermKey]uint64{gOld.Primary.Key: 20000}
	liveNewest := SortSessionGroups(in, SortActiveNewest, nil, live)
	if liveNewest[0].Primary.Key.Addr != "t1" {
		t.Fatalf("expected live activity bump to put t1 first, got %s", liveNewest[0].Primary.Key.Addr)
	}

	// Manual drag reorder places t1 before t3.
	reordered := ReorderKeySlice([]string{"mac::t3", "mac::t2", "mac::t1"}, "mac::t1", "mac::t3", false)
	manual := SortSessionGroups(in, SortManual, reordered, nil)
	if manual[0].Primary.Key.Addr != "t1" || manual[1].Primary.Key.Addr != "t3" || manual[2].Primary.Key.Addr != "t2" {
		t.Fatalf("SortManual order = [%s, %s, %s], want [t1, t3, t2]",
			manual[0].Primary.Key.Addr, manual[1].Primary.Key.Addr, manual[2].Primary.Key.Addr)
	}
}

func TestHomeSortSheetAndReorderPersistence(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		app = a
		return root
	}, nsgotest.Size(390, 844))

	if app.SessionSort() != SortActiveNewest {
		t.Fatalf("expected default sort = SortActiveNewest, got %q", app.SessionSort())
	}

	h.Press("id:home-sort-btn")
	h.Settle()
	h.Find("id:home-sort-sheet")
	h.Press("id:sort-mode-oldest")
	h.Settle()
	if app.SessionSort() != SortActiveOldest {
		t.Fatalf("expected sort = SortActiveOldest, got %q", app.SessionSort())
	}
	h.Find("id:home-sort-pill")

	// Programmatic reorder (same path as drop handler) switches to SortManual and persists.
	app.ReorderSession("mac::t1", "kr::t7", []string{"kr::t7", "kr::t8", "mac::t1"}, false)
	h.Settle()
	if app.SessionSort() != SortManual {
		t.Fatalf("expected reorder to activate SortManual, got %q", app.SessionSort())
	}
	order := app.ManualSessionOrder()
	if len(order) < 3 || order[0] != "mac::t1" || order[1] != "kr::t7" {
		t.Fatalf("unexpected persisted manual order: %v", order)
	}
}
