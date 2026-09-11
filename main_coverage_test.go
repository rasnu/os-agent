package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestFirstNonEmpty(t *testing.T) {
	os.Setenv("TEST_ENV_KEY", "env_val")
	defer os.Unsetenv("TEST_ENV_KEY")

	val := firstNonEmpty("", "TEST_ENV_KEY", "fallback")
	if val != "env_val" {
		t.Errorf("expected env_val, got %s", val)
	}
}

func TestBuildServerURL_HttpToWs(t *testing.T) {
	os.Setenv("REVERB_SCHEME", "http")
	os.Setenv("REVERB_HOST", "example.com")
	os.Setenv("REVERB_PORT", "80")
	os.Setenv("REVERB_APP_KEY", "rasnuone_agent_key")
	defer os.Unsetenv("REVERB_SCHEME")
	defer os.Unsetenv("REVERB_HOST")
	defer os.Unsetenv("REVERB_PORT")
	defer os.Unsetenv("REVERB_APP_KEY")

	url := buildServerURL("")
	if url != "ws://example.com:80/app/rasnuone_agent_key?protocol=7&client=rasnuone-agent&version=1.0.0" {
		t.Errorf("wrong URL generated: %s", url)
	}
}

func TestRun_VersionFlag(t *testing.T) {
	err := run([]string{"--version"})
	if err != nil {
		t.Errorf("run with --version returned error: %v", err)
	}
}

func TestRun_MissingTokens(t *testing.T) {
	os.Unsetenv("BEARER_TOKEN")
	os.Unsetenv("SERVER_ID")
	err := run([]string{})
	if err == nil {
		t.Errorf("expected error when missing tokens")
	}
}

func TestRun_Success(t *testing.T) {
	// Schedule interrupt
	go func() {
		time.Sleep(50 * time.Millisecond)
		// Send interrupt to ourselves
		proc, _ := os.FindProcess(os.Getpid())
		proc.Signal(syscall.SIGINT)
	}()

	err := run([]string{"--token", "test", "--server-id", "test"})
	if err != nil {
		t.Errorf("run returned error: %v", err)
	}
}
