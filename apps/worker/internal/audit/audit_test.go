package audit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestActorForRun(t *testing.T) {
	agentID := uuid.New()

	// The expected values are pinned literals on purpose. Computing them with
	// sdkaudit.AgentActor/SystemActor would recompute both sides of the
	// assertion with the same helper ActorForRun uses, so a change to the actor
	// prefix format would keep the test green while breaking the audit wire
	// format. See packages/sdk-go/audit/logger.go ActorPrefixAgent/ActorPrefixSystem.
	assert.Equal(t, "agent:"+agentID.String(), ActorForRun(agentID.String()))
	// Empty or unparseable agent ID means the worker acts as the platform actor.
	assert.Equal(t, "system:worker", ActorForRun(""))
	assert.Equal(t, "system:worker", ActorForRun("not-a-uuid"))
}
