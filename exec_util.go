package main

import (
	"os"
	"os/exec"
)

var (
	// execCommand allows us to mock exec.Command in tests
	execCommand = exec.Command
	// execCommandContext allows us to mock exec.CommandContext in tests
	execCommandContext = exec.CommandContext
	// osWriteFile allows us to mock os.WriteFile in tests
	osWriteFile = os.WriteFile
	// osRemove allows us to mock os.Remove in tests
	osRemove = os.Remove
)
