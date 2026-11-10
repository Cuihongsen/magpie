package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/provider"
)

func combinedMessage(window, prompt, output int) string {
	return fmt.Sprintf("This model configuration accepts at most %d combined input and output tokens. However, your request has %d input tokens and asks for %d output tokens (%d tokens total). Please reduce the input length or requested output length.",
		window, prompt, output, prompt+output)
}

func TestCombinedTokenReplyLeft(t *testing.T) {
	message := combinedMessage(500000, 157364, 384000)
	for _, test := range []struct {
		name    string
		message string
		left    int
		long    bool
	}{
		{"reported", message, 342636, true},
		{"wrapped", "litellm.BadRequestError: OpenAIException - " + message + "No fallback model group found. Error doing the fallback: " + message, 342636, true},
		{"parameter", "max_tokens: " + message, 342636, true},
		{"minimum", combinedMessage(500000, 491808, 384000), 8192, true},
		{"below minimum", combinedMessage(500000, 491809, 384000), 8191, true},
		{"full", combinedMessage(500000, 500000, 384000), 0, true},
		{"overfull", combinedMessage(500000, 500001, 384000), 0, true},
		{"empty input", combinedMessage(500000, 0, 500001), 500000, true},
		{"fits", combinedMessage(500000, 157364, 32000), 0, false},
		{"inconsistent total", strings.ReplaceAll(message, "541364 tokens total", "1 tokens total"), 0, false},
		{"overflowing integer", strings.ReplaceAll(message, "500000 combined", "999999999999999999999999 combined"), 0, false},
		{"zero window", combinedMessage(0, 157364, 384000), 0, false},
		{"missing input", strings.ReplaceAll(message, "157364 input tokens", "unknown input tokens"), 0, false},
		{"output limit", "Invalid max_tokens value, the valid range of max_tokens is [1, 8192]", 0, false},
		{"rate", "Too many tokens per minute; please wait", 0, false},
		{"schema", "Invalid schema for function 'example'", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := replyLeft([]byte(test.message)); got != test.left {
				t.Errorf("replyLeft = %d, want %d", got, test.left)
			}
			if got := tooLong(400, test.message); got != test.long {
				t.Errorf("tooLong = %v, want %v", got, test.long)
			}
			if test.long && tooLong(429, test.message) {
				t.Error("429 was treated as context overflow")
			}
		})
	}
}

type combinedWindow struct {
	mu           sync.Mutex
	window       int
	prompt       int
	alwaysRefuse bool
	asked        []int
}

