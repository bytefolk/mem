package memory

import (
	"strings"
	"unicode/utf8"
)

const maxExtraAgents = 8

// AgentRecallFilter is the fail-closed producer-agent allowlist for one recall.
// A nil list means no agent filter (human/operator browse). A non-empty list
// is the caller's namespace plus any explicitly shared extra agents.
func AgentRecallFilter(agentID string, extra []string) ([]string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		if len(extra) > 0 {
			return nil, invalid("extra_agent_ids requires agent_id")
		}
		return nil, nil
	}
	if utf8.RuneCountInString(agentID) > maxProducerIDRunes {
		return nil, invalid("agent_id exceeds 255 characters")
	}
	seen := map[string]struct{}{agentID: {}}
	ids := []string{agentID}
	if len(extra) > maxExtraAgents {
		return nil, invalid("extra_agent_ids exceeds 8 entries")
	}
	for _, raw := range extra {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if utf8.RuneCountInString(id) > maxProducerIDRunes {
			return nil, invalid("extra_agent_ids entry exceeds 255 characters")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}
