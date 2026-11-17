package gateway

import (
	"context"
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

func TestErrorCooldownRechecksQueuedDispatch(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{
		{code: 503, body: `{"error":{"message":"unavailable"}}`},
		{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
	}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	one := 1
	configured := provider.Provider{
		ID: "first", Name: "First", Key: "k", Keys: []provider.KeyAccount{{Key: "other"}},
		Models:        []string{"queued", "failing"},
		AccountModels: map[string][]string{provider.KeyID("k"): {"queued"}, provider.KeyID("other"): {"failing"}},
		Chat:          host.URL + "/v1", MaxConcurrency: &one, ErrorCooldownSeconds: 15,
	}
	if err := provider.Save(configured); err != nil {
		t.Fatal(err)
	}
	server := New()
	who := "first#" + provider.KeyID("k")
	release, ok := server.lanes.tryTake(who, 1)
	if !ok {
		t.Fatal("could not hold lane")
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"first/queued","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	go func() {
		server.Handler().ServeHTTP(recorder, request)
		close(done)
	}()
	within(t, "request queued", func() bool { return server.Lanes()[who].Waiting == 1 })
	code, reply := postAs(t, server, "", `{"model":"first/failing","messages":[{"role":"user","content":"hi"}]}`)
	if code != 503 || upstream.asked() != 1 {
		t.Fatalf("concurrent failure: status=%d calls=%d body=%s", code, upstream.asked(), reply)
	}
	if _, cooling := errorCooldownOf("first"); !cooling {
		t.Fatal("upstream failure did not start cooldown")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queued request did not finish")
	}
	if upstream.asked() != 1 || recorder.Code != 503 {
		t.Errorf("queued request dispatched during cooldown: status=%d upstream calls=%d", recorder.Code, upstream.asked())
	}
	if lane := server.Lanes()[who]; lane.Busy != 0 || lane.Waiting != 0 {
		t.Errorf("cooldown rejection leaked lane: %+v", lane)
	}
}

func TestErrorCooldownRejectionIsRecorded(t *testing.T) {
	fresh(t)
	upstream := &busyUpstream{replies: []busyReply{{code: 503, body: `{"error":{"message":"unavailable"}}`}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", ErrorCooldownSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	server := New()
	body := `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`
	for attempt := 0; attempt < 2; attempt++ {
		code, reply := postAs(t, server, "", body)
		if code != 503 {
			t.Fatalf("status %d: %s", code, reply)
		}
	}
	records := usage.Load(time.Time{})
	if upstream.asked() != 1 || len(server.Recent()) != 2 || len(records) != 2 {
		t.Fatalf("upstream=%d recent=%d usage=%d, want 1/2/2", upstream.asked(), len(server.Recent()), len(records))
	}
	rejected := 0
	for _, record := range records {
		if record.Rejected {
			rejected++
			if record.Status != 503 || record.RouteID == 0 {
				t.Errorf("cooldown rejection missing status or route: %+v", record)
			}
		}
	}
	if rejected != 1 {
		t.Errorf("rejected usage records=%d, want 1", rejected)
	}
}

func TestErrorCooldownAfterStreamContentDoesNotReplay(t *testing.T) {
	fresh(t)
	stream := sse(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		`data: {"error":{"code":503,"type":"server_error","message":"temporarily unavailable"}}`)
	upstream := &busyUpstream{replies: []busyReply{{code: 200, body: stream}}}
	host := httptest.NewServer(upstream)
	t.Cleanup(host.Close)
	if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", CrazyRetry: true, CrazyRetryCount: 2, ErrorCooldownSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	server := New()
	body := `{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	code, reply := postAs(t, server, "", body)
	if code != 200 || strings.Count(reply, "partial") != 1 || upstream.asked() != 1 {
		t.Fatalf("stream replayed: status=%d calls=%d body=%s", code, upstream.asked(), reply)
	}
	if _, cooling := errorCooldownOf("first"); !cooling {
		t.Error("late upstream 503 did not cool channel")
	}
	nextCode, _ := postAs(t, server, "", body)
	if nextCode != 503 || upstream.asked() != 1 {
		t.Errorf("next request dispatched after stream failure: status=%d calls=%d", nextCode, upstream.asked())
	}
}

func TestErrorCooldownIgnoresTransportFailure(t *testing.T) {
	fresh(t)
	var attempts atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		io.Copy(io.Discard, request.Body)
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	t.Cleanup(host.Close)
	if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", Patience: -1, ErrorCooldownSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	code, _ := postAs(t, New(), "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 502 || attempts.Load() == 0 {
		t.Fatalf("transport failure status=%d calls=%d", code, attempts.Load())
	}
	if rest, cooling := errorCooldownOf("first"); cooling {
		t.Errorf("gateway-generated 502 created upstream cooldown: %+v", rest)
	}
}

func TestErrorCooldownUnavailableRecoversWithMultipleKeys(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		name := "one key"
		if multiple {
			name = "two keys"
		}
		t.Run(name, func(t *testing.T) {
			fresh(t)
			upstream := &busyUpstream{replies: []busyReply{
				{code: 400, body: `{"error":{"message":"no healthy deployments"}}`},
				{code: 400, body: `{"error":{"message":"no healthy deployments"}}`},
				{code: 200, body: `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`},
			}}
			host := httptest.NewServer(upstream)
			t.Cleanup(host.Close)
			configured := provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", Routing: provider.Ordered, ErrorCooldownSeconds: 1}
			if multiple {
				configured.Keys = []provider.KeyAccount{{Key: "other"}}
			}
			if err := provider.Save(configured); err != nil {
				t.Fatal(err)
			}
			code, body := postAs(t, New(), "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
			if code != 200 || upstream.asked() != 3 {
				t.Errorf("recovery skipped: status=%d calls=%d body=%s", code, upstream.asked(), body)
			}
		})
	}
}

func TestErrorCooldownRealStreamFailuresAcrossProtocols(t *testing.T) {
	for _, upstreamProtocol := range []provider.Protocol{provider.Chat, provider.Responses, provider.Anthropic} {
		for _, translated := range []bool{false, true} {
			name := string(upstreamProtocol) + "/native"
			if translated {
				name = string(upstreamProtocol) + "/translated"
			}
			t.Run(name, func(t *testing.T) {
				fresh(t)
				stream := sse(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
					`data: {"error":{"code":503,"type":"server_error","message":"temporarily unavailable"}}`)
				switch upstreamProtocol {
				case provider.Responses:
					stream = sse(`event: response.created`+"\n"+`data: {"type":"response.created","response":{"id":"r1","status":"in_progress"}}`,
						`event: response.output_text.delta`+"\n"+`data: {"type":"response.output_text.delta","delta":"partial"}`,
						`event: response.failed`+"\n"+`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"temporarily unavailable"}}}`)
				case provider.Anthropic:
					stream = anthropicStart + sse(`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
						`event: error`+"\n"+`data: {"type":"error","error":{"type":"overloaded_error","message":"temporarily unavailable"}}`)
				}
				upstream := &busyUpstream{replies: []busyReply{{code: 200, body: stream}}}
				host := httptest.NewServer(upstream)
				t.Cleanup(host.Close)
				configured := provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, ErrorCooldownSeconds: 15}
				clientProtocol := upstreamProtocol
				switch upstreamProtocol {
				case provider.Chat:
					configured.Chat = host.URL + "/v1"
					if translated {
						clientProtocol = provider.Responses
					}
				case provider.Responses:
					configured.Responses = host.URL + "/v1"
				case provider.Anthropic:
					configured.Anthropic = host.URL
				}
				if translated && upstreamProtocol != provider.Chat {
					clientProtocol = provider.Chat
				}
				if err := provider.Save(configured); err != nil {
					t.Fatal(err)
				}
				path := "/v1/chat/completions"
				body := `{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
				switch clientProtocol {
				case provider.Responses:
					path, body = "/v1/responses", `{"model":"first/m","stream":true,"input":"hi"}`
				case provider.Anthropic:
					path, body = "/v1/messages", `{"model":"first/m","stream":true,"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
				}
				server := New()
				code, reply := sendTo(server, path, body)
				if code != 200 || !strings.Contains(reply, "partial") || upstream.asked() != 1 {
					t.Fatalf("failed stream replayed: status=%d calls=%d body=%s", code, upstream.asked(), reply)
				}
				if rest, cooling := errorCooldownOf("first"); !cooling || rest.Status < 500 {
					t.Errorf("real stream error missing cooldown: %+v, %v", rest, cooling)
				}
				code, _ = sendTo(server, path, body)
				if code != 503 || upstream.asked() != 1 {
					t.Errorf("stream failure allowed next request: status=%d calls=%d", code, upstream.asked())
				}
			})
		}
	}
}

