// agent.go holds AgentVars.

package daemon

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// AgentVars are the variables an AI coding agent sets in the shells it runs.
// A variable counts when it is defined, whatever its value.
var AgentVars = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"GROK_BUILD",
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
	"CODEX_THREAD_ID",
	"GEMINI_CLI",
	"CURSOR_AGENT",
	"OPENCODE",
	"AGENT",
}

// AgentSession answers the first agent variable this process has, if any.
func AgentSession() (string, bool) {
	for _, name := range AgentVars {
		if _, ok := os.LookupEnv(name); ok {
			return name, true
		}
	}
	return "", false
}

// refuseAgent stops an agent before it can empty the bin or change what the
// daemon keeps free.
func refuseAgent(what string) error {
	if name, ok := AgentSession(); ok {
		return fmt.Errorf("%w: %s is set, so this looks like an AI agent session, and only a person may %s",
			bin.ErrAgentSession, name, what)
	}
	return nil
}
