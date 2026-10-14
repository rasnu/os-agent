// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"rasnuos-agent/internal/transport"
)

func TestHandleFileCommand(t *testing.T) {
	var lastReqBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastReqBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ApiURL: ts.URL,
		Token:  "test-token",
	}

	tempDir, err := os.MkdirTemp("", "rasnuos_file_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFilePath := filepath.Join(tempDir, "test.txt")

	writeArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-1",
		"path":        testFilePath,
		"content":     base64.StdEncoding.EncodeToString([]byte("hello rasnuos")),
		"mode":        "0600",
	}
	client.handleFileCommand("write-file", writeArgs)
	client.wg.Wait()

	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("write-file failed: %v", lastReqBody["log"])
	}

	content, _ := os.ReadFile(testFilePath)
	if string(content) != "hello rasnuos" {
		t.Errorf("write-file content mismatch. Got: %s", string(content))
	}
	info, _ := os.Stat(testFilePath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("write-file mode mismatch. Got: %v", info.Mode().Perm())
	}

	readArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-2",
		"path":        testFilePath,
	}
	client.handleFileCommand("read-file", readArgs)
	client.wg.Wait()

	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("read-file failed: %v", lastReqBody["log"])
	}
	if data, ok := lastReqBody["data"].(string); !ok || data != base64.StdEncoding.EncodeToString([]byte("hello rasnuos")) {
		t.Errorf("read-file data mismatch. Got: %v", lastReqBody["data"])
	}

	listArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-3",
		"path":        tempDir,
	}
	client.handleFileCommand("list-directory", listArgs)
	client.wg.Wait()

	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("list-directory failed: %v", lastReqBody["log"])
	}
	dataList, ok := lastReqBody["data"].([]interface{})
	if !ok || len(dataList) != 1 {
		t.Fatalf("list-directory data mismatch. Got: %v", lastReqBody["data"])
	}
	firstFile := dataList[0].(map[string]interface{})
	if firstFile["name"] != "test.txt" {
		t.Errorf("list-directory name mismatch. Got: %v", firstFile["name"])
	}
	if _, hasSymlink := firstFile["is_symlink"]; !hasSymlink {
		t.Errorf("list-directory missing is_symlink boolean")
	}

	relPathArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        "../test.txt",
	}
	client.handleFileCommand("read-file", relPathArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected relative path to fail")
	}

	missingFileArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "does-not-exist.txt"),
	}
	client.handleFileCommand("read-file", missingFileArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected reading non-existent file to fail")
	}

	badBase64Args := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "bad.txt"),
		"content":     "this-is-not-valid-base64!!",
	}
	client.handleFileCommand("write-file", badBase64Args)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected bad base64 write to fail")
	}

	deleteArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-4",
		"path":        testFilePath,
	}
	client.handleFileCommand("delete-file", deleteArgs)
	client.wg.Wait()

	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("delete-file failed: %v", lastReqBody["log"])
	}

	if _, err := os.Stat(testFilePath); !os.IsNotExist(err) {
		t.Errorf("File was not deleted")
	}

	missingArgs := map[string]interface{}{}
	client.handleFileCommand("read-file", missingArgs)

	unknownActionArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "unknown.txt"),
	}
	client.handleFileCommand("unknown-action", unknownActionArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected unknown action to fail")
	}

	readDirArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        tempDir,
	}
	client.handleFileCommand("read-file", readDirArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected reading directory to fail")
	}

	largeFile := filepath.Join(tempDir, "large.txt")
	f, _ := os.Create(largeFile)
	f.Truncate(6 * 1024 * 1024)
	f.Close()
	readLargeArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        largeFile,
	}
	client.handleFileCommand("read-file", readLargeArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected reading large file to fail")
	}

	downloadMissingUrlArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "dl.txt"),
	}
	client.handleFileCommand("download-file", downloadMissingUrlArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected download without URL to fail")
	}

	uploadMissingUrlArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        largeFile,
	}
	client.handleFileCommand("upload-file", uploadMissingUrlArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected upload without URL to fail")
	}

	newDirPath := filepath.Join(tempDir, "subdir", "nested")
	createDirArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-mkdir",
		"path":        newDirPath,
		"mode":        "0750",
	}
	client.handleFileCommand("create-directory", createDirArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("create-directory failed: %v", lastReqBody["log"])
	}
	dirStat, err := os.Stat(newDirPath)
	if err != nil || !dirStat.IsDir() {
		t.Fatalf("Expected directory to exist at %s", newDirPath)
	}

	renamedPath := filepath.Join(tempDir, "subdir", "renamed_nested")
	renameArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-rename",
		"path":        newDirPath,
		"new_path":    renamedPath,
	}
	client.handleFileCommand("rename-path", renameArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("rename-path failed: %v", lastReqBody["log"])
	}
	if _, err := os.Stat(newDirPath); !os.IsNotExist(err) {
		t.Errorf("Expected old path to not exist after rename")
	}
	if _, err := os.Stat(renamedPath); err != nil {
		t.Errorf("Expected new path to exist after rename")
	}

	targetFile := filepath.Join(renamedPath, "chmod_target.txt")
	os.WriteFile(targetFile, []byte("chmod test"), 0644)
	chmodArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-chmod",
		"path":        targetFile,
		"mode":        "0600",
	}
	client.handleFileCommand("chmod-path", chmodArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("chmod-path failed: %v", lastReqBody["log"])
	}
	fileStat, _ := os.Stat(targetFile)
	if fileStat.Mode().Perm() != 0600 {
		t.Errorf("chmod-path mode mismatch: got %04o, expected 0600", fileStat.Mode().Perm())
	}

	chownArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-chown",
		"path":        targetFile,
		"uid":         os.Getuid(),
		"gid":         os.Getgid(),
	}
	client.handleFileCommand("chown-path", chownArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("chown-path failed: %v", lastReqBody["log"])
	}

	deleteRootArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-del-root",
		"path":        "/",
	}
	client.handleFileCommand("delete-path", deleteRootArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected delete-path on / to be rejected by safety guardrail")
	}

	deleteDirArgs := map[string]interface{}{
		"pipeline_id": "pipe-1",
		"step_id":     "step-del-dir",
		"path":        filepath.Join(tempDir, "subdir"),
	}
	client.handleFileCommand("delete-path", deleteDirArgs)
	client.wg.Wait()
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("delete-path failed: %v", lastReqBody["log"])
	}
	if _, err := os.Stat(filepath.Join(tempDir, "subdir")); !os.IsNotExist(err) {
		t.Errorf("Expected deleted directory to not exist")
	}
}

