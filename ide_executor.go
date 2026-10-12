package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

type IdeIntentPayload struct {
	Action             string             `json:"action"`
	AbsolutePath       string             `json:"absolute_path,omitempty"`
	TargetFile         string             `json:"target_file,omitempty"`
	StartLine          int                `json:"start_line,omitempty"`
	EndLine            int                `json:"end_line,omitempty"`
	TargetContent      string             `json:"target_content,omitempty"`
	ReplacementContent string             `json:"replacement_content,omitempty"`
	ReplacementChunks  []ReplacementChunk `json:"replacement_chunks,omitempty"`
	CodeContent        string             `json:"code_content,omitempty"`
}

type ReplacementChunk struct {
	StartLine          int    `json:"start_line"`
	EndLine            int    `json:"end_line"`
	TargetContent      string `json:"target_content"`
	ReplacementContent string `json:"replacement_content"`
}

func (c *PusherClient) executeIdeIntent(pipelineId, stepId, cmdStr string) {
	// Strip the prefix
	jsonStr := strings.TrimSpace(strings.TrimPrefix(cmdStr, "[IDE_INTENT]"))
	var payload IdeIntentPayload
	if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("=== IDE EXECUTION FAILED ===\nInvalid JSON payload: %v\n", err), nil)
		c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("IDE EXECUTION FAILED: Invalid JSON payload: %v", err))
		return
	}

	var output string
	var err error

	switch payload.Action {
	case "view_file":
		output, err = handleViewFile(payload)
	case "replace_file_content":
		// Convert to a single chunk for multi_replace logic
		payload.ReplacementChunks = []ReplacementChunk{
			{
				StartLine:          payload.StartLine,
				EndLine:            payload.EndLine,
				TargetContent:      payload.TargetContent,
				ReplacementContent: payload.ReplacementContent,
			},
		}
		output, err = handleMultiReplaceFileContent(payload)
	case "multi_replace_file_content":
		output, err = handleMultiReplaceFileContent(payload)
	case "write_to_file":
		output, err = handleWriteToFile(payload)
	default:
		err = fmt.Errorf("unknown IDE action: %s", payload.Action)
	}

	if err != nil {
		c.streamLogWS(pipelineId, stepId, fmt.Sprintf("=== IDE EXECUTION FAILED ===\n%v\n", err), nil)
		// Fix 1.6: Same as above — must route to /agent/complete, not /agent/migration/complete
		c.sendCompletion(pipelineId, stepId, false, fmt.Sprintf("IDE EXECUTION FAILED: %v", err))
		return
	}

	c.streamLogWS(pipelineId, stepId, fmt.Sprintf("=== IDE EXECUTION SUCCESS ===\n%s\n", output), nil)
	// Fix 1.6: Route success completion through AgentCompleteController via sendCompletion
	c.sendCompletion(pipelineId, stepId, true, output)
}