func TestErrorCooldownClientCancellationDoesNotCool(t *testing.T) {
	fresh(t)
	contacted := make(chan struct{})
	var attempts atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		io.Copy(io.Discard, request.Body)
		if attempts.Add(1) == 1 {
			close(contacted)
		}
		<-request.Context().Done()
	}))
	t.Cleanup(host.Close)
	if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", ErrorCooldownSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	server := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx))
		close(done)
	}()
	select {
	case <-contacted:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream not contacted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop request")
	}
	if rest, cooling := errorCooldownOf("first"); cooling {
		t.Errorf("client cancellation cooled channel: %+v", rest)
	}
	if attempts.Load() != 1 {
		t.Errorf("canceled request replayed %d times", attempts.Load())
	}
	records := usage.Load(time.Time{})
	if len(records) != 1 || records[0].Status != 499 {
		t.Errorf("cancellation usage: %+v", records)
	}
}

func TestErrorCooldownDetectsStreamWithoutEventStreamHeader(t *testing.T) {
	for _, contentType := range []string{"text/plain", ""} {
		t.Run("content-type="+contentType, func(t *testing.T) {
			fresh(t)
			var attempts atomic.Int32
			host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				io.Copy(io.Discard, request.Body)
				attempts.Add(1)
				writer.Header()["Content-Type"] = []string{contentType}
				io.WriteString(writer, sse(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
					`data: {"error":{"code":503,"type":"server_error","message":"temporarily unavailable"}}`))
			}))
			t.Cleanup(host.Close)
			if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", ErrorCooldownSeconds: 15}); err != nil {
				t.Fatal(err)
			}
			server := New()
			body := `{"model":"first/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
			code, reply := postAs(t, server, "", body)
			if code != 200 || strings.Count(reply, "partial") != 1 {
				t.Fatalf("unexpected stream: status=%d body=%s", code, reply)
			}
			if rest, cooling := errorCooldownOf("first"); !cooling || rest.Status < 500 {
				t.Errorf("SSE prefix was not observed: %+v, %v", rest, cooling)
			}
			code, _ = postAs(t, server, "", body)
			if code != 503 || attempts.Load() != 1 {
				t.Errorf("untyped stream bypassed cooldown: status=%d attempts=%d", code, attempts.Load())
			}
		})
	}
}

func TestErrorCooldownIgnoresNotAPIReplyConversion(t *testing.T) {
	fresh(t)
	var attempts atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		io.Copy(io.Discard, request.Body)
		attempts.Add(1)
		writer.Header().Set("Content-Type", "text/html")
		io.WriteString(writer, cloudflarePage)
	}))
	t.Cleanup(host.Close)
	if err := provider.Save(provider.Provider{ID: "first", Name: "First", Key: "k", Models: []string{"m"}, Chat: host.URL + "/v1", Patience: -1, ErrorCooldownSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	code, _ := postAs(t, New(), "", `{"model":"first/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 502 || attempts.Load() == 0 {
		t.Fatalf("invalid API reply: status=%d attempts=%d", code, attempts.Load())
	}
	if rest, cooling := errorCooldownOf("first"); cooling {
		t.Errorf("converted HTTP 200 HTML caused upstream error cooldown: %+v", rest)
	}
}

