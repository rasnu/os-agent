package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleFileCommand(t *testing.T) {
	// Create a mock HTTP server to capture the completion webhook
	var lastReqBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastReqBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	originalClient := defaultHTTPClient
	defaultHTTPClient = ts.Client()
	defer func() { defaultHTTPClient = originalClient }()

	client := &PusherClient{
		ApiURL: ts.URL,
		Token:  "test-token",
	}

	// Create a temporary directory for file operations
	tempDir, err := os.MkdirTemp("", "rasnuos_file_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFilePath := filepath.Join(tempDir, "test.txt")

	// 1. Test write-file
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

	// Verify file was written and mode was respected
	content, _ := os.ReadFile(testFilePath)
	if string(content) != "hello rasnuos" {
		t.Errorf("write-file content mismatch. Got: %s", string(content))
	}
	info, _ := os.Stat(testFilePath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("write-file mode mismatch. Got: %v", info.Mode().Perm())
	}

	// 2. Test read-file
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

	// 3. Test list-directory
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

	// 4. Test Error Case: Relative Path Rejection
	relPathArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        "../test.txt", // Relative path!
	}
	client.handleFileCommand("read-file", relPathArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected relative path to fail")
	}

	// 5. Test Error Case: Read Non-existent File
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

	// 6. Test Error Case: Bad Base64 Write
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

	// 7. Test delete-file
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

	// Verify file was deleted
	if _, err := os.Stat(testFilePath); !os.IsNotExist(err) {
		t.Errorf("File was not deleted")
	}

	// 8. Test Missing Arguments
	missingArgs := map[string]interface{}{}
	client.handleFileCommand("read-file", missingArgs)

	// 9. Test Unknown Action
	unknownActionArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "unknown.txt"),
	}
	client.handleFileCommand("unknown-action", unknownActionArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected unknown action to fail")
	}

	// 10. Test Read Directory as File
	readDirArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        tempDir, // This is a directory, not a regular file
	}
	client.handleFileCommand("read-file", readDirArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected reading directory to fail")
	}

	// 11. Test >5MB file
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

	// 12. Test download-file missing URL
	downloadMissingUrlArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        filepath.Join(tempDir, "dl.txt"),
	}
	client.handleFileCommand("download-file", downloadMissingUrlArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected download without URL to fail")
	}

	// 13. Test upload-file missing URL
	uploadMissingUrlArgs := map[string]interface{}{
		"pipeline_id": "pipe-err",
		"step_id":     "step-err",
		"path":        largeFile,
	}
	client.handleFileCommand("upload-file", uploadMissingUrlArgs)
	if success, ok := lastReqBody["success"].(bool); !ok || success {
		t.Fatalf("Expected upload without URL to fail")
	}

	// 14. Test create-directory
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

	// 15. Test rename-path
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

	// 16. Test chmod-path
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

	// 17. Test chown-path
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

	// 18. Test delete-path safety guardrail (refuse root)
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

	// 19. Test delete-path regular recursive directory deletion
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
