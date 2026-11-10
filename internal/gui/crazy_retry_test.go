package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderEditorCrazyRetry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
	}
	check := func(id string, on bool, count int) {
		t.Helper()
		saved, err := provider.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(providerInfoWith(*saved, provider.Listing{}, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		var shown struct {
			On    *bool `json:"crazyRetry"`
			Count *int  `json:"crazyRetryCount"`
		}
		if err := json.Unmarshal(data, &shown); err != nil {
			t.Fatal(err)
		}
		if shown.On == nil || shown.Count == nil {
			t.Fatalf("editor response is missing crazyRetry or crazyRetryCount")
		}
		if *shown.On != on || *shown.Count != count {
			t.Fatalf("editor shows %v/%d, want %v/%d", *shown.On, *shown.Count, on, count)
		}
	}
	const edit = `{"id":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":[],"headers":{}`
	post(edit + `,"new":true,"key":"test-key"}`)
	check("relay", false, 20)
	for _, step := range []struct {
		fields string
		on     bool
		count  int
	}{
		{`,"crazyRetry":true,"crazyRetryCount":7`, true, 7},
		{"", true, 7},
		{`,"crazyRetry":false`, false, 7},
		{`,"crazyRetryCount":1`, false, 1},
		{`,"crazyRetry":true,"crazyRetryCount":0`, true, 20},
		{`,"crazyRetryCount":100`, true, 100},
		{`,"crazyRetry":false,"crazyRetryCount":20`, false, 20},
	} {
		post(edit + step.fields + `}`)
		check("relay", step.on, step.count)
	}
	post(`{"id":"copy","name":"Copy","chat":"http://127.0.0.1:1/v1","new":true,"copyOf":"relay","crazyRetry":true,"crazyRetryCount":9}`)
	check("copy", true, 9)
	post(`{"id":"deepseek","preset":"deepseek","key":"test-key","new":true,"crazyRetry":true,"crazyRetryCount":8}`)
	check("deepseek", true, 8)
	post(`{"id":"deepseek","preset":"deepseek","models":[]}`)
	check("deepseek", true, 8)
}

func TestProviderCopyCrazyRetryInheritance(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		fields string
		on     bool
		count  int
	}{
		{"omitted", "", true, 7},
		{"explicit off", `,"crazyRetry":false`, false, 7},
		{"explicit default", `,"crazyRetryCount":0`, true, 20},
		{"explicit off and default", `,"crazyRetry":false,"crazyRetryCount":0`, false, 0},
		{"explicit settings", `,"crazyRetry":true,"crazyRetryCount":9`, true, 9},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			source := provider.Provider{
				ID: "source", Name: "Source", Chat: "http://127.0.0.1:1/v1", Key: "test-key",
				CrazyRetry: true, CrazyRetryCount: 7,
			}
			if err := provider.Save(source); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			providerRoutes(mux, nil)
			rec := httptest.NewRecorder()
			body := `{"id":"copy","name":"Copy","chat":"http://127.0.0.1:1/v1","new":true,"copyOf":"source"` + scenario.fields + `}`
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
			if rec.Code != http.StatusOK {
				t.Fatalf("copy: %d %s", rec.Code, rec.Body)
			}
			copied, err := provider.Find("copy")
			if err != nil {
				t.Fatal(err)
			}
			if copied.CrazyRetry != scenario.on || copied.CrazyRetryCount != scenario.count {
				t.Fatalf("copy has %v/%d, want %v/%d", copied.CrazyRetry, copied.CrazyRetryCount, scenario.on, scenario.count)
			}
			unchanged, err := provider.Find("source")
			if err != nil || unchanged.CrazyRetry != true || unchanged.CrazyRetryCount != 7 {
				t.Fatalf("copy changed the source: %v", err)
			}
		})
	}
}
