package gui

import (
	"os"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// The Routing page is told a member's hours and whether they hold now, so
// the row says them without reading providers.json.
func TestGroupsStateSaysAMemberWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := provider.Save(provider.Provider{ID: "a", Name: "a", Key: "ka", Chat: "http://127.0.0.1:1/v1", Models: []string{"gpt-6-astra"}}); err != nil {
		t.Fatal(err)
	}
	g := provider.Group{ID: "hours", Name: "Hours", Members: []string{"a/gpt-6-astra"}, Routing: provider.Ordered,
		Windows: map[string]provider.TimeWindow{"a/gpt-6-astra": {From: "00:00", To: "00:00", Zone: "Asia/Shanghai"}}}
	if err := provider.SaveGroup(g); err != nil {
		t.Fatal(err)
	}
	for _, gj := range groupsState().Groups {
		if gj.ID != "hours" {
			continue
		}
		if len(gj.Info) != 1 || gj.Info[0].Window == nil {
			t.Fatalf("%+v: the member's window is not told", gj.Info)
		}
		w := gj.Info[0].Window
		if w.From != "00:00" || w.To != "00:00" || w.Zone != "Asia/Shanghai" || !w.Open {
			t.Fatalf("%+v: want 00:00-00:00 Asia/Shanghai open all day", w)
		}
		return
	}
	t.Fatal("group hours not in the state")
}
