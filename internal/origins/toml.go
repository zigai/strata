package origins

import (
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

// TOML values are rendered from decoded values, rather than the original text.
func readTOML(data []byte, emit func(Record)) bool {
	var document map[string]any

	if err := toml.Unmarshal(data, &document); err != nil || len(document) == 0 {
		return false
	}

	walkTOMLValue(document, "", emit)

	return true
}

func walkTOMLValue(value any, key jsontext.Pointer, emit func(Record)) {
	if document, ok := value.(map[string]any); ok && len(document) > 0 {
		for _, name := range slices.Sorted(maps.Keys(document)) {
			walkTOMLValue(document[name], key.AppendToken(name), emit)
		}

		return
	}

	emit(Record{Key: key, Line: 0, RawValue: fmt.Sprintf("%v", value), IsTemplate: false})

	sequence := reflect.ValueOf(value)
	if sequence.Kind() != reflect.Slice && sequence.Kind() != reflect.Array {
		return
	}

	for index := range sequence.Len() {
		item := sequence.Index(index).Interface()

		kind := reflect.TypeOf(item)
		if kind != nil && (kind.Kind() == reflect.Map || kind.Kind() == reflect.Slice || kind.Kind() == reflect.Array) {
			walkTOMLValue(item, key.AppendToken(strconv.Itoa(index)), emit)
		}
	}
}
