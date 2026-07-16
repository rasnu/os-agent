// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Command rasnuos-agent is the RasnuOS host daemon. It maintains an outbound
// WebSocket connection to the RasnuOS control plane and executes authorized
// operations (commands, file management, PTY sessions, migrations, tunnels).
package main

import "rasnuos-agent/internal/agent"

func main() {
	agent.Main()
}
