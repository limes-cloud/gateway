package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/go-kratos/feature"

	"github.com/limes-cloud/gateway/config"
	"github.com/limes-cloud/gateway/proxy/condition"
)

var (
	retryFeature = feature.MustRegister("gw:Retry", true)
)

type retryStrategy struct {
	attempts      int
	timeout       time.Duration
	perTryTimeout time.Duration
	conditions    []condition.Condition
}

func calcTimeout(endpoint *config.Route) time.Duration {
	return endpoint.Upstream.Timeout()
}

func calcAttempts(endpoint *config.Route) int {
	if endpoint.Upstream.Retry == nil {
		return 1
	}
	if endpoint.Upstream.Retry.Count == 0 {
		return 1
	}
	return endpoint.Upstream.Retry.Count
}

func calcPerTryTimeout(endpoint *config.Route) time.Duration {
	var perTryTimeout time.Duration
	if endpoint.Upstream.Retry != nil && endpoint.Upstream.Retry.TimeoutMs > 0 {
		perTryTimeout = time.Duration(endpoint.Upstream.Retry.TimeoutMs) * time.Millisecond
	} else if endpoint.Upstream.Timeout() != 0 {
		perTryTimeout = endpoint.Upstream.Timeout()
	}
	if endpoint.Upstream.Timeout() == 0 {
		return 0
	}
	if perTryTimeout <= 0 {
		perTryTimeout = time.Second
	}
	return perTryTimeout
}

func prepareRetryStrategy(e *config.Route) (*retryStrategy, error) {
	strategy := &retryStrategy{
		attempts:      calcAttempts(e),
		timeout:       calcTimeout(e),
		perTryTimeout: calcPerTryTimeout(e),
	}
	conditions, err := parseRetryConditon(e)
	if err != nil {
		return nil, err
	}
	strategy.conditions = conditions
	return strategy, nil
}

func parseRetryConditon(endpoint *config.Route) ([]condition.Condition, error) {
	if endpoint.Upstream.Retry == nil {
		return []condition.Condition{}, nil
	}
	return condition.ParseConditon(endpoint.Upstream.Retry.Conditions)
}

func judgeRetryRequired(conditions []condition.Condition, resp *http.Response) bool {
	return condition.JudgeConditons(conditions, resp, false)
}

func defaultAttemptTimeoutContext(ctx context.Context, _ *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}
