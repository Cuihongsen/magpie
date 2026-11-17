package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cooldownSetting(t *testing.T, p Provider, seconds int) Provider {
	t.Helper()
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"errorCooldownSeconds":%d}`, seconds)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertErrorCooldown(t *testing.T, p Provider, seconds int) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Seconds int `json:"errorCooldownSeconds"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Seconds != seconds {
		t.Fatalf("error cooldown = %d seconds, want %d", fields.Seconds, seconds)
	}
}

func TestErrorCooldownSavedAndLoaded(t *testing.T) {
	isolate(t)
	p := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example/v1", Key: "test-key", Models: []string{"m"}}
	for _, seconds := range []int{0, 1, 15, 3600, 0} {
		if err := Save(cooldownSetting(t, p, seconds)); err != nil {
			t.Fatal(err)
		}
		got, err := Find(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertErrorCooldown(t, *got, seconds)
		data, err := os.ReadFile(Path())
		if err != nil {
			t.Fatal(err)
		}
		var saved file
		if err := json.Unmarshal(data, &saved); err != nil || len(saved.Providers) != 1 {
			t.Fatalf("stored provider could not be decoded: %v", err)
		}
		assertErrorCooldown(t, saved.Providers[0], seconds)
		if seconds == 0 && strings.Contains(string(data), "errorCooldownSeconds") {
			t.Fatal("disabled cooldown should remain omitted")
		}
	}
}

func TestErrorCooldownRejectsInvalidWithoutWriting(t *testing.T) {
	isolate(t)
	p := cooldownSetting(t, Provider{ID: "relay", Chat: "https://relay.example/v1", Key: "test-key-do-not-print"}, 15)
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, seconds := range []int{-1, 3601, 2147483647} {
		err := Save(cooldownSetting(t, p, seconds))
		if err == nil || !strings.Contains(err.Error(), "3600") {
			t.Fatalf("invalid cooldown should report the range: %v", err)
		}
		if strings.Contains(err.Error(), p.Key) {
			t.Fatal("validation exposed the API key")
		}
		after, err := os.ReadFile(Path())
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("invalid cooldown changed providers.json")
		}
	}
}

func TestErrorCooldownReadLegacyAndInvalid(t *testing.T) {
	for _, fields := range []string{"", `,"errorCooldownSeconds":-1`, `,"errorCooldownSeconds":3601`} {
		t.Run(fields, func(t *testing.T) {
			isolate(t)
			if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			data := []byte(`{"providers":[{"id":"relay","chat":"https://relay.example/v1","key":"test-key"` + fields + `}]}`)
			if err := os.WriteFile(Path(), data, 0o600); err != nil {
				t.Fatal(err)
			}
			p, err := Find("relay")
			if err != nil {
				t.Fatal(err)
			}
			assertErrorCooldown(t, *p, 0)
			after, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(data) {
				t.Fatal("reading a provider rewrote its configuration")
			}
		})
	}
}

func TestErrorCooldownAccountSettings(t *testing.T) {
	signIn(t)
	for _, id := range []string{"codex", "copilot"} {
		for _, seconds := range []int{15, 3600, 0} {
			p, err := Find(id)
			if err != nil {
				t.Fatal(err)
			}
			if err := Save(cooldownSetting(t, *p, seconds)); err != nil {
				t.Fatal(err)
			}
			got, err := Find(id)
			if err != nil {
				t.Fatal(err)
			}
			if got.Account == nil {
				t.Fatal("saving cooldown lost the subscription account")
			}
			assertErrorCooldown(t, *got, seconds)
			assertErrorCooldown(t, withPicks(Provider{ID: id}, cooldownSetting(t, *got, seconds)), seconds)
			stored, err := read()
			if err != nil {
				t.Fatal(err)
			}
			saved, ok := find(stored.Providers, id)
			if !ok {
				t.Fatal("account settings were not persisted")
			}
			assertErrorCooldown(t, saved, seconds)
		}
	}
}
