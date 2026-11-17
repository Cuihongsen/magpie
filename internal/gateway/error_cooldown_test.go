package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestErrorCooldownFallsBackForEveryHTTPError(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 408, 409, 422, 429, 499, 500, 503, 599} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			fresh(t)
			first := &busyUpstream{replies: []busyReply{{code: status, body: `{"error":{"message":"no healthy deployments"}}`}}}
			second := &busyUpstream{replies: []busyReply{{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`}}}
			firstServer := httptest.NewServer(first)
			secondServer := httptest.NewServer(second)
			t.Cleanup(firstServer.Close)
			t.Cleanup(secondServer.Close)
			var configured provider.Provider
			if err := json.Unmarshal([]byte(`{"errorCooldownSeconds":15}`), &configured); err != nil {
				t.Fatal(err)
			}
			configured.ID, configured.Name, configured.Key = "first", "First", "k1"
			configured.Models, configured.Chat, configured.Patience = []string{"m"}, firstServer.URL+"/v1", -1
			if err := provider.Save(configured); err != nil {
				t.Fatal(err)
			}
			if err := provider.Save(provider.Provider{ID: "second", Name: "Second", Key: "k2", Models: []string{"m"}, Chat: secondServer.URL + "/v1"}); err != nil {
				t.Fatal(err)
			}
			if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/m", "second/m"}, Routing: provider.Ordered, Patience: -1}); err != nil {
				t.Fatal(err)
			}
			server := New()
			before := time.Now()
			code, body := postAs(t, server, "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
			if code != 200 || first.asked() != 1 || second.asked() != 1 {
				t.Fatalf("status %d, first=%d second=%d, body=%s", code, first.asked(), second.asked(), body)
			}
			route := server.trace.routes[len(server.trace.routes)-1]
			if len(route.Tries) < 1 || route.Tries[0].Rest == nil {
				t.Fatalf("HTTP %d: failed provider did not enter cooldown", status)
			}
			rest := route.Tries[0].Rest
			if rest.Until.Before(before.Add(15*time.Second)) || rest.Until.After(time.Now().Add(15*time.Second)) {
				t.Fatalf("HTTP %d: cooldown until %v is not 15 seconds", status, rest.Until)
			}
			code, body = postAs(t, server, "", `{"model":"group/g","messages":[{"role":"user","content":"another"}]}`)
			if code != 200 || first.asked() != 1 || second.asked() != 2 {
				t.Fatalf("HTTP %d: resting provider was retried early: status=%d first=%d second=%d body=%s", status, code, first.asked(), second.asked(), body)
			}
		})
	}
}

func TestErrorCooldownLastCandidateReturnsWithoutRepeatedAttempts(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{{code: http.StatusServiceUnavailable, body: `{"error":{"message":"unavailable"}}`}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	var configured provider.Provider
	if err := json.Unmarshal([]byte(`{"errorCooldownSeconds":15}`), &configured); err != nil {
		t.Fatal(err)
	}
	configured.ID, configured.Name, configured.Key = "first", "First", "k1"
	configured.Models, configured.Chat, configured.Patience = []string{"m"}, host.URL+"/v1", -1
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, _ := postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusServiceUnavailable || upstream.asked() != 1 {
		t.Fatalf("status=%d attempts=%d, want 503 and one attempt", code, upstream.asked())
	}
	route := server.trace.routes[len(server.trace.routes)-1]
	if len(route.Tries) != 1 || route.Tries[0].Rest == nil {
		t.Fatal("last candidate did not enter cooldown")
	}
	code, _ = postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"again"}]}`)
	if code != http.StatusServiceUnavailable || upstream.asked() != 1 {
		t.Fatalf("during cooldown: status=%d attempts=%d", code, upstream.asked())
	}
	restingUntil.Lock()
	restingUntil.m[errorCooldownKey("first")] = time.Now().Add(-time.Second)
	restingUntil.Unlock()
	code, _ = postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"recovered"}]}`)
	if code != http.StatusServiceUnavailable || upstream.asked() != 2 {
		t.Fatalf("after cooldown: status=%d attempts=%d", code, upstream.asked())
	}
}

func TestErrorCooldownFollowsCrazyRetryLimit(t *testing.T) {
	fresh(t)
	first := &busyUpstream{replies: []busyReply{{code: http.StatusTooManyRequests, body: `{"error":{"message":"rate limited"}}`}}}
	second := &busyUpstream{replies: []busyReply{{code: http.StatusOK, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`}}}
	firstServer := httptest.NewServer(first)
	secondServer := httptest.NewServer(second)
	t.Cleanup(firstServer.Close)
	t.Cleanup(secondServer.Close)
	configured := provider.Provider{
		ID: "first", Name: "First", Key: "k1", Models: []string{"m"}, Chat: firstServer.URL + "/v1",
		Patience: -1, CrazyRetry: true, CrazyRetryCount: 2, ErrorCooldownSeconds: 15,
	}
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "second", Name: "Second", Key: "k2", Models: []string{"m"}, Chat: secondServer.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/m", "second/m"}, Routing: provider.Ordered, Patience: -1}); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postAs(t, server, "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || first.asked() != 3 || second.asked() != 1 {
		t.Fatalf("status=%d first=%d second=%d body=%s", code, first.asked(), second.asked(), body)
	}
	route := server.trace.routes[len(server.trace.routes)-1]
	if len(route.Tries) < 4 || route.Tries[2].Rest == nil {
		t.Fatal("cooldown was not recorded after exhausting the 429 retries")
	}
	code, _ = postAs(t, server, "", `{"model":"group/g","messages":[{"role":"user","content":"again"}]}`)
	if code != 200 || first.asked() != 3 || second.asked() != 2 {
		t.Fatalf("during cooldown: status=%d first=%d second=%d", code, first.asked(), second.asked())
	}
}

