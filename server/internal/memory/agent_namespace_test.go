package memory

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentRecallFilter(t *testing.T) {
	t.Parallel()
	ids, err := AgentRecallFilter("", nil)
	if err != nil || ids != nil {
		t.Fatalf("empty agent = %v %v", ids, err)
	}
	_, err = AgentRecallFilter("", []string{"planner"})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("extra without agent: %v", err)
	}
	ids, err = AgentRecallFilter(" executor ", []string{"planner", "executor", " reviewer "})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "executor" || ids[1] != "planner" || ids[2] != "reviewer" {
		t.Fatalf("ids=%v", ids)
	}
	_, err = AgentRecallFilter("executor", make([]string, maxExtraAgents+1))
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("too many extras: %v", err)
	}
	_, err = AgentRecallFilter(strings.Repeat("a", maxProducerIDRunes+1), nil)
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("long agent: %v", err)
	}
}
