package gateway

import (
	"net/http"
	"time"
)

func crazyRetryWait(_ http.Header, _ time.Time) (time.Duration, bool) {
	return 100 * time.Millisecond, true
}
