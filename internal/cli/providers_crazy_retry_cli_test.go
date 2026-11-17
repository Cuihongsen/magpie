package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderCrazyRetryCLI(t *testing.T) {
	groupsHome(t)
	p := mustFind(t, "a")
	p.Key = "test-secret-crazy-retry-key"
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		pairs       []string
		state       string
		count       string
		storedCount int
	}{
		{nil, "off", "50", 0},
		{[]string{"crazy-retry=on"}, "on", "50", 50},
		{[]string{"crazy-retry-count=1"}, "on", "1", 1},
		{[]string{"crazy-retry-count=100"}, "on", "100", 100},
		{[]string{"crazy-retry=off"}, "off", "100", 100},
		{[]string{"crazy-retry-count=7"}, "off", "7", 7},
		{[]string{"crazy-retry-count=0"}, "off", "50", 0},
		{[]string{"crazy-retry=on"}, "on", "50", 50},
		{[]string{"crazy-retry-count=7"}, "on", "7", 7},
		{[]string{"crazy-retry-count=0"}, "on", "50", 50},
		{[]string{"crazy-retry-count=20"}, "on", "20", 20},
	} {
		if test.pairs != nil {
			out, err := printed(t, func() error {
				return providerCmd(append([]string{"provider", "set", "a"}, test.pairs...))
			})
			if err != nil {
				t.Fatalf("set %v: %v", test.pairs, err)
			}
			if strings.Contains(out, p.Key) {
				t.Fatal("set exposed the API key")
			}
		}
		beforeShow, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		out, err := printed(t, func() error { return providerCmd([]string{"provider", "a"}) })
		if err != nil {
			t.Fatal(err)
		}
		value := func(label string) string {
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == label {
					return fields[1]
				}
			}
			return ""
		}
		if value("crazy-retry") != test.state {
			t.Fatalf("show does not report crazy retry %s", test.state)
		}
		if value("crazy-retry-count") != test.count {
			t.Fatalf("show does not report retry count %s", test.count)
		}
		saved := mustFind(t, "a")
		if saved.CrazyRetry != (test.state == "on") || saved.CrazyRetryCount != test.storedCount {
			t.Fatal("saved retry settings do not match the requested state")
		}
		afterShow, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		if string(afterShow) != string(beforeShow) {
			t.Fatal("show changed the persisted retry settings")
		}
		if strings.Contains(out, p.Key) {
			t.Fatal("show exposed the API key")
		}
	}
}

func TestProviderCrazyRetryCLIRejectsInvalidValues(t *testing.T) {
	groupsHome(t)
	for _, pair := range []string{
		"crazy-retry=maybe", "crazy-retry=",
		"crazy-retry-count=-1", "crazy-retry-count=101", "crazy-retry-count=1.5",
		"crazy-retry-count=", "crazy-retry-count=999999999999999999999999",
		"crazy-retry-count=test-secret-do-not-print",
	} {
		before, err := os.ReadFile(provider.Path())
		if err != nil {
			t.Fatal(err)
		}
		err = providerCmd([]string{"provider", "set", "a", "name=Changed", pair})
		if err == nil {
			t.Fatalf("%s was accepted", pair)
		}
		want := "0..100"
		if strings.HasPrefix(pair, "crazy-retry=") {
			want = "on|off"
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid retry value should report %s: %v", want, err)
		}
		if strings.Contains(err.Error(), "test-secret-do-not-print") {
			t.Fatal("invalid retry value exposed a secret")
		}
		after, readErr := os.ReadFile(provider.Path())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Fatal("invalid settings changed providers.json")
		}
	}
}

func TestProviderCrazyRetryCountZeroPair(t *testing.T) {
	for _, on := range []bool{false, true} {
		p := provider.Provider{CrazyRetry: on, CrazyRetryCount: 7}
		if err := applyPairs(&p, []string{"crazy-retry-count=0"}); err != nil {
			t.Fatal(err)
		}
		if p.CrazyRetryCount != 0 || p.CrazyRetry != on {
			t.Fatal("reset should keep zero as unset without changing the retry switch")
		}
	}
}

func TestProviderAddCrazyRetryCLI(t *testing.T) {
	groupsHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer server.Close()
	out, err := printed(t, func() error {
		return providerCmd([]string{"provider", "add", "Relay", "url=" + server.URL + "/v1", "key=test-secret-relay", "models=m", "crazy-retry=on", "crazy-retry-count=7"})
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := mustFind(t, "relay")
	if !saved.CrazyRetry || saved.CrazyRetryCount != 7 {
		t.Fatal("add did not save the retry settings")
	}
	if strings.Contains(out, "test-secret-relay") {
		t.Fatal("add exposed the API key")
	}
}