func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func normalizeCodeBlockForMatch(content string) string {
	lines := strings.Split(normalizeLineEndings(content), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func handleViewFile(payload IdeIntentPayload) (string, error) {
	if payload.AbsolutePath == "" {
		return "", fmt.Errorf("absolute_path is required")
	}

	info, statErr := os.Stat(payload.AbsolutePath)
	if statErr != nil {
		return "", fmt.Errorf("failed to access file: %v", statErr)
	}
	if info.Size() > 5*1024*1024 {
		return "", fmt.Errorf("file is too large to view (%d bytes). Maximum supported size is 5MB to prevent agent OOM crashes", info.Size())
	}

	data, err := os.ReadFile(payload.AbsolutePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	lines := strings.Split(normalizeLineEndings(string(data)), "\n")

	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return fmt.Sprintf("File: %s is empty.\n", payload.AbsolutePath), nil
	}

	start := payload.StartLine - 1
	if start < 0 {
		start = 0
	}
	end := payload.EndLine
	if end == 0 || end > len(lines) {
		end = len(lines)
	}

	if start >= len(lines) || start >= end {
		return "", fmt.Errorf("invalid line range: %d to %d (file has %d lines)", payload.StartLine, payload.EndLine, len(lines))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s (Lines %d to %d)\n", payload.AbsolutePath, start+1, end))
	sb.WriteString("----------------------------------------\n")
	for i := start; i < end; i++ {
		sb.WriteString(fmt.Sprintf("%4d | %s\n", i+1, lines[i]))
	}
	sb.WriteString("----------------------------------------\n")

	return sb.String(), nil
}

func handleMultiReplaceFileContent(payload IdeIntentPayload) (string, error) {
	if payload.TargetFile == "" {
		return "", fmt.Errorf("target_file is required")
	}
	if len(payload.ReplacementChunks) == 0 {
		return "", fmt.Errorf("no replacement chunks provided")
	}

	target := payload.TargetFile

	// CRITICAL SECURITY FIX: Normalize the path to resolve all ../ and mixed slashes
	cleanTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %v", err)
	}

	// CRITICAL SECURITY FIX 10: Resolve Symlinks to prevent bypassing jails via symlink attacks
	resolvedTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(cleanTarget))
			if parentErr == nil {
				resolvedTarget = filepath.Join(resolvedParent, filepath.Base(cleanTarget))
			} else {
				resolvedTarget = cleanTarget
			}
		} else {
			resolvedTarget = cleanTarget
		}
	}

	payload.TargetFile = resolvedTarget
	lowerTarget := strings.ToLower(resolvedTarget)

	// Normalize Windows backslashes to forward slashes for unified prefix checking
	normalizedLowerTarget := filepath.ToSlash(lowerTarget)

	// Safety & Security: Enforce directory jail checks
	restrictedPrefixes := []string{
		"/etc/", "/usr/", "/root/", "/bin/", "/sbin/", "/var/",
		"c:/windows/", "c:/program files/", "c:/program files (x86)/",
	}

	for _, prefix := range restrictedPrefixes {
		if strings.HasPrefix(normalizedLowerTarget, prefix) {
			return "", fmt.Errorf("permission denied: path %s targets a restricted system directory (%s)", target, prefix)
		}
	}

	info, statErr := os.Stat(payload.TargetFile)
	if statErr != nil {
		return "", fmt.Errorf("failed to access file: %v", statErr)
	}
	if info.Size() > 5*1024*1024 {
		return "", fmt.Errorf("file is too large to edit (%d bytes). Maximum supported size is 5MB to prevent agent OOM crashes", info.Size())
	}

	data, err := os.ReadFile(payload.TargetFile)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	originalContent := normalizeLineEndings(string(data))
	lines := strings.Split(originalContent, "\n")

	// Check for overlapping chunks before processing
	for i := 0; i < len(payload.ReplacementChunks)-1; i++ {
		for j := i + 1; j < len(payload.ReplacementChunks); j++ {
			c1 := payload.ReplacementChunks[i]
			c2 := payload.ReplacementChunks[j]
			if c1.StartLine <= c2.EndLine && c1.EndLine >= c2.StartLine {
				return "", fmt.Errorf("chunk %d (lines %d-%d) overlaps with chunk %d (lines %d-%d). Overlapping chunks are not allowed in a single operation", i+1, c1.StartLine, c1.EndLine, j+1, c2.StartLine, c2.EndLine)
			}
		}
	}

	// First, verify ALL chunks before making ANY changes.
	for i, chunk := range payload.ReplacementChunks {
		start := chunk.StartLine - 1
		end := chunk.EndLine
		if start < 0 || start >= len(lines) || end < start || end > len(lines) {
			return "", fmt.Errorf("chunk %d has invalid line range: %d to %d (file has %d lines)", i+1, chunk.StartLine, chunk.EndLine, len(lines))
		}

		actualTargetLines := lines[start:end]
		actualTargetContent := strings.Join(actualTargetLines, "\n")
		expectedTargetContent := normalizeLineEndings(chunk.TargetContent)
		expectedNorm := normalizeCodeBlockForMatch(expectedTargetContent)

		// Expected length in lines
		expectedLen := len(strings.Split(expectedNorm, "\n"))

		// Enterprise Fuzzy Matching: Trim trailing spaces/tabs on EVERY line to prevent LLM trailing whitespace hallucinations
		if normalizeCodeBlockForMatch(actualTargetContent) != expectedNorm {
			// FALLBACK: The LLM probably hallucinated the line numbers.
			// Let's slide a window across the entire file to find the correct start and end lines.
			found := false
			for searchStart := 0; searchStart <= len(lines)-expectedLen; searchStart++ {
				searchEnd := searchStart + expectedLen
				windowContent := strings.Join(lines[searchStart:searchEnd], "\n")
				if normalizeCodeBlockForMatch(windowContent) == expectedNorm {
					// We found the correct lines! Auto-correct the payload chunk so the replacement logic works later.
					payload.ReplacementChunks[i].StartLine = searchStart + 1
					payload.ReplacementChunks[i].EndLine = searchEnd
					found = true
					break
				}
			}

			if !found {
				return "", fmt.Errorf("chunk %d validation failed: target_content does not match the file content at lines %d-%d, and could not be found anywhere else in the file.\nExpected:\n%s\n\nActual in file at given lines:\n%s", i+1, chunk.StartLine, chunk.EndLine, expectedTargetContent, actualTargetContent)
			}
		}
	}

	// Create persistent backup for Undo feature
	backupPath := fmt.Sprintf("%s.bak_rasnuos_%d", payload.TargetFile, time.Now().Unix())
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to create atomic backup: %v", err)
	}

	defer func() {
		// We purposefully do NOT delete the backup so the UI can "Undo" it later.
	}()

	// Create a copy of chunks to sort by StartLine descending
	chunks := make([]ReplacementChunk, len(payload.ReplacementChunks))
	copy(chunks, payload.ReplacementChunks)

	for i := 0; i < len(chunks)-1; i++ {
		for j := i + 1; j < len(chunks); j++ {
			if chunks[i].StartLine < chunks[j].StartLine {
				chunks[i], chunks[j] = chunks[j], chunks[i]
			}
		}
	}

	// Now apply from bottom to top
	for _, chunk := range chunks {
		start := chunk.StartLine - 1
		end := chunk.EndLine

		replacementLines := strings.Split(normalizeLineEndings(chunk.ReplacementContent), "\n")

		newLines := make([]string, 0, len(lines)-(end-start)+len(replacementLines))
		newLines = append(newLines, lines[:start]...)
		newLines = append(newLines, replacementLines...)
		newLines = append(newLines, lines[end:]...)

		lines = newLines
	}

	// Preserve original line endings (CRLF vs LF)
	lineEnding := "\n"
	if strings.Contains(string(data), "\r\n") {
		lineEnding = "\r\n"
	}
	newContent := strings.Join(lines, lineEnding)

	// Preserve original file permissions
	fileMode := os.FileMode(0644)
	if info, err := os.Stat(payload.TargetFile); err == nil {
		fileMode = info.Mode()
	}

	// Write in-place using O_TRUNC to perfectly preserve original file ownership, permissions, and inode
	file, err := os.OpenFile(payload.TargetFile, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return "", fmt.Errorf("failed to open file for writing: %v", err)
	}

	if _, err := file.Write([]byte(newContent)); err != nil {
		file.Close()
		// CRITICAL: If write fails midway, restore from memory backup
		if restoreErr := os.WriteFile(payload.TargetFile, data, fileMode); restoreErr != nil {
			return "", fmt.Errorf("FATAL DISK ERROR: failed to write new content (%v) AND failed to restore memory backup (%v). The file is likely corrupted. A manual backup exists at %s", err, restoreErr, backupPath)
		}
		return "", fmt.Errorf("failed to write new content, restored backup: %v", err)
	}
	file.Close()

	// Generate Unified Diff (ensure we strip \r so the markdown doesn't double-space)
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(normalizeLineEndings(originalContent)),
		B:        difflib.SplitLines(normalizeLineEndings(newContent)),
		FromFile: payload.TargetFile + " (Old)",
		ToFile:   payload.TargetFile + " (New)",
		Context:  3,
	}

	diffText, _ := difflib.GetUnifiedDiffString(diff)
	if diffText == "" {
		diffText = "No changes were made."
	}

	return fmt.Sprintf("Successfully updated %s\n\n```diff\n%s\n```", payload.TargetFile, diffText), nil
}

