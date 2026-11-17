package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

func TestTokenMaxingRateCooldown(t *testing.T) {
	fresh(t)
	server := &Server{}
	target := candidate{p: provider.Provider{ID: "token-mixing"}, model: "deepseek-v4.1-flash", rest: "token-mixing#test"}
	body := []byte(`{"error":"maas_qos","reason":"offline_limit"}`)
	for attempt := 0; attempt < 3; attempt++ {
		rest := server.restAfter(target, http.StatusTooManyRequests, http.Header{}, body)
		if remaining := time.Until(rest.Until); rest.Why != failRate || remaining > 15*time.Second || remaining < 14*time.Second {
			t.Fatalf("attempt %d: rest %s, reason %v", attempt+1, remaining, rest.Why)
		}
		restingUntil.Lock()
		restingUntil.m[target.restKey()] = time.Now().Add(-time.Second)
		restingUntil.Unlock()
	}
	servedCandidate(target, 1)
	headers := http.Header{"Retry-After": []string{"120"}}
	rest := server.restAfter(target, http.StatusTooManyRequests, headers, body)
	if remaining := time.Until(rest.Until); remaining > 120*time.Second || remaining < 119*time.Second || rest.By != "retry-after" {
		t.Fatalf("Retry-After: rest %s by %s", remaining, rest.By)
	}
}

func TestTokenMaxingCooldownDoesNotChangeOtherCandidates(t *testing.T) {
	fresh(t)
	server := &Server{}
	body := []byte(`{"error":"maas_qos","reason":"offline_limit"}`)
	for _, candidate := range []candidate{
		{p: provider.Provider{ID: "token-mixing"}, model: "other-model", rest: "token-mixing#other"},
		{p: provider.Provider{ID: "other"}, model: "deepseek-v4.1-flash", rest: "other#test"},
	} {
		rest := server.restAfter(candidate, http.StatusTooManyRequests, http.Header{}, body)
		if remaining := time.Until(rest.Until); remaining > time.Minute || remaining < 59*time.Second {
			t.Fatalf("%s/%s: rest %s", candidate.p.ID, candidate.model, remaining)
		}
	}
}

func TestTokenMaxingSlowCooldown(t *testing.T) {
	fresh(t)
	target := candidate{p: provider.Provider{ID: "token-mixing"}, model: "deepseek-v4.1-flash", rest: "token-mixing#slow"}
	now := time.Now()
	restSlow(target, now)
	for attempt := 0; attempt < 2; attempt++ {
		at := now.Add(time.Duration(attempt+1) * time.Second)
		rest, ok := restSlow(target, at)
		if !ok || rest.Until.Sub(at) != 15*time.Second {
			t.Fatalf("attempt %d: rest %+v", attempt+1, rest)
		}
		restingUntil.Lock()
		restingUntil.m[target.restID()] = at.Add(-time.Second)
		restingUntil.Unlock()
	}
}

func TestTokenMaxingRateCooldownOnGroupRoute(t *testing.T) {
	fresh(t)
	upstream := &keyed{fail: map[string]int{"k1": http.StatusTooManyRequests}}
	serveOn(t, "token-mixing", "k1", []string{"deepseek-v4.1-flash"}, upstream)
	serveOn(t, "fallback", "k2", []string{"other"}, upstream)
	if err := provider.SaveGroup(provider.Group{
		Name: "G", Members: []string{"token-mixing/deepseek-v4.1-flash", "fallback/other"}, Routing: provider.Ordered,
	}); err != nil {
		t.Fatal(err)
	}
	server := New()
	request := `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`
	code, _ := postAs(t, server, "", request)
	if code != http.StatusOK {
		t.Fatalf("first request returned %d", code)
	}
	route := server.trace.routes[len(server.trace.routes)-1]
	rest := route.Tries[0].Rest
	if rest == nil || time.Until(rest.Until) > 15*time.Second || time.Until(rest.Until) < 14*time.Second {
		t.Fatalf("first 429 rest: %+v", rest)
	}
	upstream.tried = nil
	postAs(t, server, "", request)
	if len(upstream.tried) == 0 || upstream.tried[0] != "k2" {
		t.Fatalf("route during cooldown: %v", upstream.tried)
	}
	restingUntil.Lock()
	restingUntil.m[rest.Key] = time.Now().Add(-time.Second)
	restingUntil.Unlock()
	delete(upstream.fail, "k1")
	upstream.tried = nil
	postAs(t, server, "", request)
	if len(upstream.tried) == 0 || upstream.tried[0] != "k1" {
		t.Fatalf("route after cooldown: %v", upstream.tried)
	}
}
