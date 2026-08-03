package prune

import (
	"bufio"
	"os"
	"strings"
)

// LoadKeepList reads one key per line, skipping blank lines and "#"
// comments -- the mandatory-keep list format both `validate` and `apply`
// enforce. A missing file is not an error: it means no mandatory-keep list
// has been set up yet, equivalent to an empty one.
func LoadKeepList(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	keep := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keep[line] = true
	}
	return keep, scanner.Err()
}
