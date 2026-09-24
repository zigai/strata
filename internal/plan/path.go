package plan

import (
	"fmt"
	"strings"
)

func formatIndexPath(indexPath []int) string {
	parts := make([]string, 0, len(indexPath))
	for _, index := range indexPath {
		parts = append(parts, fmt.Sprintf("[%d]", index))
	}

	return strings.Join(parts, "")
}
