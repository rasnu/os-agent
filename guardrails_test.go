package main

import (
	"testing"
)

func TestIsCommandDestructive(t *testing.T) {
	testCases := []struct {
		name        string
		command     string
		expectBlock bool
	}{
		// ─────────────────────────────────────────────────────────────────────────────
		// LINUX & UNIX ROOT TARGETING (CRITICAL S1)
		// ─────────────────────────────────────────────────────────────────────────────
		{"rm -rf /", "rm -rf /", true},
		{"rm -rf /*", "rm -rf /*", true},
		{"rm -rf / with trailing space", "rm -rf / ", true},
		{"rm -rf multiple slashes", "rm -rf ///", true},
		{"rm -rf four slashes", "rm -rf ////", true},
		{"rm -rf slash dot", "rm -rf /. ", true},
		{"rm -rf slash dot dot", "rm -rf /.. ", true},
		{"rm -rf slash dot slash", "rm -rf /./", true},
		{"rm -rf slash dot slash star", "rm -rf /./*", true},
		{"rm -rf double-quoted root", "rm -rf \"/\"", true},
		{"rm -rf single-quoted root", "rm -rf '/'", true},
		{"rm -rf double-quoted root star", "rm -rf \"/*\"", true},
		{"rm -rf single-quoted root star", "rm -rf '/*'", true},
		{"rm split flags -r -f /", "rm -r -f /", true},
		{"rm split flags -f -r /", "rm -f -r /", true},
		{"rm recursive only -r /", "rm -r /", true},
		{"rm recursive only -R /", "rm -R /", true},
		{"rm inverted flags -fr /", "rm -fr /", true},
		{"rm with --no-preserve-root before", "rm --no-preserve-root -rf /", true},
		{"rm with --no-preserve-root between", "rm -rf --no-preserve-root /", true},
		{"rm with --no-preserve-root after", "rm -rf / --no-preserve-root", true},
		{"rm end-of-options flag: rm -rf -- /", "rm -rf -- /", true},
		{"rm end-of-options flag: rm -rf -- /*", "rm -rf -- /*", true},
		{"rm long options --recursive --force", "rm --recursive --force /", true},
		{"rm target before flags: rm / -rf", "rm / -rf", true},
		{"rm target before flags: rm /* -rf", "rm /* -rf", true},
		{"rm target before flags: rm / --recursive --force", "rm / --recursive --force", true},
		{"sudo rm -rf /", "sudo rm -rf /", true},
		{"sudo rm -rf /*", "sudo rm -rf /*", true},
		{"sudo -u root rm -rf /", "sudo -u root rm -rf /", true},
		{"env rm -rf /", "env rm -rf /", true},
		{"nohup rm -rf /", "nohup rm -rf /", true},
		{"exec rm -rf /", "exec rm -rf /", true},
		{"backslash alias bypass: \\rm -rf /", "\\rm -rf /", true},
		{"quoted binary: \"rm\" -rf /", "\"rm\" -rf /", true},
		{"quoted binary: 'rm' -rf /", "'rm' -rf /", true},
		{"absolute path binary: /bin/rm -rf /", "/bin/rm -rf /", true},
		{"absolute path binary: /usr/bin/rm -rf /", "/usr/bin/rm -rf /", true},
		{"quoted flags: rm \"-rf\" /", "rm \"-rf\" /", true},
		{"quoted flags: rm '-rf' /", "rm '-rf' /", true},
		{"escaped root slash: rm -rf \\/", "rm -rf \\/", true},
		{"quoted escaped root slash: rm -rf \"\\/\"", "rm -rf \"\\/\"", true},
		{"single quoted escaped root slash: rm -rf '\\/'", "rm -rf '\\/'", true},
		{"immediate redirection: rm -rf />/dev/null", "rm -rf />/dev/null", true},
		{"immediate redirection: rm -rf /*>/dev/null", "rm -rf /*>/dev/null", true},
		{"stderr redirection: rm -rf / 2>&1", "rm -rf / 2>&1", true},
		{"spaced stdout redirection: rm -rf / >/dev/null", "rm -rf / >/dev/null", true},
		{"immediate inline comment: rm -rf /#comment", "rm -rf /#comment", true},
		{"chained command: cd /tmp && rm -rf /", "cd /tmp && rm -rf /", true},
		{"chained command: test -d / && rm -rf /*", "test -d / && rm -rf /*", true},
		{"semicolon chained: rm -rf / ; echo done", "rm -rf / ; echo done", true},
		{"or chained: rm -rf / || echo done", "rm -rf / || echo done", true},
		{"piped: rm -rf / | cat", "rm -rf / | cat", true},
		{"tabs separated: rm\\t-rf\\t/", "rm\t-rf\t/", true},
		{"multiple spaces: rm   -rf    /", "rm   -rf    /", true},
		{"subshell bash -c: bash -c 'rm -rf /'", "bash -c 'rm -rf /'", true},
		{"subshell sh -c: sh -c \"rm -rf /\"", "sh -c \"rm -rf /\"", true},
		{"eval: eval \"rm -rf /\"", "eval \"rm -rf /\"", true},

		// ─────────────────────────────────────────────────────────────────────────────
		// LINUX & UNIX SYSTEM DIRECTORIES
		// ─────────────────────────────────────────────────────────────────────────────
		{"rm -rf /etc", "rm -rf /etc", true},
		{"rm -rf /etc/", "rm -rf /etc/", true},
		{"rm -rf /etc/*", "rm -rf /etc/*", true},
		{"rm -rf /boot", "rm -rf /boot", true},
		{"rm -rf /boot/*", "rm -rf /boot/*", true},
		{"rm -rf /bin", "rm -rf /bin", true},
		{"rm -rf /bin/*", "rm -rf /bin/*", true},
		{"rm -rf /sbin", "rm -rf /sbin", true},
		{"rm -rf /sbin/*", "rm -rf /sbin/*", true},
		{"rm -rf /usr", "rm -rf /usr", true},
		{"rm -rf /usr/*", "rm -rf /usr/*", true},
		{"rm -rf /var", "rm -rf /var", true},
		{"rm -rf /var/", "rm -rf /var/", true},
		{"rm -rf /var/*", "rm -rf /var/*", true},
		{"rm -rf /dev", "rm -rf /dev", true},
		{"rm -rf /sys", "rm -rf /sys", true},
		{"rm -rf /proc", "rm -rf /proc", true},
		{"rm -rf /root", "rm -rf /root", true},
		{"rm -rf /lib", "rm -rf /lib", true},
		{"rm -rf /lib64", "rm -rf /lib64", true},
		{"rm target before flags: rm /etc -rf", "rm /etc -rf", true},
		{"rm target before flags: rm /var/* -rf", "rm /var/* -rf", true},

		// ─────────────────────────────────────────────────────────────────────────────
		// LINUX & UNIX SYSTEM COMMANDS
		// ─────────────────────────────────────────────────────────────────────────────
		{"find root delete", "find / -delete", true},
		{"find root name delete", "find / -name '*.log' -delete", true},
		{"mkfs bare", "mkfs /dev/sdb", true},
		{"mkfs.ext4", "mkfs.ext4 /dev/sdb1", true},
		{"dd zero-wipe sda", "dd if=/dev/zero of=/dev/sda", true},
		{"dd urandom-wipe nvme", "dd if=/dev/urandom of=/dev/nvme0n1", true},
		{"shred block device", "shred /dev/sda", true},
		{"wipefs block device", "wipefs -a /dev/sda", true},
		{"raw device overwrite redirection", "> /dev/sda", true},
		{"cat redirect to raw disk", "cat test > /dev/sdb", true},
		{"fork bomb standard", ":(){ :|:& };:", true},
		{"fork bomb spaced", ": () { : | : & } ; :", true},
		{"delete root user", "userdel -r root", true},
		{"rm authorized_keys", "rm ~/.ssh/authorized_keys", true},
		{"truncate authorized_keys", "> /root/.ssh/authorized_keys", true},
		{"chmod -R 777 root", "chmod -R 777 /", true},
		{"chmod 777 root system path", "chmod 777 /var", true},

		// ─────────────────────────────────────────────────────────────────────────────
		// WINDOWS (POWERSHELL & CMD)
		// ─────────────────────────────────────────────────────────────────────────────
		{"Remove-Item -Recurse -Force C:\\", "Remove-Item -Recurse -Force C:\\", true},
		{"Remove-Item -Recurse -Force C:\\*", "Remove-Item -Recurse -Force C:\\*", true},
		{"Remove-Item -Recurse -Force 'C:\\'", "Remove-Item -Recurse -Force 'C:\\'", true},
		{"Remove-Item -Recurse -Force \"C:\\\"", "Remove-Item -Recurse -Force \"C:\\\"", true},
		{"Remove-Item C:\\ -Recurse -Force", "Remove-Item C:\\ -Recurse -Force", true},
		{"Remove-Item C:\\* -Recurse -Force", "Remove-Item C:\\* -Recurse -Force", true},
		{"Remove-Item -Force -Recurse D:\\", "Remove-Item -Force -Recurse D:\\", true},
		{"Remove-Item -Recurse C:\\Windows", "Remove-Item -Recurse C:\\Windows", true},
		{"Remove-Item -Recurse C:\\Windows\\System32", "Remove-Item -Recurse C:\\Windows\\System32", true},
		{"rd /s /q C:\\", "rd /s /q C:\\", true},
		{"rd C:\\ /s /q", "rd C:\\ /s /q", true},
		{"rmdir /s /q C:\\", "rmdir /s /q C:\\", true},
		{"del /s /q C:\\", "del /s /q C:\\", true},
		{"del C:\\* /s", "del C:\\* /s", true},
		{"format C: /y", "format C: /y", true},
		{"format D: /q", "format D: /q", true},
		{"Format-Volume -DriveLetter C", "Format-Volume -DriveLetter C", true},
		{"vssadmin delete shadows", "vssadmin delete shadows /all /quiet", true},
		{"Set-MpPreference -DisableRealtimeMonitoring", "Set-MpPreference -DisableRealtimeMonitoring $true", true},
		{"Clear-EventLog", "Clear-EventLog -LogName System", true},
		{"Set-NetFirewallProfile -Enabled False", "Set-NetFirewallProfile -Enabled False", true},
		{"Stop-Service MpsSvc", "Stop-Service MpsSvc", true},

		// ─────────────────────────────────────────────────────────────────────────────
		// MACOS (DARWIN)
		// ─────────────────────────────────────────────────────────────────────────────
		{"diskutil eraseDisk", "diskutil eraseDisk APFS Untitled /dev/disk2", true},
		{"diskutil apfs deleteVolume", "diskutil apfs deleteVolume /dev/disk1s1", true},
		{"diskutil apfs deleteContainer", "diskutil apfs deleteContainer /dev/disk1", true},
		{"diskutil secureErase", "diskutil secureErase 2 /dev/disk2", true},
		{"csrutil disable", "csrutil disable", true},
		{"csrutil clear", "csrutil clear", true},
		{"pfctl -d", "pfctl -d", true},
		{"pfctl -F all", "pfctl -F all", true},

		// ─────────────────────────────────────────────────────────────────────────────
		// SAFE COMMANDS ACROSS ALL PLATFORMS (MUST NEVER BE BLOCKED)
		// ─────────────────────────────────────────────────────────────────────────────
		{"safe: rm /tmp subpath", "rm -rf /tmp/foo", false},
		{"safe: rm /tmp wildcard subpath", "rm -rf /tmp/myapp/*", false},
		{"safe: rm /var/www site cache", "rm -rf /var/www/site/cache", false},
		{"safe: rm /var/log app logs", "rm -rf /var/log/nginx/*.log", false},
		{"safe: rm node_modules", "rm -rf ./node_modules", false},
		{"safe: rm build directory", "rm -rf build/", false},
		{"safe: rm single file", "rm myfile.txt", false},
		{"safe: rm single file force", "rm -f output.log", false},
		{"safe: rm relative directory", "rm -r ./temp", false},
		{"safe: rm user home directory subpath", "rm -rf /home/user/project", false},
		{"safe: rm opt subpath", "rm -rf /opt/mysoftware", false},
		{"safe: find in /tmp delete", "find /tmp -name '*.log' -delete", false},
		{"safe: npm build", "npm run build", false},
		{"safe: git clean", "git clean -fd", false},
		{"safe: chmod 755 webroot", "chmod 755 /var/www/html", false},
		{"safe: userdel non-root", "userdel testuser", false},
		{"safe: normal redirection to tmp", "echo 'hello' > /tmp/output.txt", false},
		{"safe: Windows Remove-Item temp subpath", "Remove-Item -Recurse C:\\temp\\build", false},
		{"safe: Windows Remove-Item relative dist", "Remove-Item -Recurse -Force .\\dist", false},
		{"safe: Windows del temp files", "del /q C:\\temp\\*.tmp", false},
		{"safe: Windows rd temp cache", "rd /s /q C:\\temp\\cache", false},
		{"safe: macOS diskutil list", "diskutil list", false},
		{"safe: macOS diskutil info", "diskutil info /dev/disk1", false},
		{"safe: macOS csrutil status", "csrutil status", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			blocked, pattern := isCommandDestructive(tc.command)
			if blocked != tc.expectBlock {
				t.Errorf("Command %q: got blocked=%v (pattern: %q), want blocked=%v", tc.command, blocked, pattern, tc.expectBlock)
			}
		})
	}
}
