package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderErrorCooldownCLI(t *testing.T) {
	groupsHome(t)
	p := mustFind(t, "a")
	p.Key = "test-key-do-not-print"
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		value   string
		shown   string
		seconds int
	}{
		{"", "off", 0},
		{"15s", "15s", 15},
		{"1s", "1s", 1},
		{"1m", "60s", 60},
		{"1h", "3600s", 3600},
		{"off", "off", 0},
	} {
		if test.value != "" {
			out, err := printed(t, func() error {
				return providerCmd([]string{"provider", "set", "a", "error-cooldown=" + test.value})
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, p.Key) {
				t.Fatal("set exposed the API key")
			}
		}
		before, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		out, err := printed(t, func() error { return providerCmd([]string{"provider", "a"}) })
		if err != nil {
			t.Fatal(err)
		}
		value := ""
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "error-cooldown" {
				value = fields[1]
			}
		}
		if value != test.shown {
			t.Fatalf("show reports error cooldown %q, want %q", value, test.shown)
		}
		if strings.Contains(out, p.Key) {
			t.Fatal("show exposed the API key")
		}
		data, err := json.Marshal(mustFind(t, "a"))
		if err != nil {
			t.Fatal(err)
		}
		var saved struct {
			Seconds int `json:"errorCooldownSeconds"`
		}
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Seconds != test.seconds {
			t.Fatalf("saved cooldown = %d, want %d", saved.Seconds, test.seconds)
		}
		after, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("show changed the saved configuration")
		}
	}
}

func TestProviderErrorCooldownCLIRejectsInvalid(t *testing.T) {
	groupsHome(t)
	if _, err := printed(t, func() error {
		return providerCmd([]string{"provider", "set", "a", "error-cooldown=15s"})
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "-1s", "0s", "0.5s", "1.5s", "3601s", "2h", "15", "999999999999999999999999s", "test-secret-do-not-print"} {
		before, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		err = providerCmd([]string{"provider", "set", "a", "name=Changed", "error-cooldown=" + value})
		if err == nil || !strings.Contains(err.Error(), "error-cooldown") || !strings.Contains(err.Error(), "3600") {
			t.Fatalf("invalid cooldown should report the allowed range: %v", err)
		}
		if strings.Contains(err.Error(), "test-secret-do-not-print") {
			t.Fatal("validation exposed the supplied value")
		}
		after, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("invalid settings changed providers.json")
		}
	}
}
