package edit

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/strata/codec"
)

const (
	tripleQuoteLen = 3
	// maxClosingContentQuotes is how many quote characters a multi-line string
	// may end with directly before its closing delimiter.
	maxClosingContentQuotes = 2
)

var (
	ErrEmptyKey = errors.New("invalid empty key path")

	ErrEmptyKeySegment = errors.New("invalid empty path segment")
)

type tomlAssignment struct {
	lineIdx      int
	eqIdx        int
	endIdx       int
	currentTable []string
	fullKey      []string
}

type tomlLine struct {
	header []string
	// eqIdx is the index of an assignment's "=", or -1.
	eqIdx int
	// end is the last line of an assignment's value, which is this line unless
	// the value is a multi-line string or array.
	end int
}

type tomlLexer struct {
	quote string
	depth int
}

func UpdateTOML(data []byte, dottedKey string, value any) ([]byte, error) {
	formattedVal, err := formatTOMLValue(value)
	if err != nil {
		return nil, fmt.Errorf("format toml value: %w", err)
	}

	return UpdateTOMLFormatted(data, dottedKey, value, formattedVal)
}

func UpdateTOMLFormatted(data []byte, dottedKey string, value any, formattedVal string) ([]byte, error) {
	crlf := bytes.Contains(data, []byte("\r\n"))
	rawLines := strings.Split(string(data), "\n")

	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	targetParts := splitDottedKey(dottedKey)
	if len(targetParts) == 0 {
		return nil, ErrEmptyKey
	}

	if slices.Contains(targetParts, "") {
		return nil, fmt.Errorf("%w in %q", ErrEmptyKeySegment, dottedKey)
	}

	scanned := scanTOMLLines(lines)
	candidates := collectAssignments(lines, scanned)

	matchIdx := findMatchingCandidate(candidates, targetParts)

	var (
		keyUpdated bool
		err        error
	)

	if matchIdx != -1 {
		cand := candidates[matchIdx]
		lines = updateMatchingLine(lines, cand, formattedVal)
		keyUpdated = true
	} else if trimmed, replaced := removeTOMLSubtree(lines, scanned, candidates, targetParts); replaced {
		lines = trimmed
		scanned = scanTOMLLines(lines)
		candidates = collectAssignments(lines, scanned)
	} else {
		lines, keyUpdated, err = handleInlineTableUpdate(lines, candidates, targetParts, value)
		if err != nil {
			return nil, err
		}
	}

	var result []byte
	if keyUpdated {
		result = joinLines(lines, crlf)
	} else {
		// Nothing was edited, so the scan still describes lines.
		appended := appendTOMLKey(lines, scanned, candidates, targetParts, formattedVal)
		if len(appended) > 0 && appended[len(appended)-1] != '\n' {
			appended = append(appended, '\n')
		}

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

// scanTOMLLines classifies every line of a document. A line inside a multi-line
// string or a multi-line array is part of a value, so it is never read as a
// table header or an assignment even when it looks like one.
func scanTOMLLines(lines []string) []tomlLine {
	scanned := make([]tomlLine, len(lines))

	var (
		value tomlLexer
		owner = -1
	)

	for i, line := range lines {
		scanned[i] = tomlLine{header: nil, eqIdx: -1, end: i}

		if value.open() {
			value.scan(line, 0)

			scanned[owner].end = i

			continue
		}

		trimmed := strings.TrimSpace(line)
		if tbl, ok := parseTableHeader(trimmed); ok {
			scanned[i].header = tbl
			continue
		}

		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		eqIdx := findAssignmentEquals(line)
		if eqIdx == -1 {
			continue
		}

		scanned[i].eqIdx = eqIdx
		owner = i

		value = tomlLexer{quote: "", depth: 0}
		value.scan(line[eqIdx+1:], 0)
	}

	return scanned
}

func (lx *tomlLexer) open() bool {
	return lx.quote != "" || lx.depth > 0
}

func (lx *tomlLexer) scan(line string, stop byte) int {
	for i := 0; i < len(line); {
		if lx.quote != "" {
			i = lx.skipString(line, i)
			continue
		}

		switch c := line[i]; c {
		case stop:
			return i
		case '#':
			lx.endLine()
			return -1
		case '"', '\'':
			lx.quote = line[i : i+1]
			if tripled := strings.Repeat(lx.quote, tripleQuoteLen); strings.HasPrefix(line[i:], tripled) {
				lx.quote = tripled
			}

			i += len(lx.quote)

			continue
		case '[', '{':
			lx.depth++
		case ']', '}':
			lx.depth--
		}

		i++
	}

	lx.endLine()

	return -1
}

func (lx *tomlLexer) endLine() {
	if len(lx.quote) == 1 {
		lx.quote = ""
	}
}

func (lx *tomlLexer) skipString(line string, i int) int {
	for i < len(line) {
		switch {
		case line[i] == '\\' && lx.quote[0] == '"':
			i += 2
		case strings.HasPrefix(line[i:], lx.quote):
			end := i + len(lx.quote)

			// A multi-line string may end with up to two quotes of its content
			// directly before the delimiter.
			if len(lx.quote) == tripleQuoteLen {
				for end < len(line) && end < i+tripleQuoteLen+maxClosingContentQuotes && line[end] == lx.quote[0] {
					end++
				}
			}

			lx.quote = ""

			return end
		default:
			i++
		}
	}

	return i
}

func collectAssignments(lines []string, scanned []tomlLine) []tomlAssignment {
	var (
		currentTable []string
		candidates   []tomlAssignment
	)

	for i, info := range scanned {
		if info.header != nil {
			currentTable = info.header
			continue
		}

		if info.eqIdx == -1 {
			continue
		}

		rawKey := strings.TrimSpace(lines[i][:info.eqIdx])
		keyParts := splitDottedKey(rawKey)
		lineFullKey := make([]string, 0, len(currentTable)+len(keyParts))
		lineFullKey = append(lineFullKey, currentTable...)
		lineFullKey = append(lineFullKey, keyParts...)

		candidates = append(candidates, tomlAssignment{
			lineIdx:      i,
			eqIdx:        info.eqIdx,
			endIdx:       info.end,
			currentTable: currentTable,
			fullKey:      lineFullKey,
		})
	}

	return candidates
}

func removeTOMLSubtree(lines []string, scanned []tomlLine, candidates []tomlAssignment, targetParts []string) ([]string, bool) {
	removed := make([]bool, len(lines))
	replaced := false

	for index, line := range scanned {
		if line.header != nil && len(line.header) >= len(targetParts) && partsEqual(line.header[:len(targetParts)], targetParts) {
			removed[index] = true
			replaced = true
		}
	}

	for _, candidate := range candidates {
		if len(candidate.fullKey) > len(targetParts) && partsEqual(candidate.fullKey[:len(targetParts)], targetParts) {
			for index := candidate.lineIdx; index <= candidate.endIdx; index++ {
				removed[index] = true
			}

			replaced = true
		}
	}

	if !replaced {
		return lines, false
	}

	kept := make([]string, 0, len(lines))
	for index, line := range lines {
		if !removed[index] {
			kept = append(kept, line)
		}
	}

	return kept, true
}

func findMatchingCandidate(candidates []tomlAssignment, targetParts []string) int {
	for idx, cand := range candidates {
		if partsEqual(cand.fullKey, targetParts) {
			return idx
		}
	}

	return -1
}

func handleInlineTableUpdate(lines []string, candidates []tomlAssignment, targetParts []string, value any) ([]string, bool, error) {
	for _, cand := range candidates {
		if len(cand.fullKey) >= len(targetParts) || !partsEqual(cand.fullKey, targetParts[:len(cand.fullKey)]) {
			continue
		}

		line := lines[cand.lineIdx]
		afterEq := strings.TrimSpace(line[cand.eqIdx+1:])
		valWithoutComment := stripInlineComment(afterEq)

		if strings.HasPrefix(valWithoutComment, "{") && strings.HasSuffix(valWithoutComment, "}") {
			remainingParts := targetParts[len(cand.fullKey):]

			updatedInline, err := updateInlineTable(valWithoutComment, remainingParts, value)
			if err != nil {
				return nil, false, err
			}

			lines[cand.lineIdx] = replaceLineValue(line, cand.eqIdx, updatedInline)

			return lines, true, nil
		}

		return nil, false, fmt.Errorf("%w: key %q is not a table", ErrPathNotMapping, strings.Join(cand.fullKey, "."))
	}

	return lines, false, nil
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

	var inner string

	switch {
	case strings.HasPrefix(clean, "[[") && strings.HasSuffix(clean, "]]"):
		inner = clean[2 : len(clean)-2]
	case strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]"):
		inner = clean[1 : len(clean)-1]
	default:
		return nil, false
	}

	if !isTOMLKeySyntax(inner) {
		return nil, false
	}

	return splitDottedKey(inner), true
}

// An array such as [1, 2] or a nested array line such as ["a"]] is not a key.
func isTOMLKeySyntax(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	for {
		rest, ok := consumeTOMLKeySegment(s)
		if !ok {
			return false
		}

		rest = strings.TrimSpace(rest)
		if rest == "" {
			return true
		}

		if rest[0] != '.' {
			return false
		}

		s = strings.TrimSpace(rest[1:])
	}
}

func consumeTOMLKeySegment(s string) (string, bool) {
	if s == "" {
		return "", false
	}

	if s[0] == '"' || s[0] == '\'' {
		key := tomlLexer{quote: s[:1], depth: 0}

		end := key.skipString(s, 1)
		if key.quote != "" {
			return "", false
		}

		return s[end:], true
	}

	n := 0
	for n < len(s) && isBareTOMLKeyByte(s[n]) {
		n++
	}

	return s[n:], n > 0
}

func splitDottedKey(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var parts []string

	start := 0
	for index := 0; index < len(raw); {
		if (raw[index] == '"' || raw[index] == '\'') && strings.TrimSpace(raw[start:index]) == "" {
			lexer := tomlLexer{quote: raw[index : index+1], depth: 0}
			index = lexer.skipString(raw, index+1)

			continue
		}

		if raw[index] == '.' {
			parts = append(parts, decodeTOMLKeySegment(raw[start:index]))
			start = index + 1
		}

		index++
	}

	return append(parts, decodeTOMLKeySegment(raw[start:]))
}

func decodeTOMLKeySegment(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || (raw[0] != '"' && raw[0] != '\'') {
		return raw
	}

	var decoded map[string]any
	if toml.Unmarshal([]byte(raw+" = 0"), &decoded) == nil {
		for key := range decoded {
			return key
		}
	}

	return raw
}

func findAssignmentEquals(line string) int {
	var key tomlLexer

	return key.scan(line, '=')
}

func findInlineComment(s string) int {
	var value tomlLexer

	return value.scan(s, '#')
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

func updateMatchingLine(lines []string, cand tomlAssignment, formattedVal string) []string {
	closingComment := ""

	if cand.endIdx > cand.lineIdx {
		var lexer tomlLexer
		lexer.scan(lines[cand.lineIdx][cand.eqIdx+1:], '#')

		for index := cand.lineIdx + 1; index <= cand.endIdx; index++ {
			comment := lexer.scan(lines[index], '#')
			if index == cand.endIdx && comment >= 0 {
				closingComment = lines[index][comment:]
			}
		}
	}

	lines[cand.lineIdx] = replaceLineValue(lines[cand.lineIdx], cand.eqIdx, formattedVal)
	if closingComment != "" {
		lines[cand.lineIdx] += " " + closingComment
	}

	return slices.Delete(lines, cand.lineIdx+1, cand.endIdx+1)
}

func formatTOMLValue(value any) (string, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return "", fmt.Errorf("%w: nil has no toml value", codec.ErrMalformed)
	}

	if reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		return formatCompositeTOML(reflected)
	}

	wrapper := reflect.MakeMap(reflect.MapOf(reflect.TypeFor[string](), reflected.Type()))
	wrapper.SetMapIndex(reflect.ValueOf("_k"), reflected)

	encoded, err := codec.NewTOML().Encode(wrapper.Interface())
	if err != nil {
		return "", fmt.Errorf("marshal toml value: %w", err)
	}

	var document map[string]any
	if err := toml.Unmarshal(encoded, &document); err != nil {
		return "", fmt.Errorf("parse encoded toml value: %w", err)
	}

	return formatDecodedTOMLValue(document["_k"])
}

