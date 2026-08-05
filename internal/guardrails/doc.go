// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package guardrails detects destructive shell commands (disk wipes, recursive root
// deletes, fork bombs, firewall flushes) on Linux, macOS, and Windows before a
// shell process is spawned.
package guardrails
