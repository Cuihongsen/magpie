package provider

import (
	"testing"
	"time"
)

func TestMemberWindowWrapsAcrossMidnight(t *testing.T) {
	g := Group{Members: []string{"a/m", "b/m"}, Windows: map[string]TimeWindow{
		"a/m": {From: "22:00", To: "08:00"},
		"b/m": {From: "16:00", To: "08:00"},
	}}
	cases := []struct {
		id   string
		when time.Time
		want bool
	}{
		{"a/m", at(1, 21, 59), false},
		{"a/m", at(1, 22, 0), true},
		{"a/m", at(2, 0, 0), true},
		{"a/m", at(2, 7, 59), true},
		{"a/m", at(2, 8, 0), false},
		{"a/m", at(2, 12, 0), false},
		{"b/m", at(1, 15, 59), false},
		{"b/m", at(1, 16, 0), true},
		{"b/m", at(2, 7, 59), true},
		{"b/m", at(2, 8, 0), false},
		// no days named: every day, the weekend too
		{"b/m", at(6, 10, 0), false},
		{"b/m", at(6, 17, 0), true},
	}
	for _, c := range cases {
		if got := g.MemberOn(c.id, c.when); got != c.want {
			t.Errorf("%s at %s: %v, want %v", c.id, c.when.Format("Mon 15:04"), got, c.want)
		}
	}
}

func TestRoutesAtLeavesOutMembersOutsideTheirWindow(t *testing.T) {
	g := Group{ID: "handover", Routing: Ordered, Members: []string{"a/m", "b/m", "c/m"},
		Windows: map[string]TimeWindow{
			"a/m": {From: "22:00", To: "08:00"},
			"b/m": {From: "16:00", To: "08:00"},
		}}
	cases := []struct {
		when time.Time
		want []string
	}{
		{at(1, 10, 0), []string{"c/m"}},
		{at(1, 17, 0), []string{"b/m", "c/m"}},
		{at(1, 23, 0), []string{"a/m", "b/m", "c/m"}},
	}
	for _, c := range cases {
		got := g.routesAt(c.when)
		if len(got) != len(c.want) {
			t.Errorf("at %s: %v, want %v", c.when.Format("15:04"), got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("at %s: %v, want %v", c.when.Format("15:04"), got, c.want)
				break
			}
		}
	}
}

func TestRoutesAtKeepsAWindowedMemberOffWhenSwitchedOff(t *testing.T) {
	g := Group{ID: "handover", Routing: Ordered, Members: []string{"a/m", "b/m"},
		Off:     []string{"a/m"},
		Windows: map[string]TimeWindow{"a/m": {From: "22:00", To: "08:00"}}}
	if got := g.routesAt(at(1, 23, 0)); len(got) != 1 || got[0] != "b/m" {
		t.Fatalf("%v: a member switched off is left out in its window too", got)
	}
}

func TestAnUnreadableWindowNeverTakesAMemberOut(t *testing.T) {
	g := Group{Members: []string{"a/m"}, Windows: map[string]TimeWindow{"a/m": {From: "25:00", To: "08:00"}}}
	if !g.MemberOn("a/m", at(1, 10, 0)) {
		t.Fatal("a window that can't be read should not take the member out of reach")
	}
}

func TestCleanWindowsRefusesAWindowItCannotRead(t *testing.T) {
	entries := []Entry{
		{ID: "a/m", Provider: Provider{ID: "a"}, Model: "m"},
		{ID: "b/m", Provider: Provider{ID: "b"}, Model: "m"},
	}
	g := Group{Members: []string{"a/m", "b/m"}, Windows: map[string]TimeWindow{"a/m": {From: "25:00", To: "08:00"}}}
	if err := cleanWindows(entries, &g); err == nil {
		t.Fatal("25:00 should be refused")
	}
	g = Group{Members: []string{"a/m", "b/m"}, Windows: map[string]TimeWindow{
		"a/m":    {From: "22:00", To: "08:00"},
		"gone/m": {From: "22:00", To: "08:00"},
	}}
	if err := cleanWindows(entries, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Windows) != 1 || g.Windows["a/m"].From != "22:00" {
		t.Fatalf("%v: only the group's own members keep a window", g.Windows)
	}
}
