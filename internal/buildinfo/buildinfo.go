// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

// Package buildinfo holds values stamped into the binary at link time via -ldflags.
package buildinfo

// Version is the agent release version, overridden at link time via -ldflags.
var Version = "1.0.0"
