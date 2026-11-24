package gateway

import (
	"net/http"
	"time"
)

func depletedBalance429(status int, body []byte) bool {
	return status == http.StatusTooManyRequests &&
		failure(status, body) == failCredit &&
		brokeWords.Match(wordsOf(body))
}

func crazyRetryWait(_ http.Header, _ time.Time) (time.Duration, bool) {
	return 100 * time.Millisecond, true
}