func TestErrorCooldownIgnoresCachedRevokedReply(t *testing.T) {
	fresh(t)
	var attempts atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.WriteHeader(500)
	}))
	t.Cleanup(host.Close)
	account := &provider.Account{Agent: "review", User: "revoked@example.invalid"}
	configured := provider.Provider{ID: "first", Name: "First", Key: "revoked-review-token", Chat: host.URL + "/v1", Account: account, ErrorCooldownSeconds: 15}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	signed, err := http.NewRequest(http.MethodPost, host.URL+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := configured.Sign(context.Background(), signed, provider.Chat, body); err != nil {
		t.Fatal(err)
	}
	provider.NoteRevokedSignIn(context.Background(), account, signed)
	if _, marked := provider.RevokedSignIn(account, signed); !marked {
		t.Fatal("test credential was not marked revoked")
	}
	attempt := &errorCooldownAttempt{provider: configured.ID}
	ctx := context.WithValue(context.Background(), errorCooldownAttemptKey{}, attempt)
	response, err := New().forward(ctx, configured, provider.Chat, "/chat/completions", body, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 401 || attempts.Load() != 0 {
		t.Fatalf("cached revoked response: status=%d upstream=%d body=%s", response.StatusCode, attempts.Load(), reply)
	}
	if status, _, failed := attempt.failed(response.StatusCode, reply); failed {
		t.Errorf("cached local 401 recorded as upstream HTTP %d", status)
	}
}