func TestErrorCooldownCannotBeClearedByOlderSuccess(t *testing.T) {
	fresh(t)
	candidate := candidate{p: provider.Provider{ID: "first", ErrorCooldownSeconds: 15}, rest: "first", model: "m"}
	now := time.Now()
	rest := errorCooldownAfter(candidate, http.StatusUnauthorized, []byte(`{"error":"bad key"}`), now)
	served(candidate.rest, candidate.restKey(), 1)
	got, ok := errorCooldownOf("first")
	if !ok || !got.Until.Equal(rest.Until) {
		t.Fatalf("a concurrent success cleared the newer cooldown: got=%+v ok=%v", got, ok)
	}
}

func TestErrorCooldownKeepsSlowStartRest(t *testing.T) {
	fresh(t)
	candidate := candidate{p: provider.Provider{ID: "token-mixing", ErrorCooldownSeconds: 15}, rest: "token-mixing", model: "deepseek-v4.1-flash"}
	now := time.Now()
	restSlow(candidate, now)
	if _, ok := restSlow(candidate, now.Add(time.Millisecond)); !ok {
		t.Fatal("repeated slow starts should rest the model")
	}
	rest, ok := restingOf(candidate)
	if !ok || rest.Why != failSlow {
		t.Fatalf("slow-start rest missing with error cooldown enabled: %+v, %v", rest, ok)
	}
}