func TestFileManager_AtomicWriteFilesAndHelpers(t *testing.T) {
	// 1. Test getInt helper across all types
	m := map[string]interface{}{
		"int_val":     42,
		"float_val":   float64(99),
		"str_val":     "123",
		"bad_str_val": "not-an-int",
	}
	if getInt(m, "int_val") != 42 {
		t.Errorf("Expected 42 for int_val")
	}
	if getInt(m, "float_val") != 99 {
		t.Errorf("Expected 99 for float_val")
	}
	if getInt(m, "str_val") != 123 {
		t.Errorf("Expected 123 for str_val")
	}
	if getInt(m, "bad_str_val") != 0 {
		t.Errorf("Expected 0 for bad_str_val")
	}
	if getInt(m, "missing") != 0 {
		t.Errorf("Expected 0 for missing key")
	}

	var lastReqBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastReqBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := transport.Default()
	transport.SetDefault(ts.Client())
	defer func() { transport.SetDefault(originalClient) }()

	client := &PusherClient{
		ApiURL: ts.URL,
		Token:  "test-token",
	}

	tempDir, err := os.MkdirTemp("", "rasnuos_atomic_files_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Test write-files atomic transaction (overwriting an existing file + creating a new file)
	existingFile := filepath.Join(tempDir, "existing.txt")
	os.WriteFile(existingFile, []byte("old-content"), 0644)
	newFile := filepath.Join(tempDir, "nested", "new.txt")

	client.handleFileCommand("write-files", map[string]interface{}{
		"pipeline_id": "pipe-atomic",
		"step_id":     "step-atomic",
		"files": []interface{}{
			map[string]interface{}{
				"path":    existingFile,
				"content": base64.StdEncoding.EncodeToString([]byte("updated-content")),
				"mode":    "0600",
			},
			map[string]interface{}{
				"path":    newFile,
				"content": base64.StdEncoding.EncodeToString([]byte("brand-new-content")),
				"mode":    "0644",
			},
		},
	})
	client.wg.Wait()

	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Fatalf("write-files failed: %v", lastReqBody["log"])
	}
	if b, _ := os.ReadFile(existingFile); string(b) != "updated-content" {
		t.Errorf("Expected updated-content, got %q", string(b))
	}
	if b, _ := os.ReadFile(newFile); string(b) != "brand-new-content" {
		t.Errorf("Expected brand-new-content, got %q", string(b))
	}

	// 3. Test recursive chmod-path and chown-path
	client.handleFileCommand("chmod-path", map[string]interface{}{
		"pipeline_id": "pipe-atomic",
		"step_id":     "step-chmod-rec",
		"path":        tempDir,
		"mode":        "0755",
		"recursive":   true,
	})
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Errorf("recursive chmod-path failed: %v", lastReqBody["log"])
	}

	client.handleFileCommand("chown-path", map[string]interface{}{
		"pipeline_id": "pipe-atomic",
		"step_id":     "step-chown-rec",
		"path":        tempDir,
		"uid":         os.Getuid(),
		"gid":         os.Getgid(),
		"recursive":   true,
	})
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Errorf("recursive chown-path failed: %v", lastReqBody["log"])
	}

	// 4. Test symlink resolution in list-directory
	symlinkPath := filepath.Join(tempDir, "link_to_nested")
	_ = os.Symlink(filepath.Join(tempDir, "nested"), symlinkPath)
	client.handleFileCommand("list-directory", map[string]interface{}{
		"pipeline_id": "pipe-atomic",
		"step_id":     "step-list-sym",
		"path":        tempDir,
	})
	if success, ok := lastReqBody["success"].(bool); !ok || !success {
		t.Errorf("list-directory with symlink failed: %v", lastReqBody["log"])
	}
}

