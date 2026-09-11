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
	"path/filepath"
	"strconv"
)

func (c *PusherClient) handleFileCommand(action string, args map[string]interface{}) {
	pipelineId := getString(args, "pipeline_id")
	stepId := getString(args, "step_id")
	path := getString(args, "path")

	if pipelineId == "" || stepId == "" || path == "" {
		log.Printf("File command missing pipeline_id, step_id, or path")
		return
	}

	if !filepath.IsAbs(path) {
		c.sendFileCompletion(pipelineId, stepId, false, "Path must be an absolute path", nil)
		return
	}

	path = filepath.Clean(path)

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
	default:
		success = false
		logMsg = "Unknown file action"
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

	resp, err := defaultHTTPClient.Do(req)
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

	resp, err := defaultHTTPClient.Do(req)
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
}

func (c *PusherClient) downloadFileFromDashboard(requestId, targetPath, downloadUrl string) {
	log.Printf("Downloading file from %s to %s", downloadUrl, targetPath)

	req, err := http.NewRequest("GET", downloadUrl, nil)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to create download request: %v", err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := fileTransferHTTPClient.Do(req)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to download file: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Dashboard rejected file download [%d]: %s", resp.StatusCode, string(body)))
		return
	}

	out, err := os.Create(targetPath)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to create target file: %v", err))
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to save downloaded file: %v", err))
		return
	}

	// Verify file hash if provided by dashboard
	expectedHash := resp.Header.Get("X-File-Hash")
	if expectedHash != "" {
		out.Seek(0, io.SeekStart)
		hash := sha256.New()
		if _, err := io.Copy(hash, out); err != nil {
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("Failed to compute downloaded file hash: %v", err))
			return
		}
		actualHash := hex.EncodeToString(hash.Sum(nil))
		if actualHash != expectedHash {
			os.Remove(targetPath)
			c.sendCompletion("sync", requestId, false, fmt.Sprintf("File integrity check failed (SHA256 mismatch). Expected: %s, Actual: %s", expectedHash, actualHash))
			return
		}
	}

	c.sendCompletion("sync", requestId, true, fmt.Sprintf("File %s downloaded successfully", targetPath))
}
