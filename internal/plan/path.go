package plan

import (
	"fmt"
	"strings"
)

func kebabCase(s string) string {
	return strings.ReplaceAll(toSnakeCaseBase(s), "_", "-")
}

func toSnakeCaseBase(s string) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s) + snakePad)

	runes := []rune(s)
	for i := range runes {
		writeSnakeRune(&b, runes, i)
	}

	return b.String()
}

func writeSnakeRune(b *strings.Builder, runes []rune, i int) {
	r := runes[i]
	if r < 'A' || r > 'Z' {
		b.WriteRune(r)

		return
	}

	if needsSnakeSeparator(runes, i) {
		b.WriteRune('_')
	}

	b.WriteRune(r + ('a' - 'A'))
}

func needsSnakeSeparator(runes []rune, i int) bool {
	if i == 0 {
		return false
	}

	previous := runes[i-1]
	if previous >= 'a' && previous <= 'z' {
		return true
	}

	if i+1 >= len(runes) {
		return false
	}

	return previous >= 'A' && previous <= 'Z' && runes[i+1] >= 'a' && runes[i+1] <= 'z'
}

func joinFlagName(prefix, name string) string {
	switch {
	case prefix == "":
		return name
	case name == "":
		return prefix
	default:
		return prefix + "-" + name
	}
}

func joinConfigPath(prefix, key string) string {
	switch {
	case prefix == "":
		return key
	case key == "":
		return prefix
	default:
		return prefix + "." + key
	}
}

func joinDisplay(prefix, name string) string {
	if prefix == "" {
		return name
	}

	return prefix + "." + name
}

func appendIndex(indexPath []int, i int) []int {
	out := make([]int, len(indexPath)+1)
	copy(out, indexPath)
	out[len(indexPath)] = i

	return out
}

func formatIndexPath(indexPath []int) string {
	parts := make([]string, 0, len(indexPath))
	for _, index := range indexPath {
		parts = append(parts, fmt.Sprintf("[%d]", index))
	}

	return strings.Join(parts, "")
}
