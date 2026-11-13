package provider

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// MemberOn reports whether the group's member id may be sent a request
// now: it has no window, or now is in it. The window is a TimeWindow, the
// same a rule holds by, so its days and zone are its own and "22:00 to
// 08:00" wraps across midnight. A window this magpie can't read is no
// window, so a bad value never takes a member out of reach.
func (g Group) MemberOn(id string, now time.Time) bool {
	w, ok := g.Windows[id]
	if !ok {
		return true
	}
	if _, ok := clockMinutes(w.From); !ok {
		return true
	}
	if _, ok := clockMinutes(w.To); !ok {
		return true
	}
	if _, ok := zoneOf(w.Zone); !ok {
		return true
	}
	return w.Holds(now)
}

// routesAt is routes at now: a member outside its window is left out as
// one switched off is.
func (g Group) routesAt(now time.Time) []string {
	if g.Routing == Manual {
		if p := g.Picked(); p != "" && g.MemberOn(p, now) {
			return []string{p}
		}
		return nil
	}
	return slices.DeleteFunc(slices.Clone(g.Members), func(id string) bool {
		return g.IsOff(id) || !g.MemberOn(id, now)
	})
}

// renamedWindows is windows with each member as to spells it now, those
// it gives "" dropped; nil when none is left.
func renamedWindows(windows map[string]TimeWindow, to func(string) string) map[string]TimeWindow {
	var out map[string]TimeWindow
	for m, w := range windows {
		if m = to(m); m == "" {
			continue
		}
		if out == nil {
			out = map[string]TimeWindow{}
		}
		out[m] = w
	}
	return out
}

// cleanWindows keeps g.Windows to its members as Members spells them,
// each a window it can read, put the one way; nil when none is left.
func cleanWindows(entries []Entry, g *Group) error {
	var out map[string]TimeWindow
	for m, w := range g.Windows {
		id, _ := memberFastIn(entries, strings.TrimSpace(m))
		id = cleanMember(entries, id)
		if !slices.Contains(g.Members, id) {
			continue
		}
		if err := w.clean(); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if out == nil {
			out = map[string]TimeWindow{}
		}
		out[id] = w
	}
	g.Windows = out
	return nil
}
