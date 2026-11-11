package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderErrorCooldownSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string, status int) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
		if rec.Code != status {
			t.Fatalf("save: %d, want %d: %s", rec.Code, status, rec.Body)
		}
	}
	check := func(id string, seconds int) {
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
			Seconds *int `json:"errorCooldownSeconds"`
		}
		if err := json.Unmarshal(data, &shown); err != nil {
			t.Fatal(err)
		}
		if shown.Seconds == nil {
			t.Fatal("editor response is missing errorCooldownSeconds")
		}
		if *shown.Seconds != seconds {
			t.Fatalf("editor shows %d seconds, want %d", *shown.Seconds, seconds)
		}
	}
	const edit = `{"id":"relay","name":"Relay","chat":"http://127.0.0.1:1/v1","models":[],"headers":{}`
	post(edit+`,"new":true,"key":"test-key"}`, http.StatusOK)
	check("relay", 0)
	for _, scenario := range []struct {
		fields string
		want   int
	}{
		{`,"errorCooldownSeconds":15`, 15},
		{"", 15},
		{`,"errorCooldownSeconds":0`, 0},
		{`,"errorCooldownSeconds":1`, 1},
		{`,"errorCooldownSeconds":3600`, 3600},
		{`,"errorCooldownSeconds":15`, 15},
	} {
		post(edit+scenario.fields+`}`, http.StatusOK)
		check("relay", scenario.want)
	}
	for _, invalid := range []string{"-1", "3601", "1.5", `"invalid"`} {
		post(edit+`,"errorCooldownSeconds":`+invalid+`}`, http.StatusBadRequest)
		check("relay", 15)
	}
	post(`{"id":"inherited","name":"Inherited","chat":"http://127.0.0.1:1/v1","new":true,"copyOf":"relay","crazyRetry":false,"crazyRetryCount":20}`, http.StatusOK)
	check("inherited", 15)
	post(`{"id":"disabled","name":"Disabled","chat":"http://127.0.0.1:1/v1","new":true,"copyOf":"relay","errorCooldownSeconds":0}`, http.StatusOK)
	check("disabled", 0)
	check("relay", 15)
	post(`{"id":"deepseek","preset":"deepseek","key":"test-key","new":true,"errorCooldownSeconds":15}`, http.StatusOK)
	check("deepseek", 15)
	post(`{"id":"deepseek","preset":"deepseek","models":[]}`, http.StatusOK)
	check("deepseek", 15)
	post(`{"id":"deepseek","preset":"deepseek","models":[],"errorCooldownSeconds":0}`, http.StatusOK)
	check("deepseek", 0)
}
