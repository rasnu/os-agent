package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(int); ok {
		return v
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(string); ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}

func (c *PusherClient) handleFileCommand(action string, args map[string]interface{}) {
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")
	path := getString(args, "path")

	if pipelineId == "" || stepId == "" || (path == "" && action != "write-files") {
		log.Printf("File command missing pipeline_id, step_id, or path")
		return
	}

	if action != "write-files" {
		if !filepath.IsAbs(path) {
			c.sendFileCompletion(pipelineId, stepId, false, "Path must be an absolute path", nil)
			return
		}
		path = filepath.Clean(path)
	}

	var success bool
	var logMsg string
	var data interface{}

	switch action {
	case "read-file":
		info, err := os.Stat(path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("File not found or inaccessible: %v", err)
			break
		}
		if !info.Mode().IsRegular() {
			success = false
			logMsg = "Cannot read non-regular files (devices, pipes, sockets)"
			break
		}
		if info.Size() > 5*1024*1024 { // 5MB limit for WebSockets
			success = false
			logMsg = "File is too large for WebSocket transfer (>5MB). Use secure download instead."
			break
		}
		content, err := os.ReadFile(path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to read file: %v", err)
		} else {
			success = true
			logMsg = "File read successfully"
			data = base64.StdEncoding.EncodeToString(content) // Safe JSON transport
		}
	case "write-files":
		filesInter, ok := args["files"].([]interface{})
		if !ok {
			success = false
			logMsg = "files argument must be an array"
			break
		}

		txID := fmt.Sprintf("rasnuos_tx_%d", time.Now().UnixNano())
		txBaseDir := filepath.Join(os.TempDir(), txID)
		stagingDir := filepath.Join(txBaseDir, "staging")
		backupDir := filepath.Join(txBaseDir, "backup")

		os.MkdirAll(stagingDir, 0755)
		os.MkdirAll(backupDir, 0755)
		defer os.RemoveAll(txBaseDir)

		type fileOp struct {
			Target string
			Staging string
			Backup string
			HasBackup bool
			OriginalMode os.FileMode
			NewMode os.FileMode
		}
		var ops []fileOp

		hasError := false
		for i, fInter := range filesInter {
			fMap, ok := fInter.(map[string]interface{})
			if !ok {
				success = false
				logMsg = fmt.Sprintf("Item %d in files array is not an object", i)
				hasError = true
				break
			}
			fPath, _ := fMap["path"].(string)
			fContentB64, _ := fMap["content"].(string)
			
			if fPath == "" {
				continue
			}

			if !filepath.IsAbs(fPath) {
				success = false
				logMsg = fmt.Sprintf("Path must be an absolute path: %s", fPath)
				hasError = true
				break
			}
			fPath = filepath.Clean(fPath)

			fContent, err := base64.StdEncoding.DecodeString(fContentB64)
			if err != nil {
				success = false
				logMsg = fmt.Sprintf("Failed to decode base64 for %s: %v", fPath, err)
				hasError = true
				break
			}
			
			fModeStr, _ := fMap["mode"].(string)
			var fMode os.FileMode = 0644
			if fModeStr != "" {
				if parsed, err := strconv.ParseUint(fModeStr, 8, 32); err == nil {
					fMode = os.FileMode(parsed)
				}
			}

			op := fileOp{
				Target: fPath,
				Staging: filepath.Join(stagingDir, fmt.Sprintf("file_%d", i)),
				Backup: filepath.Join(backupDir, fmt.Sprintf("file_%d", i)),
				HasBackup: false,
				NewMode: fMode,
			}

			if stat, err := os.Stat(fPath); err == nil && !stat.IsDir() {
				op.HasBackup = true
				op.OriginalMode = stat.Mode()
				if err := copyFile(fPath, op.Backup, stat.Mode()); err != nil {
					success = false
					logMsg = fmt.Sprintf("Failed to backup %s: %v", fPath, err)
					hasError = true
					break
				}
			}

			if err := os.WriteFile(op.Staging, fContent, op.NewMode); err != nil {
				success = false
				logMsg = fmt.Sprintf("Failed to stage %s: %v", fPath, err)
				hasError = true
				break
			}
			ops = append(ops, op)
		}

		if hasError {
			break
		}

		commitError := false
		for _, op := range ops {
			os.MkdirAll(filepath.Dir(op.Target), 0755)
			if err := copyFile(op.Staging, op.Target, op.NewMode); err != nil {
				success = false
				logMsg = fmt.Sprintf("Failed to commit %s: %v", op.Target, err)
				commitError = true
				break
			}
		}

		if commitError {
			for _, op := range ops {
				if op.HasBackup {
					copyFile(op.Backup, op.Target, op.OriginalMode)
				} else {
					os.Remove(op.Target)
				}
			}
			break
		}

		success = true
		logMsg = "Successfully wrote array of files atomically"
	case "write-file":
		b64content := getString(args, "content")
		content, err := base64.StdEncoding.DecodeString(b64content)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to decode base64 content: %v", err)
			break
		}

		// Parse optional mode (default 0644)
		modeStr := getString(args, "mode")
		var mode os.FileMode = 0644
		if modeStr != "" {
			if parsed, err := strconv.ParseUint(modeStr, 8, 32); err == nil {
				mode = os.FileMode(parsed)
			}
		}

		err = os.WriteFile(path, content, mode)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to write file: %v", err)
		} else {
			success = true
			logMsg = "File written successfully"
		}
	case "delete-file":
		err := os.Remove(path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to delete file: %v", err)
		} else {
			success = true
			logMsg = "File deleted successfully"
		}
	case "download-file":
		url := getString(args, "url")
		if url == "" {
			success = false
			logMsg = "URL is required for download"
			break
		}
		err := downloadFile(url, path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to download file: %v", err)
		} else {
			success = true
			logMsg = "File downloaded successfully"
		}
	case "upload-file":
		url := getString(args, "url")
		if url == "" {
			success = false
			logMsg = "URL is required for upload"
			break
		}
		err := c.uploadFileStream(url, path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to upload file: %v", err)
		} else {
			success = true
			logMsg = "File uploaded successfully"
		}
	case "list-directory":
		files, err := os.ReadDir(path)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to list directory: %v", err)
		} else {
			success = true
			logMsg = "Directory listed successfully"
			var fileList []map[string]interface{}
			for i, f := range files {
				if i >= 2000 {
					logMsg = "Directory listed successfully (truncated to 2000 files)"
					break
				}
				info, err := f.Info()
				if err != nil {
					continue
				}
				isDir := f.IsDir()
				isSymlink := f.Type()&os.ModeSymlink != 0

				if isSymlink {
					targetPath := filepath.Join(path, f.Name())
					if targetInfo, err := os.Stat(targetPath); err == nil {
						isDir = targetInfo.IsDir()
					}
				}

				fileList = append(fileList, map[string]interface{}{
					"name":       f.Name(),
					"size":       info.Size(),
					"is_dir":     isDir,
					"is_symlink": isSymlink,
					"mode":       info.Mode().String(),
					"time":       info.ModTime().Unix(),
				})
			}
			data = fileList
		}
	case "create-directory":
		modeStr := getString(args, "mode")
		var mode os.FileMode = 0755
		if modeStr != "" {
			if parsed, err := strconv.ParseUint(modeStr, 8, 32); err == nil {
				mode = os.FileMode(parsed)
			}
		}
		err := os.MkdirAll(path, mode)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to create directory: %v", err)
		} else {
			success = true
			logMsg = "Directory created successfully"
		}
	case "rename-path":
		newPath := getString(args, "new_path")
		if newPath == "" {
			newPath = getString(args, "destination")
		}
		if newPath == "" {
			success = false
			logMsg = "new_path or destination is required"
			break
		}
		if !filepath.IsAbs(newPath) {
			success = false
			logMsg = "new_path must be an absolute path"
			break
		}
		newPath = filepath.Clean(newPath)
		err := os.Rename(path, newPath)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to rename path: %v", err)
		} else {
			success = true
			logMsg = "Path renamed successfully"
		}
	case "delete-path":
		cleanPath := filepath.Clean(path)
		if cleanPath == "/" || cleanPath == filepath.VolumeName(cleanPath)+"\\" {
			success = false
			logMsg = "Refusing to delete root directory"
			break
		}
		err := os.RemoveAll(cleanPath)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to delete path: %v", err)
		} else {
			success = true
			logMsg = "Path deleted successfully"
		}
	case "chmod-path":
		modeStr := getString(args, "mode")
		if modeStr == "" {
			success = false
			logMsg = "mode is required for chmod"
			break
		}
		parsed, err := strconv.ParseUint(modeStr, 8, 32)
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Invalid mode %q: must be octal (e.g. 0755)", modeStr)
			break
		}
		recursive := getBool(args, "recursive")
		if recursive {
			err = filepath.Walk(path, func(p string, info os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				return os.Chmod(p, os.FileMode(parsed))
			})
		} else {
			err = os.Chmod(path, os.FileMode(parsed))
		}
		if err != nil {
			success = false
			logMsg = fmt.Sprintf("Failed to chmod path: %v", err)
		} else {
			success = true
			logMsg = "Path permissions updated successfully"
		}
	case "chown-path":
		uid := getInt(args, "uid")
		gid := getInt(args, "gid")
		owner := getString(args, "owner")
		if owner != "" {
			if u, err := user.Lookup(owner); err == nil {
				if parsedUID, err := strconv.Atoi(u.Uid); err == nil {
					uid = parsedUID
				}
				if parsedGID, err := strconv.Atoi(u.Gid); err == nil && gid == 0 {
					gid = parsedGID
				}
			}
		}
		group := getString(args, "group")
		if group != "" {
			if g, err := user.LookupGroup(group); err == nil {
				if parsedGID, err := strconv.Atoi(g.Gid); err == nil {
					gid = parsedGID
				}
			}
		}

		recursive := getBool(args, "recursive")
		if recursive {
			err := filepath.Walk(path, func(p string, info os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				return os.Chown(p, uid, gid)
			})
			if err != nil {
				success = false
				logMsg = fmt.Sprintf("Failed to chown path recursively: %v", err)
			} else {
				success = true
				logMsg = "Path owner updated successfully"
			}
		} else {
			err := os.Chown(path, uid, gid)
			if err != nil {
				success = false
				logMsg = fmt.Sprintf("Failed to chown path: %v", err)
			} else {
				success = true
				logMsg = "Path owner updated successfully"
			}
		}
	default:
		success = false
		logMsg = "Unknown file action"
	}

	if !success {
		logAuditEvent(AuditEvent{Action: "file_" + action, PipelineID: pipelineId, StepID: stepId, Command: path, Success: false, Error: logMsg})
	} else {
		logAuditEvent(AuditEvent{Action: "file_" + action, PipelineID: pipelineId, StepID: stepId, Command: path, Success: true})
	}

	c.sendFileCompletion(pipelineId, stepId, success, logMsg, data)
}

