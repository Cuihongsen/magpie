package provider

const (
	DefaultCrazyRetryCount = 20
	MaxCrazyRetryCount     = 100
)

func (p Provider) CrazyRetryLimit() int {
	if !p.CrazyRetry {
		return 0
	}
	if p.CrazyRetryCount < 1 || p.CrazyRetryCount > MaxCrazyRetryCount {
		return DefaultCrazyRetryCount
	}
	return p.CrazyRetryCount
}