func (upstream *combinedWindow) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	output := int(max(gjson.GetBytes(body, "max_tokens").Int(), gjson.GetBytes(body, "max_completion_tokens").Int()))
	upstream.mu.Lock()
	upstream.asked = append(upstream.asked, output)
	upstream.mu.Unlock()
	prompt := upstream.prompt
	if upstream.alwaysRefuse && prompt+output <= upstream.window {
		prompt = upstream.window - output + 1
	}
	if prompt+output > upstream.window {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(400)
		message := combinedMessage(upstream.window, prompt, output)
		json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
			"message": "litellm.BadRequestError: OpenAIException - " + message +
				"No fallback model group found for original model_group=deepseek-v4.1-flash-b300-idle. Error doing the fallback: " + message,
			"type": "invalid_request_error",
		}})
		return
	}
	if gjson.GetBytes(body, "stream").Bool() {
		writer.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(writer, sse(
			`data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	io.WriteString(writer, chatOK)
}

func (upstream *combinedWindow) calls() []int {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	return append([]int(nil), upstream.asked...)
}

func serveCombined(t *testing.T, id string, upstream *combinedWindow) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	if err := provider.Save(provider.Provider{
		ID: id, Name: id, Key: "fixture-key", Models: []string{"m"},
		Chat: server.URL + "/v1", BaseAPI: string(provider.Chat),
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetModelOutput(id+"/m", 384000); err != nil {
		t.Fatal(err)
	}
}

func TestCombinedTokenRecoveryProtocols(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
		want []int
	}{
		{"chat", "/v1/chat/completions", `{"model":"combined/m","messages":[{"role":"user","content":"hi"}],"max_tokens":384000}`, []int{384000, 342636}},
		{"chat completion", "/v1/chat/completions", `{"model":"combined/m","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":384000}`, []int{384000, 342636}},
		{"responses", "/v1/responses", `{"model":"combined/m","input":"hi","max_output_tokens":384000}`, []int{384000, 342636}},
		{"responses stream", "/v1/responses", `{"model":"combined/m","stream":true,"input":"hi","max_output_tokens":384000}`, []int{384000, 342636}},
		{"responses default", "/v1/responses", `{"model":"combined/m","input":"hi"}`, []int{384000, 342636}},
		{"anthropic", "/v1/messages", `{"model":"combined/m","messages":[{"role":"user","content":"hi"}],"max_tokens":384000}`, []int{384000, 342636}},
		{"small explicit", "/v1/responses", `{"model":"combined/m","input":"hi","max_output_tokens":32000}`, []int{32000}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh(t)
			upstream := &combinedWindow{window: 500000, prompt: 157364}
			serveCombined(t, "combined", upstream)
			gateway := New()
			code, body := postJSON(t, gateway, test.path, test.body)
			if code != 200 || !strings.Contains(body, "hello") {
				t.Fatalf("HTTP %d: %s", code, body)
			}
			if got := upstream.calls(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("asked %v, want %v", got, test.want)
			}
			route := gateway.trace.routes[len(gateway.trace.routes)-1]
			for _, attempt := range route.Tries {
				if attempt.Rest != nil {
					t.Fatalf("healthy model rested: %+v", attempt.Rest)
				}
			}
			if len(test.want) == 2 && route.Tries[0].Fail != failRoom {
				t.Fatalf("first failure = %q, want %q", route.Tries[0].Fail, failRoom)
			}
		})
	}
}

func TestCombinedTokenRecoveryBoundaries(t *testing.T) {
	for _, test := range []struct {
		name         string
		prompt       int
		alwaysRefuse bool
		fallback     bool
		status       int
		calls        int
	}{
		{"minimum reply", 491808, false, false, 200, 2},
		{"too little room", 491809, false, false, 400, 1},
		{"input full", 500000, false, false, 400, 1},
		{"input too large", 500001, false, false, 400, 1},
		{"bounded retry", 157364, true, false, 400, 2},
		{"fallback after retry", 157364, true, true, 200, 2},
		{"fallback without room", 500001, false, true, 200, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fresh(t)
			upstream := &combinedWindow{window: 500000, prompt: test.prompt, alwaysRefuse: test.alwaysRefuse}
			serveCombined(t, "combined", upstream)
			members := []string{"combined/m"}
			fallback := &combinedWindow{window: 1000000}
			if test.fallback {
				serveCombined(t, "good", fallback)
				members = append(members, "good/m")
			}
			if err := provider.SaveGroup(provider.Group{Name: "G", Members: members, Routing: provider.Ordered}); err != nil {
				t.Fatal(err)
			}
			gateway := New()
			code, body := postJSON(t, gateway, "/v1/responses", `{"model":"group/g","input":"hi","max_output_tokens":384000}`)
			if code != test.status || len(upstream.calls()) != test.calls {
				t.Fatalf("HTTP %d, calls %v: %s", code, upstream.calls(), body)
			}
			if code == 400 && !strings.Contains(body, "context_length_exceeded") {
				t.Fatalf("no compaction signal: %s", body)
			}
			if test.fallback && len(fallback.calls()) != 1 {
				t.Fatalf("fallback calls = %v, want one", fallback.calls())
			}
			route := gateway.trace.routes[len(gateway.trace.routes)-1]
			for _, attempt := range route.Tries {
				if attempt.Rest != nil {
					t.Fatalf("context failure rested a model: %+v", attempt.Rest)
				}
			}
		})
	}
}

func TestCombinedTokenFallbackSkipsSmallerWindow(t *testing.T) {
	fresh(t)
	failed := &combinedWindow{window: 500000, prompt: 500001}
	small := &combinedWindow{window: 1000000}
	good := &combinedWindow{window: 1000000}
	serveCombined(t, "combined", failed)
	serveCombined(t, "small", small)
	serveCombined(t, "good", good)
	for id, window := range map[string]int{"small": 300000, "good": 1000000} {
		selected, err := provider.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.SetContext(*selected, "m", window); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.SaveGroup(provider.Group{
		Name: "G", Members: []string{"combined/m", "small/m", "good/m"}, Routing: provider.Ordered,
	}); err != nil {
		t.Fatal(err)
	}
	gateway := New()
	code, body := postJSON(t, gateway, "/v1/responses", `{"model":"group/g","input":"hi","max_output_tokens":384000}`)
	if code != 200 || !strings.Contains(body, "hello") || len(small.calls()) != 0 || len(good.calls()) != 1 {
		t.Fatalf("HTTP %d, small %v, good %v: %s", code, small.calls(), good.calls(), body)
	}
}

func TestCombinedTokenExhaustedProviderDoesNotRest(t *testing.T) {
	fresh(t)
	upstream := &combinedWindow{window: 500000, prompt: 500001}
	serveCombined(t, "combined", upstream)
	selected, err := provider.Find("combined")
	if err != nil {
		t.Fatal(err)
	}
	selected.Keys = []provider.KeyAccount{{Key: "k2"}}
	if err := provider.Save(*selected); err != nil {
		t.Fatal(err)
	}
	gateway := New()
	code, body := postJSON(t, gateway, "/v1/responses", `{"model":"combined/m","input":"hi","max_output_tokens":384000}`)
	if code != 400 || !strings.Contains(body, "context_length_exceeded") || len(upstream.calls()) != 1 {
		t.Fatalf("HTTP %d, calls %v: %s", code, upstream.calls(), body)
	}
	route := gateway.trace.routes[len(gateway.trace.routes)-1]
	if len(route.Tries) != 1 || route.Tries[0].Rest != nil {
		t.Fatalf("unusable siblings were tried or provider rested: %+v", route.Tries)
	}
}

func TestCombinedTokenRecoveryDoesNotPersistOutput(t *testing.T) {
	fresh(t)
	upstream := &combinedWindow{window: 500000, prompt: 157364}
	serveCombined(t, "combined", upstream)
	gateway := New()
	for round := 0; round < 2; round++ {
		code, body := postJSON(t, gateway, "/v1/responses", `{"model":"combined/m","input":"hi"}`)
		if code != 200 {
			t.Fatalf("round %d HTTP %d: %s", round, code, body)
		}
	}
	if got, want := upstream.calls(), []int{384000, 342636, 384000, 342636}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output allowances = %v, want %v", got, want)
	}
}