func TestErrorCooldownRetriesUnavailableDeployment(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{
		{code: 400, body: `{"error":{"message":"No healthy deployments"}}`},
		{code: 400, body: `{"error":{"message":"no healthy deployments"}}`},
		{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
	}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	configured := provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", Patience: -1, ErrorCooldownSeconds: 1}
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || upstream.asked() != 3 {
		t.Fatalf("status=%d attempts=%d body=%s, want 200 and 3 attempts", code, upstream.asked(), body)
	}
	route := server.trace.routes[len(server.trace.routes)-1]
	if len(route.Tries) != 3 || route.Tries[0].Again != 1000 || route.Tries[1].Again != 1000 {
		t.Fatalf("expected two 1 second waits, got %+v", route.Tries)
	}
}

func TestErrorCooldownUnavailableDeploymentEventuallyFailsAs503(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{{code: 400, body: `{"error":{"message":"No healthy deployments"}}`}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	configured := provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", Patience: -1, ErrorCooldownSeconds: 1}
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, _ := postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusServiceUnavailable || upstream.asked() != 3 {
		t.Fatalf("status=%d attempts=%d, want 503 and 3 attempts", code, upstream.asked())
	}
	if rest, ok := errorCooldownOf("first"); !ok || rest.Status != 400 {
		t.Fatalf("final upstream failure should be cooling: %+v, %v", rest, ok)
	}
}

func TestCrazyRetryHundredMillisecondsDespiteUpstreamHeader(t *testing.T) {
	fresh(t)
	var attempts atomic.Int32
	times := make(chan time.Time, 3)
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		times <- time.Now()
		writer.Header().Set("Content-Type", "application/json")
		if attempts.Add(1) < 3 {
			writer.Header().Set("Retry-After", "1")
			writer.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(writer, `{"error":{"message":"rate limited"}}`)
			return
		}
		fmt.Fprint(writer, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(host.Close)
	configured := provider.Provider{
		ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1",
		CrazyRetry: true, CrazyRetryCount: 2, ErrorCooldownSeconds: 15,
	}
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || attempts.Load() != 3 {
		t.Fatalf("status=%d attempts=%d body=%s", code, attempts.Load(), body)
	}
	first, second, third := <-times, <-times, <-times
	if second.Sub(first) < 90*time.Millisecond || third.Sub(second) < 90*time.Millisecond {
		t.Fatalf("requests were sent early: %v, %v", second.Sub(first), third.Sub(second))
	}
	route := server.trace.routes[len(server.trace.routes)-1]
	if len(route.Tries) != 3 || route.Tries[0].Again != 100 || route.Tries[1].Again != 100 {
		t.Fatalf("expected two 100 ms waits despite Retry-After: %+v", route.Tries)
	}
}

func TestErrorCooldownPreservesCombinedTokenFallback(t *testing.T) {
	fresh(t)
	failed := &combinedWindow{window: 500000, prompt: 500001}
	small := &combinedWindow{window: 1000000}
	good := &combinedWindow{window: 1000000}
	serveCombined(t, "combined", failed)
	serveCombined(t, "small", small)
	serveCombined(t, "good", good)
	selected, err := provider.Find("combined")
	if err != nil {
		t.Fatal(err)
	}
	selected.ErrorCooldownSeconds = 15
	if err := provider.Save(*selected); err != nil {
		t.Fatal(err)
	}
	for id, window := range map[string]int{"small": 300000, "good": 1000000} {
		selected, err := provider.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.SetContext(*selected, "m", window); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"combined/m", "small/m", "good/m"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postJSON(t, server, "/v1/responses", `{"model":"group/g","input":"hi","max_output_tokens":384000}`)
	if code != 200 || !strings.Contains(body, "hello") || len(small.calls()) != 0 || len(good.calls()) != 1 {
		t.Fatalf("status=%d small=%v good=%v body=%s", code, small.calls(), good.calls(), body)
	}
}

func TestErrorCooldownPreservesCombinedTokenRecovery(t *testing.T) {
	fresh(t)
	upstream := &combinedWindow{window: 500000, prompt: 157364}
	serveCombined(t, "combined", upstream)
	selected, err := provider.Find("combined")
	if err != nil {
		t.Fatal(err)
	}
	selected.ErrorCooldownSeconds = 15
	if err := provider.Save(*selected); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postJSON(t, server, "/v1/responses", `{"model":"combined/m","input":"hi","max_output_tokens":384000}`)
	if code != 200 || !strings.Contains(body, "hello") || len(upstream.calls()) != 2 {
		t.Fatalf("status=%d calls=%v body=%s", code, upstream.calls(), body)
	}
	if rest, ok := errorCooldownOf("combined"); ok {
		t.Fatalf("recovered request incorrectly cooled the provider: %+v", rest)
	}
}
