package status

import "strings"

const (
	lineLength = 77
)

func wrapLine(line string) []string {
	if len(line) <= lineLength {
		return []string{line}
	}
	tokens := strings.Split(line, " ")
	lines := []string{}
	currentLine := ""
	for _, token := range tokens {
		appendedLength := len(token)
		if currentLine != "" {
			appendedLength++
		}
		newLength := appendedLength + len(currentLine)
		if newLength > lineLength {
			lines = append(lines, currentLine)
			currentLine = ""
		}
		if currentLine == "" {
			currentLine = token
			continue
		}
		currentLine = currentLine + " " + token
	}
	return append(lines, currentLine)
}

func writeComment(name, comment string) string {
	comment = strings.TrimSpace(comment)
	lines := strings.Split(comment, "\n")
	wrappedLines := []string{}
	for i, line := range lines {
		if i == 0 && name != "" {
			line = name + " - " + line
		}
		if i != 0 {
			wrappedLines = append(wrappedLines, "")
		}
		wrappedLines = append(wrappedLines, wrapLine(line)...)
	}
	for i, line := range wrappedLines {
		wrappedLines[i] = "// " + line
	}
	return strings.Join(wrappedLines, "\n")
}
