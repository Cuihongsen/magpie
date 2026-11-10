package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func retryProvider(t *testing.T, fields string) Provider {
	t.Helper()
	var p Provider
	if err := json.Unmarshal([]byte(`{"id":"relay","name":"Relay","chat":"https://relay.example/v1","key":"test-key","models":["m"]`+fields+`}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertCrazyRetry(t *testing.T, p Provider, on bool, count int) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		On    bool `json:"crazyRetry"`
		Count int  `json:"crazyRetryCount"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.On != on || fields.Count != count {
		t.Fatalf("crazy retry = %v/%d, want %v/%d", fields.On, fields.Count, on, count)
	}
}

func TestCrazyRetrySavedAndLoaded(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields string
		on     bool
		count  int
	}{
		{"legacy", "", false, 0},
		{"default", `,"crazyRetry":true`, true, 20},
		{"minimum", `,"crazyRetry":true,"crazyRetryCount":1`, true, 1},
		{"maximum", `,"crazyRetry":true,"crazyRetryCount":100`, true, 100},
		{"disabled keeps count", `,"crazyRetryCount":7`, false, 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolate(t)
			p := retryProvider(t, test.fields)
			if err := Save(p); err != nil {
				t.Fatal(err)
			}
			got, err := Find(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertCrazyRetry(t, *got, test.on, test.count)
			data, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			var saved file
			if err := json.Unmarshal(data, &saved); err != nil || len(saved.Providers) != 1 {
				t.Fatalf("stored providers could not be decoded: %v", err)
			}
			assertCrazyRetry(t, saved.Providers[0], test.on, test.count)
			if test.name == "legacy" && strings.Contains(string(data), "crazyRetry") {
				t.Fatal("legacy defaults should remain omitted")
			}
		})
	}
}

func TestCrazyRetryReadDefaults(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(`{"providers":[{"id":"relay","chat":"https://relay.example/v1","key":"test-key","crazyRetry":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	assertCrazyRetry(t, *p, true, 20)
}

func TestCrazyRetryRefusesInvalidCountsWithoutWriting(t *testing.T) {
	isolate(t)
	if err := Save(retryProvider(t, `,"crazyRetry":true,"crazyRetryCount":7`)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range []string{
		`,"crazyRetry":true,"crazyRetryCount":-1`,
		`,"crazyRetry":true,"crazyRetryCount":101`,
		`,"crazyRetryCount":-1`,
		`,"crazyRetryCount":101`,
	} {
		err := Save(retryProvider(t, fields))
		if err == nil || !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "100") {
			t.Fatalf("invalid count should report the allowed range: %v", err)
		}
		after, err := os.ReadFile(Path())
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("invalid count changed the saved configuration")
		}
	}
}

func TestCrazyRetryAccountSettings(t *testing.T) {
	p := retryProvider(t, `,"crazyRetry":true,"crazyRetryCount":7`)
	assertCrazyRetry(t, accountSettings(Provider{ID: "codex"}, p), true, 7)
	assertCrazyRetry(t, withPicks(Provider{ID: "codex"}, p), true, 7)
}

func TestCrazyRetryLimit(t *testing.T) {
	for _, test := range []struct {
		on    bool
		count int
		want  int
	}{
		{false, 0, 0},
		{false, 7, 0},
		{true, 0, 20},
		{true, 1, 1},
		{true, 7, 7},
		{true, 100, 100},
		{true, -1, 20},
		{true, 101, 20},
	} {
		p := Provider{CrazyRetry: test.on, CrazyRetryCount: test.count}
		limit := p.CrazyRetryLimit()
		if limit != test.want {
			t.Errorf("on=%v count=%d: limit=%d, want %d", test.on, test.count, limit, test.want)
		}
	}
}
