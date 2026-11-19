// agent.go holds AgentVars.

package daemon

import (
	"fmt"
	"os"
	"slices"

	agent "github.com/wow-look-at-my/is-this-an-agent"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// extraAgentVars are markers the shared roster does not list. Every marker
// counts when it is defined, whatever its value.
var extraAgentVars = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"GROK_BUILD",
	"CODEX_THREAD_ID",
	"CURSOR_AGENT",
	"AGENT",
}

// DetectAgent answers the agent this process runs under, by process ancestry and then by environment marker.
var DetectAgent = agent.Detect

// AgentVars answers every variable that marks an agent session.
func AgentVars() []string {
	vars := slices.Clone(extraAgentVars)
	for _, a := range agent.Roster() {
		vars = append(vars, a.EnvVars...)
	}
	return vars
}

// AgentSession answers what marks this process as an AI agent session, if anything does.
func AgentSession() (string, bool) {
	if a, ok := DetectAgent(); ok {
		return "it runs under " + a.Name, true
	}
	for _, name := range AgentVars() {
		if _, ok := os.LookupEnv(name); ok {
			return name + " is set", true
		}
	}
	return "", false
}

// refuseAgent stops an agent before it can make the daemon delete what it
// would not delete by itself.
func refuseAgent(what string) error {
	if why, ok := AgentSession(); ok {
		return fmt.Errorf("%w: %s, so only a person may %s", bin.ErrAgentSession, why, what)
	}
	return nil
}
