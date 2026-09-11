package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"
)

func TestDebugPipe3(t *testing.T) {
	command := "echo 'E2E Test Success'" // EXACTLY what is passed
	cmd := exec.CommandContext(context.Background(), "bash", "-c", command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(stdout)
	fmt.Printf("OUTPUT: %q\n", string(b))
	cmd.Wait()
}