func handleWriteToFile(payload IdeIntentPayload) (string, error) {
	if payload.TargetFile == "" {
		return "", fmt.Errorf("target_file is required")
	}
	if payload.CodeContent == "" && payload.ReplacementContent != "" {
		payload.CodeContent = payload.ReplacementContent
	}
	if payload.CodeContent == "" {
		return "", fmt.Errorf("code_content is required")
	}

	target := payload.TargetFile

	// CRITICAL SECURITY FIX: Normalize the path to resolve all ../ and mixed slashes
	// Otherwise //etc/passwd or c:/windows bypassing HasPrefix check!
	cleanTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %v", err)
	}

	// CRITICAL SECURITY FIX 10: Resolve Symlinks to prevent bypassing jails via symlink attacks
	resolvedTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(cleanTarget))
			if parentErr == nil {
				resolvedTarget = filepath.Join(resolvedParent, filepath.Base(cleanTarget))
			} else {
				resolvedTarget = cleanTarget
			}
		} else {
			resolvedTarget = cleanTarget
		}
	}

	target = resolvedTarget
	lowerTarget := strings.ToLower(target)

	// Normalize Windows backslashes to forward slashes for unified prefix checking
	normalizedLowerTarget := filepath.ToSlash(lowerTarget)

	// Safety & Security: Enforce directory jail checks
	restrictedPrefixes := []string{
		"/etc/", "/usr/", "/root/", "/bin/", "/sbin/", "/var/",
		"c:/windows/", "c:/program files/", "c:/program files (x86)/",
	}

	for _, prefix := range restrictedPrefixes {
		if strings.HasPrefix(normalizedLowerTarget, prefix) {
			return "", fmt.Errorf("permission denied: path %s targets a restricted system directory (%s)", target, prefix)
		}
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %v", err)
	}

	var originalContent string
	if info, err := os.Stat(target); err == nil {
		if info.Size() > 5*1024*1024 {
			return "", fmt.Errorf("target file already exists and is too large (%d bytes). Refusing to overwrite a massive file to prevent accidental binary corruption", info.Size())
		}
		data, readErr := os.ReadFile(target)
		if readErr == nil {
			originalContent = normalizeLineEndings(string(data))
			backupPath := fmt.Sprintf("%s.bak_rasnuos_%d", target, time.Now().Unix())
			os.WriteFile(backupPath, data, info.Mode())
		}
	}

	contentToWrite := normalizeLineEndings(payload.CodeContent)
	if err := os.WriteFile(target, []byte(contentToWrite), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %v", err)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(originalContent),
		B:        difflib.SplitLines(contentToWrite),
		FromFile: target + " (Old)",
		ToFile:   target + " (New)",
		Context:  3,
	}
	diffText, _ := difflib.GetUnifiedDiffString(diff)
	if diffText == "" {
		diffText = "No changes were made."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Successfully written to: %s\n", target))
	if originalContent == "" {
		sb.WriteString("File created from scratch.\n\n")
	} else {
		sb.WriteString("Existing file overwritten.\n\n")
	}
	sb.WriteString("```diff\n")
	sb.WriteString(diffText)
	sb.WriteString("\n```")

	return sb.String(), nil
}
