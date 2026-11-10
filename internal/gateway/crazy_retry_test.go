package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

func TestCrazyRetryProviderBeforeFallback(t *testing.T) {
	for _, test := range []struct {
		name       string
		config     string
		failures   int
		wantFirst  int
		wantSecond int
	}{
		{name: "disabled", config: `{}`, failures: 3, wantFirst: 1, wantSecond: 1},
		{name: "recovers", config: `{"crazyRetry":true}`, failures: 3, wantFirst: 4},
		{name: "default limit", config: `{"crazyRetry":true}`, failures: 30, wantFirst: 21, wantSecond: 1},
		{name: "configured limit", config: `{"crazyRetry":true,"crazyRetryCount":2}`, failures: 8, wantFirst: 3, wantSecond: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh(t)
			oldPause := retryPause
			retryPause = time.Millisecond
			t.Cleanup(func() { retryPause = oldPause })
			limited := busyReply{code: http.StatusTooManyRequests, body: `{"error":{"message":"rate limited"}}`}
			success := busyReply{code: http.StatusOK, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`}
			first := &busyUpstream{}
			for attempt := 0; attempt < test.failures; attempt++ {
				first.replies = append(first.replies, limited)
			}
			first.replies = append(first.replies, success)
			second := &busyUpstream{replies: []busyReply{success}}
			firstServer := httptest.NewServer(first)
			secondServer := httptest.NewServer(second)
			t.Cleanup(firstServer.Close)
			t.Cleanup(secondServer.Close)
			var firstProvider provider.Provider
			if err := json.Unmarshal([]byte(test.config), &firstProvider); err != nil {
				t.Fatal(err)
			}
			firstProvider.ID, firstProvider.Name, firstProvider.Key = "first", "First", "k1"
			firstProvider.Models, firstProvider.Chat, firstProvider.Patience = []string{"m"}, firstServer.URL+"/v1", -1
			if err := provider.Save(firstProvider); err != nil {
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
			if code != http.StatusOK {
				t.Fatalf("status %d: %s", code, body)
			}
			if first.asked() != test.wantFirst || second.asked() != test.wantSecond {
				t.Fatalf("first=%d second=%d, want %d/%d", first.asked(), second.asked(), test.wantFirst, test.wantSecond)
			}
		})
	}
}

func TestCrazyRetrySingleCandidateStopsAtLimit(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	upstream := &busyUpstream{replies: []busyReply{{code: http.StatusTooManyRequests, body: `{"error":{"message":"quota exceeded"}}`}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	var candidate provider.Provider
	if err := json.Unmarshal([]byte(`{"crazyRetry":true,"crazyRetryCount":2}`), &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.ID, candidate.Name, candidate.Key = "first", "First", "k1"
	candidate.Models, candidate.Chat = []string{"m"}, host.URL+"/v1"
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, _ := postAs(t, server, "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusTooManyRequests || upstream.asked() != 3 {
		t.Fatalf("status %d, attempts %d, want 429 and 3 attempts", code, upstream.asked())
	}
}

func TestCrazyRetryCancellationWhileWaiting(t *testing.T) {
	fresh(t)
	var attempts atomic.Int32
	replied := make(chan struct{}, 1)
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Retry-After", "30")
		writer.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(writer, `{"error":{"message":"rate limited"}}`)
		replied <- struct{}{}
	}))
	t.Cleanup(host.Close)
	var candidate provider.Provider
	if err := json.Unmarshal([]byte(`{"crazyRetry":true,"crazyRetryCount":2}`), &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.ID, candidate.Name, candidate.Key = "first", "First", "k1"
	candidate.Models, candidate.Chat = []string{"m"}, host.URL+"/v1"
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	server := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-replied:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not contacted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop after cancellation")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts after cancellation: %d", attempts.Load())
	}
}

func TestCrazyRetryWaitRespectsRetryAfter(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{header: "3", want: 3 * time.Second, ok: true},
		{header: now.Add(2 * time.Hour).UTC().Format(http.TimeFormat), ok: false},
		{header: "7200", ok: false},
	} {
		wait, ok := crazyRetryWait(http.Header{"Retry-After": {test.header}}, now)
		if wait != test.want || ok != test.ok {
			t.Fatalf("Retry-After %q: wait=%s ok=%v", test.header, wait, ok)
		}
	}
}

func TestCrazyRetryDoesNotRepeatStartedStream(t *testing.T) {
	fresh(t)
	first := sse(
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		`data: {"error":{"message":"`+carriedTPM+`","type":"tokens","code":"rate_limit_exceeded"}}`,
	)
	second := sse(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"duplicated"}}]}`, `data: [DONE]`)
	upstream := &busyUpstream{replies: []busyReply{{code: http.StatusOK, body: first}, {code: http.StatusOK, body: second}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	var candidate provider.Provider
	if err := json.Unmarshal([]byte(`{"crazyRetry":true}`), &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.ID, candidate.Name, candidate.Key = "first", "First", "k1"
	candidate.Models, candidate.Chat = []string{"m"}, host.URL+"/v1"
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	server := New()
	_, body := postAs(t, server, "", `{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if upstream.asked() != 1 || !strings.Contains(body, "partial") || strings.Contains(body, "duplicated") {
		t.Fatalf("attempts %d, stream %s", upstream.asked(), body)
	}
}

func TestCrazyRetryStreamErrorBeforeContent(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	first := sse(`data: {"error":{"code":429,"type":"rate_limit_error","message":"rate limited"}}`)
	second := sse(
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"unique"}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)
	upstream := &busyUpstream{replies: []busyReply{{code: http.StatusOK, body: first}, {code: http.StatusOK, body: second}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	candidate := provider.Provider{ID: "first", Name: "First", Key: "k1", Models: []string{"m"}, Chat: host.URL + "/v1", CrazyRetry: true}
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	server := New()
	code, body := postAs(t, server, "", `{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || upstream.asked() != 2 || strings.Count(body, "unique") != 1 {
		t.Fatalf("status %d, attempts %d, stream %s", code, upstream.asked(), body)
	}
}

func TestCrazyRetryGroupReplanKeepsProviderLimit(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	first := &busyUpstream{replies: []busyReply{{code: 429, body: `{"error":{"message":"rate limited"}}`}}}
	second := &busyUpstream{replies: []busyReply{{code: 503, body: `{"error":{"message":"temporarily unavailable"}}`}}}
	firstServer, secondServer := httptest.NewServer(first), httptest.NewServer(second)
	t.Cleanup(firstServer.Close)
	t.Cleanup(secondServer.Close)
	for _, candidate := range []provider.Provider{
		{ID: "first", Name: "First", Key: "k1", Models: []string{"m"}, Chat: firstServer.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2},
		{ID: "second", Name: "Second", Key: "k2", Models: []string{"m"}, Chat: secondServer.URL + "/v1"},
	} {
		if err := provider.Save(candidate); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/m", "second/m"}, Routing: provider.Ordered, Patience: 1}); err != nil {
		t.Fatal(err)
	}
	postAs(t, New(), "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if first.asked() != 3 {
		t.Fatalf("429 provider called %d times, want 3", first.asked())
	}
}

func TestCrazyRetryExhaustedLastStillReplansOthers(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	first := &busyUpstream{replies: []busyReply{
		{code: 503, body: `{"error":{"message":"temporarily unavailable"}}`},
		{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
	}}
	second := &busyUpstream{replies: []busyReply{{code: 429, body: `{"error":{"message":"rate limited"}}`}}}
	firstServer, secondServer := httptest.NewServer(first), httptest.NewServer(second)
	t.Cleanup(firstServer.Close)
	t.Cleanup(secondServer.Close)
	for _, candidate := range []provider.Provider{
		{ID: "first", Name: "First", Key: "k1", Models: []string{"m"}, Chat: firstServer.URL + "/v1"},
		{ID: "second", Name: "Second", Key: "k2", Models: []string{"m"}, Chat: secondServer.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2},
	} {
		if err := provider.Save(candidate); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/m", "second/m"}, Routing: provider.Ordered, Patience: 1}); err != nil {
		t.Fatal(err)
	}
	code, body := postAs(t, New(), "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || first.asked() != 2 || second.asked() != 3 {
		t.Fatalf("status %d, first %d, second %d: %s", code, first.asked(), second.asked(), body)
	}
}

func TestCrazyRetryBudgetSharedAcrossProviderKeys(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	first := &busyUpstream{replies: []busyReply{{code: 429, body: `{"error":{"message":"rate limited"}}`}}}
	second := &busyUpstream{replies: []busyReply{{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`}}}
	firstServer, secondServer := httptest.NewServer(first), httptest.NewServer(second)
	t.Cleanup(firstServer.Close)
	t.Cleanup(secondServer.Close)
	for _, candidate := range []provider.Provider{
		{ID: "first", Name: "First", Key: "k1", Keys: []provider.KeyAccount{{Key: "k2"}}, Models: []string{"m"}, Chat: firstServer.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2},
		{ID: "second", Name: "Second", Key: "k3", Models: []string{"m"}, Chat: secondServer.URL + "/v1"},
	} {
		if err := provider.Save(candidate); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/m", "second/m"}, Routing: provider.Ordered, Patience: -1}); err != nil {
		t.Fatal(err)
	}
	code, body := postAs(t, New(), "", `{"model":"group/g","messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK || first.asked() != 3 || second.asked() != 1 {
		t.Fatalf("status %d, first %d, second %d: %s", code, first.asked(), second.asked(), body)
	}
}

func TestCrazyRetryDoesNotReintroduceSmallModel(t *testing.T) {
	fresh(t)
	oldPause := retryPause
	retryPause = time.Millisecond
	t.Cleanup(func() { retryPause = oldPause })
	upstream := &busyUpstream{replies: []busyReply{{code: 429, body: `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota, please check your plan and billing details."}}`}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	candidate := provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"big", "small"}, Contexts: map[string]int{"big": 10000, "small": 2000}, Chat: host.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2}
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/big", "first/small"}, Routing: provider.Ordered, Patience: 1}); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"group/g","messages":[{"role":"user","content":"` + strings.Repeat("word ", 1760) + `"}]}`
	code, _ := postAs(t, New(), "", body)
	if code != http.StatusTooManyRequests || upstream.asked() != 3 {
		t.Fatalf("status %d, attempts %d, want 429 and 3 attempts", code, upstream.asked())
	}
}

func TestCrazyRetryStoppedLastPreservesFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		withSmall  bool
		retryAfter string
		wantCalls  int
	}{
		{name: "direct final response", retryAfter: "7200", wantCalls: 1},
		{name: "stopped final response", withSmall: true, retryAfter: "7200", wantCalls: 1},
		{name: "exhausted final response", withSmall: true, wantCalls: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh(t)
			oldPause := retryPause
			retryPause = time.Millisecond
			t.Cleanup(func() { retryPause = oldPause })
			upstream := &busyUpstream{replies: []busyReply{{
				code:       http.StatusTooManyRequests,
				retryAfter: test.retryAfter,
				body:       `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota, please check your plan and billing details."}}`,
			}}}
			host := httptest.NewServer(upstream)
			t.Cleanup(host.Close)
			candidate := provider.Provider{
				ID: "first", Name: "First", Key: "k", Models: []string{"big", "small"},
				Contexts: map[string]int{"big": 10000, "small": 2000},
				Chat:     host.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2,
			}
			if err := provider.Save(candidate); err != nil {
				t.Fatal(err)
			}
			members := []string{"first/big"}
			if test.withSmall {
				members = append(members, "first/small")
			}
			if err := provider.SaveGroup(provider.Group{Name: "G", Members: members, Routing: provider.Ordered, Patience: 1}); err != nil {
				t.Fatal(err)
			}
			server := New()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
				`{"model":"group/g","messages":[{"role":"user","content":"`+strings.Repeat("word ", 1760)+`"}]}`,
			))
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusTooManyRequests || upstream.asked() != test.wantCalls {
				t.Fatalf("status %d, attempts %d, want 429 and %d attempts", recorder.Code, upstream.asked(), test.wantCalls)
			}
			if test.retryAfter != "" && recorder.Header().Get("Retry-After") != test.retryAfter {
				t.Errorf("Retry-After %q, want %q", recorder.Header().Get("Retry-After"), test.retryAfter)
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != "insufficient_quota" {
				t.Errorf("error code %q, want insufficient_quota", response.Error.Code)
			}
			records := usage.Load(time.Time{})
			if len(records) != 1 {
				t.Fatalf("usage records %d, want one terminal failure", len(records))
			}
			record := records[0]
			if record.Status != http.StatusTooManyRequests || record.Provider != "first" || record.Model != "big" || record.HandedOn || !record.Failed() {
				t.Errorf("terminal usage record: %+v", record)
			}
			if test.wantCalls > 1 && record.Tries != test.wantCalls {
				t.Errorf("recorded tries %d, want %d", record.Tries, test.wantCalls)
			}
			_, totals, _ := usage.Ledger(usage.All, usage.Filter{})
			if totals.Calls != 1 || totals.Errors != 1 {
				t.Errorf("ledger calls=%d errors=%d, want 1/1", totals.Calls, totals.Errors)
			}
		})
	}
}

func TestCrazyRetryStoppedLastKeepsBilledFailureOnce(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{{
		code: http.StatusOK,
		body: anthropicStart + sse(`event: error`+"\n"+
			`data: {"type":"error","error":{"type":"rate_limit_error","message":"You exceeded your current quota, please check your plan and billing details."}}`),
	}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	candidate := provider.Provider{
		ID: "first", Name: "First", Key: "k", Models: []string{"big", "small"},
		Contexts:  map[string]int{"big": 10000, "small": 2000},
		Anthropic: host.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2,
	}
	if err := provider.Save(candidate); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{Name: "G", Members: []string{"first/big", "first/small"}, Routing: provider.Ordered, Patience: 1}); err != nil {
		t.Fatal(err)
	}
	server := New()
	_, reply := sendTo(server, "/v1/messages", `{"model":"group/g","stream":true,"max_tokens":8,"messages":[{"role":"user","content":"`+strings.Repeat("word ", 1760)+`"}]}`)
	if upstream.asked() != 3 || strings.Count(reply, "You exceeded your current quota") != 1 {
		t.Fatalf("attempts %d, reply %s", upstream.asked(), reply)
	}
	records := usage.Load(time.Time{})
	if len(records) != 3 {
		t.Fatalf("usage records %d, want two handed-on attempts and one terminal failure", len(records))
	}
	for index, record := range records {
		if record.Status != http.StatusTooManyRequests || record.Model != "big" || record.HandedOn != (index < 2) || record.Input != 3 || record.Output != 1 {
			t.Errorf("billed usage record %d: %+v", index, record)
		}
	}
	_, totals, _ := usage.Ledger(usage.All, usage.Filter{})
	if totals.Calls != 1 || totals.Errors != 1 || totals.Input != 9 || totals.Output != 3 {
		t.Errorf("billed terminal ledger totals: %+v", totals)
	}
}
