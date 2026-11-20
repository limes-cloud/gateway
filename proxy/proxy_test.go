package proxy

import (
	"testing"

	"github.com/limes-cloud/gateway/config"
)

func TestRouteBeforeUsesPriorityThenID(t *testing.T) {
	highPriority := config.Route{ID: 2, Priority: 10}
	lowPriority := config.Route{ID: 1, Priority: 0}
	if !routeBefore(&highPriority, &lowPriority) {
		t.Fatal("higher priority should match first")
	}
	first := config.Route{ID: 1, Priority: 0}
	second := config.Route{ID: 2, Priority: 0}
	if !routeBefore(&first, &second) {
		t.Fatal("lower id should match first at same priority")
	}
}
