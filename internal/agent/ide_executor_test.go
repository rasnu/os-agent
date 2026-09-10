// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleViewFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")

	if _, err := handleViewFile(IdeIntentPayload{}); err == nil {
		t.Errorf("expected error when absolute_path is empty")
	}

	if _, err := handleViewFile(IdeIntentPayload{AbsolutePath: filepath.Join(tmpDir, "missing.txt")}); err == nil {
		t.Errorf("expected error for non-existent file")
	}

	// Empty file
	if err := os.WriteFile(filePath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := handleViewFile(IdeIntentPayload{AbsolutePath: filePath})
	if err != nil || !strings.Contains(out, "is empty") {
		t.Errorf("expected empty file notice, got %q, err=%v", out, err)
	}

	// Populated file with CRLF
	content := "line one\r\nline two\r\nline three\r\nline four"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	out, err = handleViewFile(IdeIntentPayload{AbsolutePath: filePath, StartLine: 2, EndLine: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "line two") || !strings.Contains(out, "line three") || strings.Contains(out, "line one") {
		t.Errorf("unexpected sliced output:\n%s", out)
	}

	// Invalid range
	if _, err := handleViewFile(IdeIntentPayload{AbsolutePath: filePath, StartLine: 10, EndLine: 12}); err == nil {
		t.Errorf("expected error for out-of-bounds line range")
	}
}

func TestHandleWriteToFile(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "subdir", "config.conf")

	if _, err := handleWriteToFile(IdeIntentPayload{}); err == nil {
		t.Errorf("expected error when target_file is missing")
	}
	if _, err := handleWriteToFile(IdeIntentPayload{TargetFile: target}); err == nil {
		t.Errorf("expected error when code_content is missing")
	}
	if _, err := handleWriteToFile(IdeIntentPayload{TargetFile: "/etc/passwd", CodeContent: "test"}); err == nil {
		t.Errorf("expected restricted directory /etc/ to be blocked")
	}

	// Create new file via ReplacementContent fallback
	out, err := handleWriteToFile(IdeIntentPayload{
		TargetFile:         target,
		ReplacementContent: "port=8080\nhost=127.0.0.1\n",
	})
	if err != nil {
		t.Fatalf("handleWriteToFile create failed: %v", err)
	}
	if !strings.Contains(out, "File created from scratch") {
		t.Errorf("expected 'File created from scratch', got:\n%s", out)
	}

	// Overwrite existing file
	out2, err := handleWriteToFile(IdeIntentPayload{
		TargetFile:  target,
		CodeContent: "port=9090\nhost=127.0.0.1\n",
	})
	if err != nil {
		t.Fatalf("handleWriteToFile overwrite failed: %v", err)
	}
	if !strings.Contains(out2, "Existing file overwritten") {
		t.Errorf("expected 'Existing file overwritten', got:\n%s", out2)
	}
}

func TestHandleMultiReplaceFileContent(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "app.env")

	initial := "APP_ENV=local\r\nAPP_DEBUG=true\r\nDB_HOST=127.0.0.1\r\nCACHE_DRIVER=file\r\n"
	if err := os.WriteFile(target, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	// Restricted path check
	if _, err := handleMultiReplaceFileContent(IdeIntentPayload{
		TargetFile: "/etc/hosts",
		ReplacementChunks: []ReplacementChunk{
			{StartLine: 1, EndLine: 1, TargetContent: "a", ReplacementContent: "b"},
		},
	}); err == nil {
		t.Errorf("expected /etc/hosts to be blocked")
	}

	// Overlapping chunks check
	if _, err := handleMultiReplaceFileContent(IdeIntentPayload{
		TargetFile: target,
		ReplacementChunks: []ReplacementChunk{
			{StartLine: 1, EndLine: 2, TargetContent: "APP_ENV=local", ReplacementContent: "APP_ENV=prod"},
			{StartLine: 2, EndLine: 3, TargetContent: "APP_DEBUG=true", ReplacementContent: "APP_DEBUG=false"},
		},
	}); err == nil {
		t.Errorf("expected overlapping chunks to be rejected")
	}

	// Multi-chunk replacement including shifted line auto-discovery and CRLF preservation
	out, err := handleMultiReplaceFileContent(IdeIntentPayload{
		TargetFile: target,
		ReplacementChunks: []ReplacementChunk{
			{
				StartLine:          1,
				EndLine:            1,
				TargetContent:      "APP_ENV=local",
				ReplacementContent: "APP_ENV=production",
			},
			{
				// Intentionally give line 2 even though CACHE_DRIVER=file is on line 4 to test window search fallback
				StartLine:          2,
				EndLine:            2,
				TargetContent:      "CACHE_DRIVER=file",
				ReplacementContent: "CACHE_DRIVER=redis",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleMultiReplaceFileContent failed: %v", err)
	}
	if !strings.Contains(out, "Successfully updated") {
		t.Errorf("unexpected output: %s", out)
	}

	updatedBytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	updatedStr := string(updatedBytes)
	if !strings.Contains(updatedStr, "APP_ENV=production\r\n") || !strings.Contains(updatedStr, "CACHE_DRIVER=redis") {
		t.Errorf("expected updated content with preserved CRLF, got %q", updatedStr)
	}
}

func TestExecuteIdeIntentDispatch(t *testing.T) {
	_, cleanup := setupMockHTTP()
	defer cleanup()

	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "intent.txt")

	client := &PusherClient{
		ServerID: "srv-1",
		ApiURL:   "http://mock-backend",
	}

	// Invalid JSON
	client.executeIdeIntent("p1", "s1", "[IDE_INTENT]{bad-json")

	// Unknown action
	client.executeIdeIntent("p1", "s2", `[IDE_INTENT]{"action":"unknown_op"}`)

	// write_to_file
	writePayload, _ := json.Marshal(IdeIntentPayload{
		Action:      "write_to_file",
		TargetFile:  target,
		CodeContent: "alpha\nbeta\ngamma\n",
	})
	client.executeIdeIntent("p1", "s3", "[IDE_INTENT]"+string(writePayload))

	// replace_file_content
	replacePayload, _ := json.Marshal(IdeIntentPayload{
		Action:             "replace_file_content",
		TargetFile:         target,
		StartLine:          2,
		EndLine:            2,
		TargetContent:      "beta",
		ReplacementContent: "BETA_UPDATED",
	})
	client.executeIdeIntent("p1", "s4", "[IDE_INTENT]"+string(replacePayload))

	// view_file
	viewPayload, _ := json.Marshal(IdeIntentPayload{
		Action:       "view_file",
		AbsolutePath: target,
		StartLine:    1,
		EndLine:      3,
	})
	client.executeIdeIntent("p1", "s5", "[IDE_INTENT]"+string(viewPayload))

	finalData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(finalData), "BETA_UPDATED") {
		t.Errorf("expected BETA_UPDATED in file, got %q", string(finalData))
	}
}
