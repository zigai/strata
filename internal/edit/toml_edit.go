package edit

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/zigai/strata/codec"
)

// UpdateTOML returns data with value written at the dotted key.
//
// An existing assignment for the key is rewritten in place and only the value is
// substituted. The document's comments, blank lines, and surrounding formatting
// survive that substitution. The continuation lines of a multiline string that
// held the key are removed.
//
// A key with no assignment is appended: in the root region, inside the matching
// table when the file declares one, or under a new table header at the end of
// the document.
//
// A key and a table header are matched case-insensitively, and a header written
// as [table."sub.key"] names the same table as [table.sub.key].
func UpdateTOML(data []byte, dottedKey string, value any) ([]byte, error) {
	formattedVal, err := formatTOMLValue(value)
	if err != nil {
		return nil, fmt.Errorf("format toml value: %w", err)
	}

	return UpdateFormatted(data, dottedKey, value, formattedVal)
}

// UpdateFormatted updates dottedKey in TOML data using an already formatted literal.
func UpdateFormatted(data []byte, dottedKey string, value any, formattedVal string) ([]byte, error) {
	crlf := bytes.Contains(data, []byte("\r\n"))
	rawLines := strings.Split(string(data), "\n")

	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	targetParts := splitDottedKey(dottedKey)
	if len(targetParts) == 0 {
		return nil, fmt.Errorf("invalid empty key path")
	}
	for _, p := range targetParts {
		if p == "" {
			return nil, fmt.Errorf("invalid empty path segment in %q", dottedKey)
		}
	}

	var currentTable []string
	var inMultilineString bool
	var multilineQuote string

	type candAssignment struct {
		lineIdx      int
		eqIdx        int
		currentTable []string
		fullKey      []string
	}
	var candidates []candAssignment

	for i := range len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if inMultilineString {
			if strings.Contains(line, multilineQuote) {
				inMultilineString = false
				multilineQuote = ""
			}
			continue
		}

		clean := stripInlineComment(line)
		if opensMulti, quote := checkOpeningMultilineString(clean); opensMulti {
			inMultilineString = true
			multilineQuote = quote
		}

		if tbl, ok := parseTableHeader(trimmed); ok {
			currentTable = tbl
			continue
		}

		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		eqIdx := findAssignmentEquals(line)
		if eqIdx == -1 {
			continue
		}

		rawKey := strings.TrimSpace(line[:eqIdx])
		keyParts := splitDottedKey(rawKey)
		lineFullKey := make([]string, 0, len(currentTable)+len(keyParts))
		lineFullKey = append(lineFullKey, currentTable...)
		lineFullKey = append(lineFullKey, keyParts...)

		candidates = append(candidates, candAssignment{
			lineIdx:      i,
			eqIdx:        eqIdx,
			currentTable: currentTable,
			fullKey:      lineFullKey,
		})
	}

	keyUpdated := false

	// 1. Look for exact case-sensitive match
	matchIdx := -1
	for idx, cand := range candidates {
		if partsEqual(cand.fullKey, targetParts) {
			matchIdx = idx
			break
		}
	}

	// 2. Look for case-insensitive match if no exact match
	if matchIdx == -1 {
		foldMatches := 0
		for idx, cand := range candidates {
			if partsEqualFold(cand.fullKey, targetParts) {
				foldMatches++
				matchIdx = idx
			}
		}
		if foldMatches > 1 {
			return nil, fmt.Errorf("ambiguous key %q matches multiple keys in TOML document", dottedKey)
		}
	}

	if matchIdx != -1 {
		cand := candidates[matchIdx]
		lines = updateMatchingLine(lines, cand.lineIdx, cand.eqIdx, formattedVal)
		keyUpdated = true
	} else {
		// 3. Check for inline table navigation
		for _, cand := range candidates {
			if len(cand.fullKey) < len(targetParts) && partsEqualFold(cand.fullKey, targetParts[:len(cand.fullKey)]) {
				line := lines[cand.lineIdx]
				afterEq := strings.TrimSpace(line[cand.eqIdx+1:])
				valWithoutComment := stripInlineComment(afterEq)
				if strings.HasPrefix(valWithoutComment, "{") && strings.HasSuffix(valWithoutComment, "}") {
					remainingParts := targetParts[len(cand.fullKey):]
					updatedInline, err := updateInlineTable(valWithoutComment, remainingParts, value)
					if err != nil {
						return nil, err
					}
					lines[cand.lineIdx] = replaceLineValue(line, cand.eqIdx, updatedInline)
					keyUpdated = true
					break
				}
				return nil, fmt.Errorf("%w: key %q is not a table", ErrNonObjectNavigation, strings.Join(cand.fullKey, "."))
			}
		}
	}

	var result []byte
	if keyUpdated {
		result = joinLines(lines, crlf)
	} else {
		appended := appendTOMLKey(lines, targetParts, formattedVal)
		if crlf {
			result = bytes.ReplaceAll(appended, []byte("\n"), []byte("\r\n"))
		} else {
			result = appended
		}
	}

	var dummy any
	if err := toml.Unmarshal(result, &dummy); err != nil {
		return nil, fmt.Errorf("%w: %w", codec.ErrMalformed, err)
	}
	return result, nil
}

