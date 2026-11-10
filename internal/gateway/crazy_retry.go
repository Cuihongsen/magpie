package gateway

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func crazyRetryWait(header http.Header, now time.Time) (time.Duration, bool) {
	if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			if seconds > longestWait.Seconds() {
				return 0, false
			}
		} else if when, err := http.ParseTime(value); err == nil && when.Sub(now) > longestWait {
			return 0, false
		}
	}
	return max(retryPause, retryAfter(header, now)), true
}
