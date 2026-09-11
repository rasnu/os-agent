package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"
)

func TestDebugPipe2(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "bash", "-c", "echo 'E2E Test Success'")
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
