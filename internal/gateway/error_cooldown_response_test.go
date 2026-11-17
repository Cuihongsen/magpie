package gateway

import (
	"context"
	"errors"
	"fmt"
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

func cooldownZhipuTransport(outcome string, cancel context.CancelFunc, calls *atomic.Int32) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "open.bigmodel.cn" {
			return nil, fmt.Errorf("unexpected host: %s", request.URL.Host)
		}
		io.Copy(io.Discard, request.Body)
		calls.Add(1)
		status, body, requestID, contentType := 429, zhipuNoBalance, "payg", "application/json"
		switch request.URL.Path {
		case "/api/paas/v4/chat/completions":
		case "/api/coding/paas/v4/chat/completions":
			requestID = "coding"
			switch outcome {
			case "connection":
				return nil, errors.New("local Coding Plan connection failure")
			case "cancel":
				cancel()
				return nil, request.Context().Err()
			case "denied":
				status, body = 403, `{"error":{"message":"Coding Plan denied"}}`
			case "html":
				status, body, contentType = 200, cloudflarePage, "text/html"
			case "success":
				status, body = 200, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
			default:
				return nil, fmt.Errorf("unexpected outcome: %s", outcome)
			}
		default:
			return nil, fmt.Errorf("unexpected path: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {requestID}},
			Body:   io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
}

func TestErrorCooldownTracksFinalZhipuResponse(t *testing.T) {
	for _, test := range []struct {
		outcome     string
		wantStatus  int
		wantSource  int
		wantRequest string
	}{
		{outcome: "connection", wantStatus: 429, wantSource: 429, wantRequest: "payg"},
		{outcome: "denied", wantStatus: 429, wantSource: 429, wantRequest: "payg"},
		{outcome: "html", wantStatus: 502, wantRequest: "coding"},
		{outcome: "success", wantStatus: 200, wantRequest: "coding"},
	} {
		t.Run(test.outcome, func(t *testing.T) {
			fresh(t)
			configured := provider.Provider{ID: "first", Name: "First", Key: "k", Chat: "https://open.bigmodel.cn/api/paas/v4", ErrorCooldownSeconds: 15}
			var calls atomic.Int32
			server := New()
			server.client = &http.Client{Transport: cooldownZhipuTransport(test.outcome, nil, &calls)}
			attempt := &errorCooldownAttempt{provider: configured.ID}
			ctx := context.WithValue(context.Background(), errorCooldownAttemptKey{}, attempt)
			response, err := server.forward(ctx, configured, provider.Chat, "/chat/completions",
				[]byte(`{"model":"glm-5.1","messages":[{"role":"user","content":"hi"}]}`), http.Header{})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 || response.StatusCode != test.wantStatus || response.Header.Get("X-Request-Id") != test.wantRequest {
				t.Fatalf("calls=%d status=%d request=%q body=%s", calls.Load(), response.StatusCode, response.Header.Get("X-Request-Id"), body)
			}
			sourceStatus, _, failed := attempt.failed(0, nil)
			if test.wantSource == 0 {
				if failed {
					t.Errorf("non-error source associated with old HTTP %d", sourceStatus)
				}
			} else if !failed || sourceStatus != test.wantSource {
				t.Errorf("final response source=%d failed=%v, want %d", sourceStatus, failed, test.wantSource)
			}
		})
	}
}

func TestErrorCooldownZhipuProbeFailureAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name, outcome, wantStatus := "retained 429", "connection", 429
		if canceled {
			name, outcome, wantStatus = "canceled probe", "cancel", 499
		}
		t.Run(name, func(t *testing.T) {
			fresh(t)
			configured := provider.Provider{
				ID: "first", Name: "First", Key: "k", Models: []string{"glm-5.1"},
				Chat: "https://open.bigmodel.cn/api/paas/v4", Patience: -1, ErrorCooldownSeconds: 15,
			}
			if err := provider.Save(configured); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			server := New()
			server.client = &http.Client{Transport: cooldownZhipuTransport(outcome, cancel, &calls)}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"first/glm-5.1","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx))
			recent := server.Recent()
			if calls.Load() != 2 || len(recent) != 1 || recent[0].Status != wantStatus {
				t.Fatalf("calls=%d recent=%+v response=%d %s", calls.Load(), recent, recorder.Code, recorder.Body.String())
			}
			rest, cooling := errorCooldownOf(configured.ID)
			if canceled {
				if cooling {
					t.Errorf("canceled probe started cooldown: %+v", rest)
				}
			} else {
				if !cooling || rest.Status != 429 || time.Until(rest.Until) <= 0 || time.Until(rest.Until) > 15*time.Second {
					t.Errorf("retained upstream 429 missing fixed cooldown: %+v, %v", rest, cooling)
				}
				if recorder.Code != 429 || !strings.Contains(recorder.Body.String(), "1113") {
					t.Errorf("original upstream error lost: %d %s", recorder.Code, recorder.Body.String())
				}
			}
			records := usage.Load(time.Time{})
			if len(records) != 1 || records[0].Status != wantStatus {
				t.Errorf("final response usage=%+v", records)
			}
		})
	}
}
