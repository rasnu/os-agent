package main

import (
	"regexp"
)

// destructivePatterns defines regex patterns for commands that are prohibited in Strict Mode
// across Linux, macOS (Darwin), and Windows platforms.
var destructivePatterns = []*regexp.Regexp{
	// ─────────────────────────────────────────────────────────────────────────────
	// LINUX & UNIX & MACOS COMMON DESTRUCTIVE PATTERNS
	// ─────────────────────────────────────────────────────────────────────────────

	// rm -r/-R targeting root directory (/ or /* or /// or /.) with flags before target
	// Matches: rm -rf /, \rm -rf /, "rm" -rf /, /bin/rm -rf /, rm "-rf" /, rm -rf \/, rm -rf /./*, rm -rf />/dev/null, etc.
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b[^;&|]*?\s+["']?\\?/(?:[/*.]*["']?(?:$|[\s;&|><#]))`),

	// rm targeting root directory where target appears before flags (e.g. rm / -rf, rm /* -rf, rm / --recursive)
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+["']?\\?/(?:[/*.]*["']?)\s+[^;&|]*?(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b`),

	// rm -r/-R targeting critical system directories (/etc, /boot, /bin, /sbin, /usr, /lib, /lib64, /root, /sys, /proc, /dev, /var)
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b[^;&|]*?\s+["']?/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)(?:/|\*/?|/\*)?["']?(?:\s|$|[;&|><#])`),

	// rm targeting critical system directories before flags
	regexp.MustCompile(`(?i)(?:^|[^\w'"])(?:["']?rm["']?|\\rm)\b[^;&|]*?\s+["']?/(?:etc|boot|bin|sbin|usr|lib|lib64|root|sys|proc|dev|var)(?:/|\*/?|/\*)?["']?\s+[^;&|]*?(?:["']?-[a-z0-9_-]*r[a-z0-9_-]*["']?|["']?--recursive["']?)\b`),

	// find targeting root with -delete
	regexp.MustCompile(`(?i)\bfind\s+["']?/+[\*.]*["']?\s+[^;&|]*?-delete\b`),

	// Filesystem formatting (Linux/macOS)
	regexp.MustCompile(`(?i)\bmkfs(?:\.[a-z0-9]+)?\b`),

	// dd zero-wipe or disk wipe of raw block device
	regexp.MustCompile(`(?i)\bdd\s+.*(?:if=/dev/(?:zero|urandom)|of=/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9]))\b`),

	// shred raw block device
	regexp.MustCompile(`(?i)\bshred\s+.*\/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),

	// wipefs raw block device
	regexp.MustCompile(`(?i)\bwipefs\s+.*\/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),

	// Direct raw block device overwrite redirection
	regexp.MustCompile(`(?:^|[\s;&|])>\s*/dev/(?:sd[a-z]|nvme[0-9]|vd[a-z]|hd[a-z]|xvd[a-z]|disk[0-9])\b`),

	// Bash / Shell fork bomb
	regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&?\s*\}\s*;\s*:`),

	// Deleting root user account
	regexp.MustCompile(`(?i)\buserdel\s+.*-r\b.*\broot\b`),

	// Deleting or truncating authorized_keys (SSH lockout)
	regexp.MustCompile(`(?i)\brm\s+[^;&|]*authorized_keys\b`),
	regexp.MustCompile(`(?:^|[\s;&|])>\s*[^;&|]*authorized_keys\b`),

	// chmod 777 on root or system root paths
	regexp.MustCompile(`(?i)\bchmod\s+(?:-[a-z]*R[a-z]*\s+)?777\s+/`),

	// ─────────────────────────────────────────────────────────────────────────────
	// WINDOWS (POWERSHELL & CMD) DESTRUCTIVE PATTERNS
	// ─────────────────────────────────────────────────────────────────────────────

	// PowerShell Remove-Item flags before target: Remove-Item -Recurse -Force C:\
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*-(?:Recurse|r)\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	// PowerShell Remove-Item target before flags: Remove-Item C:\ -Recurse -Force
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*-(?:Recurse|r)\b`),
	// PowerShell Remove-Item targeting Windows system directories
	regexp.MustCompile(`(?i)\bRemove-Item\s+[^;&|]*(?:\\\\|\\)(?:Windows|System32|Boot)\b`),

	// Windows CMD rd / rmdir /s on drive root (e.g. rd /s /q C:\ or rmdir /s C:\)
	regexp.MustCompile(`(?i)\b(?:rmdir|rd)\s+[^;&|]*\/s\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	regexp.MustCompile(`(?i)\b(?:rmdir|rd)\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*\/s\b`),

	// Windows CMD del /s on drive root (e.g. del /s /q C:\ or del C:\* /s)
	regexp.MustCompile(`(?i)\bdel\s+[^;&|]*\/s\b[^;&|]*\s+["']?[A-Za-z]:\\?[\*.]*["']?(?:\s|$|[;&|><#])`),
	regexp.MustCompile(`(?i)\bdel\s+[^;&|]*["']?[A-Za-z]:\\?[\*.]*["']?\s+[^;&|]*\/s\b`),

	// Windows format drive command (format C: /q or format C: /y)
	regexp.MustCompile(`(?i)\bformat\s+[A-Za-z]:(?:\s|/|\z)`),

	// PowerShell Format-Volume
	regexp.MustCompile(`(?i)\bFormat-Volume\b`),

	// Windows Volume Shadow Copy deletion (ransomware indicator)
	regexp.MustCompile(`(?i)\bvssadmin\s+delete\s+shadows\b`),

	// Windows Defender real-time monitoring disable
	regexp.MustCompile(`(?i)\bSet-MpPreference\s+.*-DisableRealtimeMonitoring\b`),

	// Windows Event log wiping
	regexp.MustCompile(`(?i)\bClear-EventLog\b`),

	// Windows Firewall disable
	regexp.MustCompile(`(?i)\bSet-NetFirewallProfile\s+.*-Enabled\s+(?:False|0)\b`),
	regexp.MustCompile(`(?i)\bStop-Service\s+(?:-Name\s+)?MpsSvc\b`),

	// ─────────────────────────────────────────────────────────────────────────────
	// MACOS SPECIFIC DESTRUCTIVE PATTERNS
	// ─────────────────────────────────────────────────────────────────────────────

	// macOS diskutil disk destruction
	regexp.MustCompile(`(?i)\bdiskutil\s+(?:eraseDisk|apfs\s+delete\w*|secureErase)\b`),

	// macOS System Integrity Protection disable/clear
	regexp.MustCompile(`(?i)\bcsrutil\s+(?:disable|clear)\b`),

	// macOS packet filter firewall disable/flush
	regexp.MustCompile(`(?i)\bpfctl\s+(?:-[a-z]*[dF][a-z]*)\b`),
}

// isCommandDestructive checks if a command matches any destructive pattern.
// Returns true and the matched pattern string if blocked, false and "" otherwise.
func isCommandDestructive(command string) (bool, string) {
	for _, re := range destructivePatterns {
		if re.MatchString(command) {
			return true, re.String()
		}
	}
	return false, ""
}