func (c *PusherClient) uploadFileStream(url, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()
		part, err := writer.CreateFormFile("file", filepath.Base(filePath))
		if err != nil {
			return
		}
		io.Copy(part, file)
	}()

	req, err := http.NewRequest("POST", url, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := getDefaultHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func (c *PusherClient) sendFileCompletion(pipelineId string, stepId string, success bool, finalLog string, data interface{}) {
	completionURL := fmt.Sprintf("%s/agent/file/complete", c.ApiURL)

	reqBody, err := json.Marshal(map[string]interface{}{
		"pipeline_id": pipelineId,
		"step_id":     stepId,
		"success":     success,
		"log":         finalLog,
		"data":        data,
	})
	if err != nil {
		log.Printf("Error marshaling file completion request: %v", err)
		return
	}

	req, err := http.NewRequest("POST", completionURL, bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Error creating file completion request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := getDefaultHTTPClient().Do(req)
	if err != nil {
		log.Printf("Error sending file completion: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("File completion failed with status %d", resp.StatusCode)
	} else {
		log.Printf("Successfully reported file completion for step %s", stepId)
	}
}

func (c *PusherClient) uploadFileToDashboard(requestId, filePath, uploadUrl string) {
	log.Printf("Uploading file %s to %s", filePath, uploadUrl)
	file, err := os.Open(filePath)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to open file for upload: %v", err))
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to stat file: %v", err))
		return
	}

	fileSize := stat.Size()
	chunkSize := int64(5 * 1024 * 1024) // 5MB chunks
	totalPages := int(math.Ceil(float64(fileSize) / float64(chunkSize)))
	if totalPages == 0 {
		totalPages = 1
	}

	// Compute file hash
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to compute file hash: %v", err))
		return
	}
	fileHash := hex.EncodeToString(hash.Sum(nil))

	for i := 0; i < totalPages; i++ {
		offset := int64(i) * chunkSize
		file.Seek(offset, io.SeekStart)

		limitReader := io.LimitReader(file, chunkSize)

		url := fmt.Sprintf("%s?chunk=%d&total=%d", uploadUrl, i, totalPages)
		req, err := http.NewRequest("POST", url, limitReader)
		if err != nil {
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to create upload request: %v", err))
			return
		}

		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("X-File-Hash", fileHash)

		resp, err := fileTransferHTTPClient.Do(req)
		if err != nil {
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to upload chunk %d: %v", i, err))
			return
		}

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("Dashboard rejected file upload [%d]: %s", resp.StatusCode, string(body)))
			return
		}
		resp.Body.Close()
	}

	c.sendCompletion("sync", requestId, true, fmt.Sprintf("File %s uploaded successfully", filePath))
	logAuditEvent(AuditEvent{Action: "upload_file_to_dashboard", StepID: requestId, Command: filePath, Success: true})
}