func formatDecodedTOMLValue(value any) (string, error) {
	if mapping, ok := value.(map[string]any); ok {
		return formatInlineTable(mapping)
	}

	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && (reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
		return formatCompositeTOML(reflected)
	}

	encoded, err := toml.Marshal(map[string]any{"_k": value})
	if err != nil {
		return "", fmt.Errorf("marshal toml value: %w", err)
	}

	text := strings.TrimSpace(string(encoded))
	if _, after, ok := strings.Cut(text, "_k = "); ok {
		return strings.TrimSpace(after), nil
	}

	return "", fmt.Errorf("%w: cannot encode %T as a toml value", codec.ErrMalformed, value)
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

func appendTOMLKey(lines []string, scanned []tomlLine, candidates []tomlAssignment, targetParts []string, formattedVal string) []byte {
	if len(targetParts) == 1 {
		return appendRootKey(lines, scanned, targetParts[0], formattedVal)
	}

	tableParts := targetParts[:len(targetParts)-1]
	leaf := targetParts[len(targetParts)-1]

	return appendTableKey(lines, scanned, candidates, tableParts, leaf, formattedVal)
}

func appendRootKey(lines []string, scanned []tomlLine, key, formattedVal string) []byte {
	insertIdx := len(lines)

	for i, info := range scanned {
		if info.header != nil {
			insertIdx = i
			break
		}
	}

	newLine := fmt.Sprintf("%s = %s", formatTOMLKey(key), formattedVal)
	lines = slices.Insert(lines, insertIdx, newLine)

	return []byte(strings.Join(lines, "\n"))
}

func appendTableKey(lines []string, scanned []tomlLine, candidates []tomlAssignment, tableParts []string, leaf, formattedVal string) []byte {
	tableIdx := -1

	for i, info := range scanned {
		if info.header != nil && partsEqual(info.header, tableParts) {
			tableIdx = i
			break
		}
	}

	if tableIdx != -1 {
		insertIdx := len(lines)

		for i := tableIdx + 1; i < len(lines); i++ {
			if scanned[i].header != nil {
				insertIdx = i
				break
			}
		}

		newLine := fmt.Sprintf("%s = %s", formatTOMLKey(leaf), formattedVal)
		lines = slices.Insert(lines, insertIdx, newLine)

		return []byte(strings.Join(lines, "\n"))
	}

	// A table defined by dotted keys, such as server.port = 80, cannot also get
	// a [server] header; the new key joins it as another dotted key.
	if last, ok := lastDottedDefinition(candidates, tableParts); ok {
		relative := append(slices.Clone(tableParts[len(last.currentTable):]), leaf)
		newLine := fmt.Sprintf("%s = %s", formatTableHeader(relative), formattedVal)
		lines = slices.Insert(lines, last.endIdx+1, newLine)

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

func lastDottedDefinition(candidates []tomlAssignment, tableParts []string) (tomlAssignment, bool) {
	var (
		last  tomlAssignment
		found bool
	)

	for _, cand := range candidates {
		if len(cand.currentTable) < len(tableParts) && len(cand.fullKey) > len(tableParts) &&
			partsEqual(cand.fullKey[:len(tableParts)], tableParts) {
			last, found = cand, true
		}
	}

	return last, found
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
		if !isBareTOMLKeyByte(s[i]) {
			return false
		}
	}

	return true
}

func isBareTOMLKeyByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func updateInlineTable(inlineText string, parts []string, value any) (string, error) {
	var m map[string]any

	dummyDoc := "dummy = " + inlineText
	if err := toml.Unmarshal([]byte(dummyDoc), &m); err != nil {
		return "", fmt.Errorf("parse inline table: %w", err)
	}

	tableVal, ok := m["dummy"]
	if !ok {
		return "", ErrPathNotMapping
	}

	tableMap, ok := tableVal.(map[string]any)
	if !ok {
		return "", ErrPathNotMapping
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
		return ErrPathNotMapping
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
