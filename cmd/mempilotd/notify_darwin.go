//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

// notifyUser surfaces an advisory to the user through Notification Center.
//
// It returns whether the call succeeded, and the reason when it did not, so
// the caller can record the delivery outcome. An "exit 0" is not proof the
// user saw anything: macOS can accept the AppleScript and still suppress the
// banner (notifications disabled for the calling app, Focus mode, Do Not
// Disturb). What the return value honestly means is "the notification was
// handed to the system", not "it was displayed".
func notifyUser(title, body string) (bool, string) {
	if title == "" {
		title = "Memory Pilot"
	}
	title = escapeAppleScript(title)
	body = escapeAppleScript(body)
	script := "display notification \"" + body + "\" with title \"" + title + "\""
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(string(out)) + ": " + err.Error()
	}
	return true, ""
}

// escapeAppleScript makes a string safe inside an AppleScript double-quoted
// literal. Backslash first, then the quote — the other order double-escapes
// the backslashes it just inserted.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