func (c *PusherClient) downloadFileFromDashboard(requestId, targetPath, downloadUrl string) {
	log.Printf("Downloading file from %s to %s", downloadUrl, targetPath)

	req, err := http.NewRequest("GET", downloadUrl, nil)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to create download request: %v", err))
		logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := fileTransferHTTPClient.Do(req)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to download file: %v", err))
		logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Dashboard rejected file download [%d]: %s", resp.StatusCode, string(body)))
		logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: string(body)})
		return
	}

	out, err := os.Create(targetPath)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to create target file %s: %v", targetPath, err))
		logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: err.Error()})
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to write to file %s: %v", targetPath, err))
		logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: err.Error()})
		return
	}

	// Verify file hash if provided by dashboard
	expectedHash := resp.Header.Get("X-File-Hash")
	if expectedHash != "" {
		out.Seek(0, io.SeekStart)
		hash := sha256.New()
		if _, err := io.Copy(hash, out); err != nil {
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to compute downloaded file hash: %v", err))
			logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: err.Error()})
			return
		}
		actualHash := hex.EncodeToString(hash.Sum(nil))
		if actualHash != expectedHash {
			os.Remove(targetPath)
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("File integrity check failed (SHA256 mismatch). Expected: %s, Actual: %s", expectedHash, actualHash))
			logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: false, Error: "SHA256 hash mismatch"})
			return
		}
	}

	c.sendCompletion("sync", requestId, true, fmt.Sprintf("File %s downloaded successfully", targetPath))
	logAuditEvent(AuditEvent{Action: "download_file_from_dashboard", StepID: requestId, Command: targetPath, Success: true})
}
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
