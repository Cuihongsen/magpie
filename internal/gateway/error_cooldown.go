package gateway

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

func errorCooldownKey(id string) string {
	return "error-cooldown:" + id
}

func errorCooldownOf(id string) (Rest, bool) {
	return restOf(errorCooldownKey(id))
}

func errorCooldownAfter(c candidate, status int, body []byte, now time.Time) Rest {
	id := errorCooldownKey(c.p.ID)
	rest := Rest{
		Why:    failureOf(c, status, body),
		Status: status,
		Until:  now.Add(time.Duration(c.p.ErrorCooldownSeconds) * time.Second),
		By:     "error-cooldown",
		Key:    id,
		Error:  restError(status, body),
	}
	restingUntil.Lock()
	if current := restingUntil.m[id]; current.After(rest.Until) {
		rest.Until = current
	} else {
		restingUntil.m[id] = rest.Until
		restingUntil.note[id] = rest
	}
	restingUntil.Unlock()
	return rest
}

func errorCooldownStatus(status int) bool {
	return status >= http.StatusBadRequest && status < 600
}

func errorCooldownRemaining(candidates []candidate, stopped string) []candidate {
	return slices.DeleteFunc(slices.Clone(candidates), func(item candidate) bool {
		if item.p.ID == stopped {
			return true
		}
		_, cooling := errorCooldownOf(item.p.ID)
		return item.p.ErrorCooldownSeconds > 0 && cooling
	})
}

type errorCooldownAttemptKey struct{}

type errorCooldownAttempt struct {
	mu         sync.Mutex
	provider   string
	httpStatus int
	status     int
	body       []byte
}

func observeErrorCooldownReply(ctx context.Context, id string, response *http.Response, err error) {
	attempt, _ := ctx.Value(errorCooldownAttemptKey{}).(*errorCooldownAttempt)
	if attempt == nil || attempt.provider != id {
		return
	}
	attempt.mu.Lock()
	attempt.httpStatus, attempt.status, attempt.body = 0, 0, nil
	if err == nil && response != nil {
		attempt.httpStatus = response.StatusCode
	}
	attempt.mu.Unlock()
	if err == nil && response != nil && response.Body != nil && response.StatusCode < 400 {
		response.Body = &errorCooldownBody{
			ReadCloser: response.Body,
			attempt:    attempt,
			stream:     strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"),
		}
	}
}

func (attempt *errorCooldownAttempt) failed(status int, body []byte) (int, []byte, bool) {
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if errorCooldownStatus(attempt.status) {
		return attempt.status, attempt.body, true
	}
	if errorCooldownStatus(attempt.httpStatus) {
		if errorCooldownStatus(status) {
			return status, body, true
		}
		return attempt.httpStatus, body, true
	}
	return 0, nil, false
}

func (attempt *errorCooldownAttempt) note(status int, body []byte) {
	if !errorCooldownStatus(status) {
		return
	}
	attempt.mu.Lock()
	attempt.status, attempt.body = status, slices.Clone(body)
	attempt.mu.Unlock()
}

type errorCooldownBody struct {
	io.ReadCloser
	attempt *errorCooldownAttempt
	stream  bool
	pending []byte
	over    bool
}

func (body *errorCooldownBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if body.over {
		return count, err
	}
	body.pending = append(body.pending, buffer[:count]...)
	if !body.stream {
		if stream, sure := sseStart(body.pending); sure && stream {
			body.stream = true
		}
	}
	if body.stream {
		for {
			end := eventEnd(body.pending)
			if end < 0 {
				break
			}
			body.event(body.pending[:end])
			body.pending = body.pending[end:]
		}
		if err == io.EOF && len(body.pending) > 0 {
			body.event(body.pending)
			body.pending = nil
		}
	} else if err == io.EOF {
		if status, message, failed := errorReply(body.pending); failed {
			body.attempt.note(status, []byte(message))
		}
		body.pending = nil
	}
	if len(body.pending) > 1<<20 {
		body.pending = nil
		body.over = !body.stream
	}
	return count, err
}

func (body *errorCooldownBody) event(event []byte) {
	kind, status, message := streamEvent(event)
	if kind == eventError || kind == eventRefusalError {
		body.attempt.note(status, []byte(message))
	}
}
