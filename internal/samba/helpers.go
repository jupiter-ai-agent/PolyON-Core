package samba

import "strings"

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func afterColon(s string) string {
	if idx := strings.Index(s, ":"); idx >= 0 {
		return strings.TrimSpace(s[idx+1:])
	}
	return ""
}
