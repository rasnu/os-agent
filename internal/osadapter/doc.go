// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package osadapter isolates OS-specific behavior behind the OSAdapter interface:
// shell invocation, service management (systemd, launchd, Windows SCM), scheduled
// jobs, log file locations, and binary replacement during self-updates.
package osadapter