func joinLines(lines []string, crlf bool) []byte {
	sep := "\n"
	if crlf {
		sep = "\r\n"
	}

	return []byte(strings.Join(lines, sep))
}

func parseTableHeader(trimmed string) ([]string, bool) {
	clean := stripInlineComment(trimmed)
	if strings.Contains(clean, ",") {
		return nil, false
	}

	if strings.HasPrefix(clean, "[[") && strings.HasSuffix(clean, "]]") {
		inner := strings.TrimSpace(clean[2 : len(clean)-2])
		if inner == "" || strings.Contains(inner, ",") {
			return nil, false
		}

		return canonicalHeaderParts(inner), true
	}

	if strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]") {
		inner := strings.TrimSpace(clean[1 : len(clean)-1])
		if inner == "" || strings.Contains(inner, ",") {
			return nil, false
		}

		return canonicalHeaderParts(inner), true
	}

	return nil, false
}

// canonicalHeaderParts returns the header segments a caller's dotted key is
// compared against.
//
// The joined header is split a second time deliberately. A header written as
// [table."sub.key"] and a header written as [table.sub.key] name the same table,
// and a caller does not have to know how the file quoted a segment.
//
// NB: the collapsed form also means a quoted segment that contains a dot cannot
// be addressed as one segment. That ambiguity is longstanding and is preserved
// deliberately.
func canonicalHeaderParts(raw string) []string {
	parts := splitDottedKey(strings.TrimSpace(raw))

	return splitDottedKey(strings.Join(parts, "."))
}

func splitDottedKey(s string) []string {
	var (
		parts []string
		curr  []rune
	)

	inQuote := false

	var quoteChar rune

	for _, r := range s {
		if inQuote {
			if r == quoteChar {
				inQuote = false
			} else {
				curr = append(curr, r)
			}

			continue
		}

		if r == '"' || r == '\'' {
			inQuote = true
			quoteChar = r

			continue
		}

		if r == '.' {
			parts = append(parts, strings.TrimSpace(string(curr)))
			curr = nil

			continue
		}

		curr = append(curr, r)
	}

	if len(curr) > 0 {
		parts = append(parts, strings.TrimSpace(string(curr)))
	}

	return parts
}

func updateLineAssignment(
	lines []string,
	lineIdx, eqIdx int,
	currentTable, targetParts []string,
	formattedVal string,
	value any,
) ([]string, bool, error) {
	line := lines[lineIdx]
	rawKey := strings.TrimSpace(line[:eqIdx])
	keyParts := splitDottedKey(rawKey)
	lineFullKey := make([]string, 0, len(currentTable)+len(keyParts))
	lineFullKey = append(lineFullKey, currentTable...)
	lineFullKey = append(lineFullKey, keyParts...)

	if partsEqualFold(lineFullKey, targetParts) {
		lines = updateMatchingLine(lines, lineIdx, eqIdx, formattedVal)
		return lines, true, nil
	}

	if len(lineFullKey) < len(targetParts) && partsEqualFold(lineFullKey, targetParts[:len(lineFullKey)]) {
		afterEq := strings.TrimSpace(line[eqIdx+1:])
		valWithoutComment := stripInlineComment(afterEq)

		if strings.HasPrefix(valWithoutComment, "{") && strings.HasSuffix(valWithoutComment, "}") {
			remainingParts := targetParts[len(lineFullKey):]

			updatedInline, err := updateInlineTable(valWithoutComment, remainingParts, value)
			if err != nil {
				return nil, false, err
			}

			lines[lineIdx] = replaceLineValue(line, eqIdx, updatedInline)

			return lines, true, nil
		}

		return nil, false, fmt.Errorf("%w: key %q is not a table", ErrNonObjectNavigation, strings.Join(lineFullKey, "."))
	}

	return lines, false, nil
}

func partsEqualFold(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}

	return true
}

func stripInlineComment(s string) string {
	cIdx := findInlineComment(s)
	if cIdx != -1 {
		return strings.TrimSpace(s[:cIdx])
	}

	return s
}

func partsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countTripleQuotes(s, quote string) int {
	count := 0
	idx := 0
	for {
		p := strings.Index(s[idx:], quote)
		if p == -1 {
			break
		}
		count++
		idx += p + len(quote)
	}
	return count
}

