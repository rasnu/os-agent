// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package guardrails

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf16"
)

// destructivePatterns lists pre-compiled regex rules for commands blocked across
// Linux, macOS, and Windows hosts regardless of control plane state.
var destructivePatterns = []*regexp.Regexp{
	// rm -r/-R targeting / (flags before path, including quoted/escaped binaries and flags).
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b[^;&|]*?\s+["']?\\?/(?:[/*.]*["']?(?:$|[\s;&|><#]))`),

	// rm targeting / where path precedes flags (e.g. rm / -rf, rm /* --recursive).
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+["']?\\?/(?:[/*.]*["']?)\s+[^;&|]*?(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b`),

	// rm -r/-R targeting top-level OS directories (/etc, /boot, /bin, /sbin, /usr, /lib, /lib64, /root, /sys, /proc, /dev, /var).
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b[^;&|]*?\s+["']?/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)(?:/|\*/?|/\*)?["']?(?:\s|$|[;&|><#])`),

	// rm targeting top-level OS directories where path precedes flags.
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+["']?/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)(?:/|\*/?|/\*)?["']?\s+[^;&|]*?(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b`),

	// find / -delete.
	regexp.MustCompile(`(?i)\bfind\s+["']?/+[\*.]*["']?\s+[^;&|]*?-delete\b`),

	// Filesystem formatting utilities.
	regexp.MustCompile(`(?i)\bmkfs(?:\.[a-z0-9]+)?\b`),

	// Raw block device wipes via dd, shred, wipefs, or shell redirection.
	regexp.MustCompile(`(?i)\bdd\s+.*(?:if=/dev/(?:zero|urandom)|of=/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9]))\b`),
	regexp.MustCompile(`(?i)\bshred\s+.*\/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),
	regexp.MustCompile(`(?i)\bwipefs\s+.*\/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),
	regexp.MustCompile(`(?:^|[\s;&|])>\s*/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),

	// Shell fork bomb.
	regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&?\s*\}\s*;\s*:`),

	// Root account removal, authorized_keys destruction, and recursive root permission changes.
	regexp.MustCompile(`(?i)\buserdel\s+.*-r\b.*\broot\b`),
	regexp.MustCompile(`(?i)\brm\s+[^;&|]*authorized_keys\b`),
	regexp.MustCompile(`(?:^|[\s;&|])>\s*[^;&|]*authorized_keys\b`),
	regexp.MustCompile(`(?i)\bchmod\s+(?:-[a-z]*R[a-z]*\s+)?777\s+/`),

	// PowerShell Remove-Item against drive roots or Windows system directories.
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*-(?:Recurse|r)\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*-(?:Recurse|r)\b`),
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*(?:\\\\|\\)(?:Windows|System32|Boot)\b`),

	// Windows CMD rd/rmdir/del recursive drive root deletion and volume formatting.
	regexp.MustCompile(`(?i)\b(?:rmdir|rd)\s+[^;&|]*\/s\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	regexp.MustCompile(`(?i)\b(?:rmdir|rd)\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*\/s\b`),
	regexp.MustCompile(`(?i)\bdel\s+[^;&|]*\/s\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	regexp.MustCompile(`(?i)\bdel\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*\/s\b`),
	regexp.MustCompile(`(?i)\bformat\s+[A-Za-z]:(?:\s|/|\z)`),
	regexp.MustCompile(`(?i)\bFormat-Volume\b`),

	// Windows shadow copy deletion, Defender/Firewall tampering, and event log clearing.
	regexp.MustCompile(`(?i)\bvssadmin\s+delete\s+shadows\b`),
	regexp.MustCompile(`(?i)\bSet-MpPreference\s+.*-DisableRealtimeMonitoring\b`),
	regexp.MustCompile(`(?i)\bClear-EventLog\b`),
	regexp.MustCompile(`(?i)\bSet-NetFirewallProfile\s+.*-Enabled\s+(?:False|0)\b`),
	regexp.MustCompile(`(?i)\bStop-Service\s+(?:-Name\s+)?MpsSvc\b`),

	// macOS disk erasure, SIP disablement, and PF firewall flush/disable.
	regexp.MustCompile(`(?i)\bdiskutil\s+(?:eraseDisk|apfs\s+delete\w*|secureErase)\b`),
	regexp.MustCompile(`(?i)\bcsrutil\s+(?:disable|clear)\b`),
	regexp.MustCompile(`(?i)\bpfctl\s+(?:-[a-z]*[dF][a-z]*)\b`),

	// Obfuscated decode-to-shell pipes (base64 -d | sh, xxd -r | bash, etc.).
	regexp.MustCompile(`(?i)\b(?:base64\s+(?:-d|--decode|-D)|xxd\s+-r|openssl\s+(?:enc|base64)\s+-d)\b[^;&]*\|\s*(?:sudo\s+)?(?:sh|bash|zsh|dash|ksh|pwsh|powershell|iex|Invoke-Expression)\b`),

	// Scripting language root-wipe one-liners (Python shutil.rmtree('/'), Ruby FileUtils.rm_rf('/'), Node fs.rmSync('/')).
	regexp.MustCompile(`(?i)\bshutil\.rmtree\s*\(\s*["']/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)?/*["']`),
	regexp.MustCompile(`(?i)\bFileUtils\.rm_rf\s*\(\s*["']/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)?/*["']`),
	regexp.MustCompile(`(?i)\bfs\.(?:rmSync|rmdirSync)\s*\(\s*["']/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)?/*["']`),
}

// base64TokenRegex finds candidate Base64 blobs (at least 8 chars, starting with an alphanumeric
// character after a non-path boundary) inside commands so encoded payloads
// (e.g. `eval $(echo cm0gLXJmIC8= | base64 -d)` or `powershell -enc ...`)
// can be decoded and inspected recursively without matching Unix file paths.
var base64TokenRegex = regexp.MustCompile(`(?:^|[\s"'=:(])([A-Za-z0-9][A-Za-z0-9+/]{7,}={0,2})`)

func decodeUTF16LE(b []byte) string {
	if len(b) < 2 || len(b)%2 != 0 {
		return ""
	}
	// Check if odd bytes are mostly 0x00 (typical ASCII-in-UTF16LE used by PowerShell -EncodedCommand).
	zeroCount := 0
	pairCount := len(b) / 2
	u16 := make([]uint16, pairCount)
	for i := 0; i < pairCount; i++ {
		if b[2*i+1] == 0 {
			zeroCount++
		}
		u16[i] = uint16(b[2*i]) | (uint16(b[2*i+1]) << 8)
	}
	if zeroCount*2 < pairCount {
		return ""
	}
	return string(utf16.Decode(u16))
}

func isDestructiveDepth(command string, depth int) (bool, string) {
	for _, re := range destructivePatterns {
		if re.MatchString(command) {
			return true, re.String()
		}
	}

	if depth >= 3 {
		return false, ""
	}

	for _, match := range base64TokenRegex.FindAllStringSubmatch(command, 16) {
		if len(match) < 2 {
			continue
		}
		candidate := match[1]
		raw, err := base64.StdEncoding.DecodeString(candidate)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(candidate, "="))
			if err != nil {
				continue
			}
		}
		if len(raw) == 0 {
			continue
		}
		// Inspect both UTF-8 and UTF-16LE (PowerShell -EncodedCommand) representations.
		if blocked, pat := isDestructiveDepth(string(raw), depth+1); blocked {
			return true, "encoded:" + pat
		}
		if utf16Str := decodeUTF16LE(raw); utf16Str != "" {
			if blocked, pat := isDestructiveDepth(utf16Str, depth+1); blocked {
				return true, "encoded_utf16le:" + pat
			}
		}
	}

	return false, ""
}

// IsDestructive reports whether command matches any prohibited pattern (directly or via
// Base64 / UTF-16LE encoding) and returns the matched regular expression string when blocked.
func IsDestructive(command string) (bool, string) {
	return isDestructiveDepth(command, 0)
}