func TestNormalizePlatformPathForWindows(t *testing.T) {
	cases := []struct {
		input    string
		sysDrive string
		expected string
	}{
		{"/", "C:", `C:\`},
		{"\\", "C:", `C:\`},
		{"~", "D:", `D:\`},
		{"/Users", "C:", `C:\Users`},
		{"/Users/Administrator/notes.txt", "C:", `C:\Users\Administrator\notes.txt`},
		{"/C:/Users/Administrator", "C:", `C:\Users\Administrator`},
		{"C:/Users/Administrator", "C:", `C:\Users\Administrator`},
		{`C:\Users\Administrator`, "C:", `C:\Users\Administrator`},
		{"C:", "C:", `C:\`},
		{"/C:", "C:", `C:\`},
		{"../secret.txt", "C:", `..\secret.txt`},
	}

	for _, tc := range cases {
		got := normalizePlatformPathForOS(tc.input, "windows", tc.sysDrive)
		if got != tc.expected {
			t.Errorf("normalizePlatformPathForOS(%q, windows, %q) = %q, want %q", tc.input, tc.sysDrive, got, tc.expected)
		}
	}

	// Ensure non-Windows OS paths are untouched
	if got := normalizePlatformPathForOS("/var/www/html", "linux", ""); got != "/var/www/html" {
		t.Errorf("Expected Linux path to remain untouched, got %q", got)
	}
}
