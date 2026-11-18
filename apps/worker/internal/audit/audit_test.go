package audit

import (
	"testing"

	"github.com/google/uuid"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
	"github.com/stretchr/testify/assert"
)

func TestActorForRun(t *testing.T) {
	agentID := uuid.New()

	assert.Equal(t, sdkaudit.AgentActor(agentID), ActorForRun(agentID.String()))
	assert.Equal(t, sdkaudit.SystemActor("worker"), ActorForRun(""))
	assert.Equal(t, sdkaudit.SystemActor("worker"), ActorForRun("not-a-uuid"))
}