func checkOpeningMultilineString(s string) (bool, string) {
	if strings.Contains(s, `"""`) {
		if countTripleQuotes(s, `"""`)%2 != 0 {
			return true, `"""`
		}
	}
	if strings.Contains(s, `'''`) {
		if countTripleQuotes(s, `'''`)%2 != 0 {
			return true, `'''`
		}
	}
	return false, ""
}

func isOpeningMultilineString(val string) bool {
	clean := stripInlineComment(val)
	if strings.HasPrefix(clean, `"""`) {
		return countTripleQuotes(clean, `"""`)%2 != 0
	}
	if strings.HasPrefix(clean, `'''`) {
		return countTripleQuotes(clean, `'''`)%2 != 0
	}
	return false
}

func isOpeningMultilineArray(val string) bool {
	clean := stripInlineComment(val)
	return bracketDelta(clean) > 0
}

func bracketDelta(s string) int {
	delta := 0
	inQuote := false
	var quoteChar rune
	escaped := false
	for i := range len(s) {
		r := rune(s[i])
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuote && quoteChar == '"' {
			escaped = true
			continue
		}
		if inQuote {
			if r == quoteChar {
				inQuote = false
			}
			continue
		}
		if r == '"' || r == '\'' {
			inQuote = true
			quoteChar = r
			continue
		}
		if r == '#' {
			break
		}
		if r == '[' {
			delta++
		} else if r == ']' {
			delta--
		}
	}
	return delta
}

func removeMultilineArrayTail(lines []string, startIdx int, cleanVal string) []string {
	depth := bracketDelta(cleanVal)
	endIdx := startIdx
	for endIdx < len(lines) {
		line := lines[endIdx]
		depth += bracketDelta(line)
		endIdx++
		if depth <= 0 {
			break
		}
	}
	return append(lines[:startIdx], lines[endIdx:]...)
}

func updateMatchingLine(lines []string, i, eqIdx int, formattedVal string) []string {
	line := lines[i]
	newLine := replaceLineValue(line, eqIdx, formattedVal)

	afterEq := strings.TrimSpace(line[eqIdx+1:])
	if isOpeningMultilineString(afterEq) {
		lines = removeMultilineTail(lines, i+1)
	} else if isOpeningMultilineArray(afterEq) {
		lines = removeMultilineArrayTail(lines, i+1, afterEq)
	}

	lines[i] = newLine

	return lines
}

func removeMultilineTail(lines []string, startIdx int) []string {
	endIdx := startIdx
	for endIdx < len(lines) {
		line := lines[endIdx]
		if strings.Contains(line, `"""`) || strings.Contains(line, `'''`) {
			endIdx++
			break
		}
		endIdx++
	}
	return append(lines[:startIdx], lines[endIdx:]...)
}

func formatTOMLValue(value any) (string, error) {
	if m, ok := value.(map[string]any); ok {
		return formatInlineTable(m)
	}

	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Map || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		return formatCompositeTOML(rv)
	}

	dummy := map[string]any{"_k": value}

	encoded, err := toml.Marshal(dummy)
	if err != nil {
		return "", fmt.Errorf("marshal toml value: %w", err)
	}

	str := strings.TrimSpace(string(encoded))

	prefix := "_k = "
	if _, after, ok := strings.Cut(str, prefix); ok {
		return strings.TrimSpace(after), nil
	}

	return str, nil
}

func formatCompositeTOML(rv reflect.Value) (string, error) {
	if rv.Kind() == reflect.Map {
		m := make(map[string]any, rv.Len())

		iter := rv.MapRange()
		for iter.Next() {
			m[fmt.Sprint(iter.Key().Interface())] = iter.Value().Interface()
		}

		return formatInlineTable(m)
	}

	elems := make([]string, rv.Len())
	for i := range rv.Len() {
		elemStr, err := formatTOMLValue(rv.Index(i).Interface())
		if err != nil {
			return "", err
		}

		elems[i] = elemStr
	}

	return "[ " + strings.Join(elems, ", ") + " ]", nil
}

func findAssignmentEquals(line string) int {
	inQuote := false

	var quoteChar rune

	escaped := false

	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}

		if inQuote {
			if quoteChar == '"' && r == '\\' {
				escaped = true
				continue
			}

			if r == quoteChar {
				inQuote = false
			}

			continue
		}

		if r == '"' || r == '\'' {
			inQuote = true
			quoteChar = r

			continue
		}

		if r == '#' {
			return -1
		}

		if r == '=' {
			return i
		}
	}

	return -1
}

