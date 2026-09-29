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
	// tripleQuoteLen is the length of a multi-line string delimiter.
	tripleQuoteLen = 3
	// maxClosingContentQuotes is how many quote characters a multi-line string
	// may end with directly before its closing delimiter.
	maxClosingContentQuotes = 2
)

var (
	// ErrInvalidEmptyKeyPath is returned when a dotted key path is empty.
	ErrInvalidEmptyKeyPath = errors.New("invalid empty key path")

	// ErrInvalidEmptyPathSegment is returned when a dotted key path contains an empty segment.
	ErrInvalidEmptyPathSegment = errors.New("invalid empty path segment")
)

type candAssignment struct {
	lineIdx      int
	eqIdx        int
	endIdx       int
	currentTable []string
	fullKey      []string
}

// tomlLine is what [scanTOMLLines] learned about one line.
type tomlLine struct {
	// header holds the parts of a table header on this line, or nil.
	header []string
	// eqIdx is the index of an assignment's "=", or -1.
	eqIdx int
	// end is the last line of an assignment's value, which is this line unless
	// the value is a multi-line string or array.
	end int
}

// tomlLexer follows the strings, arrays, and inline tables of TOML text across
// lines, so a quote, bracket, "=" or "#" inside a string is not read as syntax.
type tomlLexer struct {
	// quote is the delimiter of the string the text is inside, or "".
	quote string
	// depth counts the arrays and inline tables open outside strings.
	depth int
}

// UpdateTOML writes value at dottedKey. Existing assignments retain surrounding
// formatting, except replaced multiline values lose their continuation lines.
// Missing keys are added to the matching table or a new one. Keys match exactly.
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
		return nil, ErrInvalidEmptyKeyPath
	}

	if slices.Contains(targetParts, "") {
		return nil, fmt.Errorf("%w in %q", ErrInvalidEmptyPathSegment, dottedKey)
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

// open reports whether the text scanned so far leaves a string, array, or
// inline table open, so the value continues on the next line.
func (lx *tomlLexer) open() bool {
	return lx.quote != "" || lx.depth > 0
}

// scan lexes line and returns the index of the first byte stop outside a
// string, or -1. A comment ends the line, so it ends the scan unless stop is '#'.
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

// endLine closes a one-line string left open, which cannot continue below.
func (lx *tomlLexer) endLine() {
	if len(lx.quote) == 1 {
		lx.quote = ""
	}
}

// skipString advances through the content of the open string from i, closing
// the string at its delimiter, and returns the index lexing resumes at.
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

func collectAssignments(lines []string, scanned []tomlLine) []candAssignment {
	var (
		currentTable []string
		candidates   []candAssignment
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

		candidates = append(candidates, candAssignment{
			lineIdx:      i,
			eqIdx:        info.eqIdx,
			endIdx:       info.end,
			currentTable: currentTable,
			fullKey:      lineFullKey,
		})
	}

	return candidates
}

func findMatchingCandidate(candidates []candAssignment, targetParts []string) int {
	for idx, cand := range candidates {
		if partsEqual(cand.fullKey, targetParts) {
			return idx
		}
	}

	return -1
}

func handleInlineTableUpdate(lines []string, candidates []candAssignment, targetParts []string, value any) ([]string, bool, error) {
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

		return nil, false, fmt.Errorf("%w: key %q is not a table", ErrNonObjectNavigation, strings.Join(cand.fullKey, "."))
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

	return canonicalHeaderParts(inner), true
}

// isTOMLKeySyntax reports whether s is a dotted key as TOML writes one: bare
// or quoted segments joined by dots, with optional whitespace around each. An
// array such as [1, 2] or a nested array line such as ["a"]] is not.
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

// consumeTOMLKeySegment strips one bare or quoted key segment from the start
// of s and returns the rest.
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
	segments := splitDottedKey(strings.TrimSpace(raw))
	canonical := make([]string, 0, len(segments))

	// NB: splitting each segment on its own dots, rather than re-splitting the
	// joined header, keeps an empty quoted segment such as [a.""].
	for _, part := range segments {
		canonical = append(canonical, strings.Split(part, ".")...)
	}

	return canonical
}

func splitDottedKey(s string) []string {
	var (
		parts     []string
		curr      []rune
		quoted    bool
		inQuote   bool
		quoteChar rune
		// pending holds unquoted whitespace that belongs to the segment only if
		// more of the segment follows it.
		pending []rune
	)

	for _, r := range s {
		if inQuote {
			if r == quoteChar {
				inQuote = false
			} else {
				curr = append(curr, r)
			}

			continue
		}

		switch r {
		case '.':
			parts = append(parts, string(curr))
			curr, pending, quoted = nil, nil, false
		case ' ', '\t':
			// Whitespace around a segment surrounds a dot; whitespace between its
			// characters is part of the key, as in "my key".
			if len(curr) > 0 || quoted {
				pending = append(pending, r)
			}
		case '"', '\'':
			curr, pending = append(curr, pending...), nil
			inQuote, quoted, quoteChar = true, true, r
		default:
			curr, pending = append(curr, pending...), nil
			curr = append(curr, r)
		}
	}

	// NB: a quoted segment may be empty, as in "" = 1; it is still a key.
	if len(curr) > 0 || quoted {
		parts = append(parts, string(curr))
	}

	return parts
}

// findAssignmentEquals returns the index of the "=" of an assignment on line,
// or -1 when line assigns nothing.
func findAssignmentEquals(line string) int {
	var key tomlLexer

	return key.scan(line, '=')
}

// findInlineComment returns the index of the "#" starting a comment in s, a
// value that starts outside any string, or -1.
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

// updateMatchingLine replaces the value of the assignment cand, removing the
// continuation lines of a multi-line value it replaces.
func updateMatchingLine(lines []string, cand candAssignment, formattedVal string) []string {
	lines[cand.lineIdx] = replaceLineValue(lines[cand.lineIdx], cand.eqIdx, formattedVal)

	return slices.Delete(lines, cand.lineIdx+1, cand.endIdx+1)
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

func appendTOMLKey(lines []string, scanned []tomlLine, candidates []candAssignment, targetParts []string, formattedVal string) []byte {
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

func appendTableKey(lines []string, scanned []tomlLine, candidates []candAssignment, tableParts []string, leaf, formattedVal string) []byte {
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

// lastDottedDefinition returns the last assignment that defines a key inside
// tableParts through a dotted key, as in server.port = 80 for the table server.
func lastDottedDefinition(candidates []candAssignment, tableParts []string) (candAssignment, bool) {
	var (
		last  candAssignment
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
