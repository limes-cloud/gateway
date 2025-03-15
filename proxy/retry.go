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

func calcTimeout(endpoint *config.Endpoint) time.Duration {
	var timeout time.Duration = 0
	if endpoint.Timeout != 0 {
		timeout = endpoint.Timeout
	}
	return timeout
}

func calcAttempts(endpoint *config.Endpoint) int {
	if endpoint.Retry == nil {
		return 1
	}
	if endpoint.Retry.Count == 0 {
		return 1
	}
	return endpoint.Retry.Count
}

func calcPerTryTimeout(endpoint *config.Endpoint) time.Duration {
	var perTryTimeout time.Duration
	if endpoint.Retry != nil && endpoint.Retry.Timeout != 0 {
		perTryTimeout = endpoint.Retry.Timeout
	} else if endpoint.Timeout != 0 {
		perTryTimeout = endpoint.Timeout
	}
	if endpoint.Timeout == 0 {
		return 0
	}
	if perTryTimeout <= 0 {
		perTryTimeout = time.Second
	}
	return perTryTimeout
}

func prepareRetryStrategy(e *config.Endpoint) (*retryStrategy, error) {
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

func parseRetryConditon(endpoint *config.Endpoint) ([]condition.Condition, error) {
	if endpoint.Retry == nil {
		return []condition.Condition{}, nil
	}
	return condition.ParseConditon(endpoint.Retry.Conditions)
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
