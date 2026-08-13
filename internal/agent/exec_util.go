// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"os"
	"os/exec"
)

var (
	execCommand        = exec.Command
	execCommandContext = exec.CommandContext
	osWriteFile        = os.WriteFile
	osRemove           = os.Remove
)
