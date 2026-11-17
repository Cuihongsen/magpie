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
	mu        sync.Mutex
	provider  string
	reply     *errorCooldownEvidence
	responses map[*http.Response]*errorCooldownEvidence
	requests  map[*http.Request]*errorCooldownEvidence
}

type errorCooldownEvidence struct {
	httpStatus int
	status     int
	body       []byte
}

func observeErrorCooldownReply(ctx context.Context, id string, response *http.Response, err error) {
	attempt, _ := ctx.Value(errorCooldownAttemptKey{}).(*errorCooldownAttempt)
	if attempt == nil || attempt.provider != id {
		return
	}
	var evidence *errorCooldownEvidence
	attempt.mu.Lock()
	attempt.reply = nil
	if err == nil && response != nil {
		evidence = &errorCooldownEvidence{httpStatus: response.StatusCode}
		if attempt.responses == nil {
			attempt.responses = map[*http.Response]*errorCooldownEvidence{}
			attempt.requests = map[*http.Request]*errorCooldownEvidence{}
		}
		attempt.responses[response] = evidence
		if response.Request != nil {
			attempt.requests[response.Request] = evidence
		}
		attempt.reply = evidence
	}
	attempt.mu.Unlock()
	if err == nil && response != nil && response.Body != nil && response.StatusCode < 400 {
		response.Body = &errorCooldownBody{
			ReadCloser: response.Body,
			attempt:    attempt,
			reply:      evidence,
			stream:     strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"),
		}
	}
}

func selectErrorCooldownReply(ctx context.Context, id string, response *http.Response, err error) {
	attempt, _ := ctx.Value(errorCooldownAttemptKey{}).(*errorCooldownAttempt)
	if attempt == nil || attempt.provider != id {
		return
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	attempt.reply = nil
	if err == nil && response != nil {
		attempt.reply = attempt.responses[response]
		if attempt.reply == nil && response.Request != nil {
			attempt.reply = attempt.requests[response.Request]
		}
	}
}

func inheritErrorCooldownReply(ctx context.Context, id string, response, source *http.Response) {
	attempt, _ := ctx.Value(errorCooldownAttemptKey{}).(*errorCooldownAttempt)
	if attempt == nil || attempt.provider != id || response == nil || source == nil {
		return
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if evidence := attempt.responses[source]; evidence != nil {
		attempt.responses[response] = evidence
	}
}

func (attempt *errorCooldownAttempt) failed(status int, body []byte) (int, []byte, bool) {
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	reply := attempt.reply
	if reply == nil {
		return 0, nil, false
	}
	if errorCooldownStatus(reply.status) {
		return reply.status, reply.body, true
	}
	if errorCooldownStatus(reply.httpStatus) {
		if errorCooldownStatus(status) {
			return status, body, true
		}
		return reply.httpStatus, body, true
	}
	return 0, nil, false
}

func (attempt *errorCooldownAttempt) note(reply *errorCooldownEvidence, status int, body []byte) {
	if reply == nil || !errorCooldownStatus(status) {
		return
	}
	attempt.mu.Lock()
	reply.status, reply.body = status, slices.Clone(body)
	attempt.mu.Unlock()
}

type errorCooldownBody struct {
	io.ReadCloser
	attempt *errorCooldownAttempt
	reply   *errorCooldownEvidence
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
			body.attempt.note(body.reply, status, []byte(message))
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
		body.attempt.note(body.reply, status, []byte(message))
	}
}
