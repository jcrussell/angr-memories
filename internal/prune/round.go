package prune

import "regexp"

// roundNameRE bounds --round to something safe to use as a directory
// component (rounds/<round>/...) -- shared by chunk/validate/apply so the
// rule can't drift between them.
var roundNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidRoundName reports whether name is safe to use as a round directory
// name under rounds/.
func ValidRoundName(name string) bool {
	return roundNameRE.MatchString(name)
}

// RoundNamePattern is the human-readable form of the round-name rule, for
// error messages.
const RoundNamePattern = `^[a-zA-Z0-9_-]+$`
