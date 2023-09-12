package utils

import "strings"

func ExtractLast20LinesOfLog(err error) []string {
	errorString := err.Error()
	logs := strings.Split(errorString, "\n")
	if len(logs) < 20 {
		return logs
	}
	return logs[len(logs)-20:]
}
