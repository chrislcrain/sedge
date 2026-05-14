package instructions

import _ "embed"

//go:embed default_agents.md
var DefaultAgentsMD string

//go:embed planner.md
var OrchestrationPlannerMD string