func replaceLineValue(line string, eqIdx int, newVal string) string {
	afterEq := line[eqIdx+1:]
	beforeVal := line[:eqIdx+1]

	spaceLen := 0

	for _, r := range afterEq {
		if r == ' ' || r == '\t' {
			spaceLen++
		} else {
			break
		}
	}

	spaces := afterEq[:spaceLen]
	valAndComment := afterEq[spaceLen:]

	commentIdx := findInlineComment(valAndComment)

	commentSuffix := ""
	if commentIdx != -1 {
		commentSuffix = valAndComment[commentIdx:]
	}

	if spaces == "" {
		spaces = " "
	}

	if commentSuffix != "" {
		return beforeVal + spaces + newVal + " " + strings.TrimLeft(commentSuffix, " \t")
	}

	return beforeVal + spaces + newVal
}

func findInlineComment(s string) int {
	inQuote := false

	var quoteChar rune

	escaped := false

	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}

		if inQuote {
			if quoteChar == '"' && r == '\\' {
				escaped = true
				continue
			}

			if r == quoteChar {
				inQuote = false
			}

			continue
		}

		if r == '"' || r == '\'' {
			inQuote = true
			quoteChar = r

			continue
		}

		if r == '#' {
			return i
		}
	}

	return -1
}

func appendTOMLKey(lines []string, targetParts []string, formattedVal string) []byte {
	if len(targetParts) == 1 {
		return appendRootKey(lines, targetParts[0], formattedVal)
	}

	tableParts := targetParts[:len(targetParts)-1]
	leaf := targetParts[len(targetParts)-1]

	return appendTableKey(lines, tableParts, leaf, formattedVal)
}

func appendRootKey(lines []string, key, formattedVal string) []byte {
	insertIdx := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if _, ok := parseTableHeader(trimmed); ok {
			insertIdx = i
			break
		}
	}

	newLine := fmt.Sprintf("%s = %s", formatTOMLKey(key), formattedVal)
	lines = slices.Insert(lines, insertIdx, newLine)

	return []byte(strings.Join(lines, "\n"))
}

func appendTableKey(lines []string, tableParts []string, leaf, formattedVal string) []byte {
	tableIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if tbl, ok := parseTableHeader(trimmed); ok {
			if partsEqualFold(tbl, tableParts) {
				tableIdx = i
				break
			}
		}
	}

	if tableIdx != -1 {
		insertIdx := len(lines)
		for i := tableIdx + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if _, ok := parseTableHeader(trimmed); ok {
				insertIdx = i
				break
			}
		}

		newLine := fmt.Sprintf("%s = %s", formatTOMLKey(leaf), formattedVal)
		lines = slices.Insert(lines, insertIdx, newLine)

		return []byte(strings.Join(lines, "\n"))
	}

	var buf bytes.Buffer
	buf.WriteString(strings.Join(lines, "\n"))

	if len(lines) > 0 && lines[len(lines)-1] != "" {
		buf.WriteString("\n")
	}

	formattedHeader := formatTableHeader(tableParts)
	fmt.Fprintf(&buf, "\n[%s]\n%s = %s\n", formattedHeader, formatTOMLKey(leaf), formattedVal)

	return buf.Bytes()
}

func formatTableHeader(parts []string) string {
	formatted := make([]string, 0, len(parts))

	for _, p := range parts {
		formatted = append(formatted, formatTOMLKey(p))
	}

	return strings.Join(formatted, ".")
}

func formatTOMLKey(key string) string {
	if isBareTOMLKey(key) {
		return key
	}

	return fmt.Sprintf("%q", key)
}

func isBareTOMLKey(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}

		return false
	}

	return true
}

func updateInlineTable(inlineText string, parts []string, value any) (string, error) {
	var m map[string]any

	dummyDoc := "dummy = " + inlineText
	if err := toml.Unmarshal([]byte(dummyDoc), &m); err != nil {
		return "", fmt.Errorf("parse inline table: %w", err)
	}

	tableVal, ok := m["dummy"]
	if !ok {
		return "", ErrNonObjectNavigation
	}

	tableMap, ok := tableVal.(map[string]any)
	if !ok {
		return "", ErrNonObjectNavigation
	}

	if err := setMapNested(tableMap, parts, value); err != nil {
		return "", err
	}

	return formatInlineTable(tableMap)
}

func setMapNested(m map[string]any, parts []string, value any) error {
	if len(parts) == 0 {
		return nil
	}

	key := parts[0]
	if len(parts) == 1 {
		m[key] = value
		return nil
	}

	child, exists := m[key]
	if !exists {
		newChild := make(map[string]any)
		m[key] = newChild

		return setMapNested(newChild, parts[1:], value)
	}

	childMap, ok := child.(map[string]any)
	if !ok {
		return ErrNonObjectNavigation
	}

	return setMapNested(childMap, parts[1:], value)
}

func formatInlineTable(m map[string]any) (string, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		valStr, err := formatTOMLValue(m[k])
		if err != nil {
			return "", err
		}

		pairs = append(pairs, fmt.Sprintf("%s = %s", formatTOMLKey(k), valStr))
	}

	return "{ " + strings.Join(pairs, ", ") + " }", nil
}
