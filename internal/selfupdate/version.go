package selfupdate

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a strict MAJOR.MINOR.PATCH release number. Pre-release and build
// suffixes are deliberately not accepted: such builds are not comparable.
type Version struct{ Major, Minor, Patch int }

var versionPattern = regexp.MustCompile(`^v?(\d{1,9})\.(\d{1,9})\.(\d{1,9})$`)

// ParseVersion accepts "1.2.3" and "v1.2.3" and nothing else.
func ParseVersion(s string) (Version, bool) {
	groups := versionPattern.FindStringSubmatch(s)
	if groups == nil {
		return Version{}, false
	}
	var v Version
	for i, field := range []*int{&v.Major, &v.Minor, &v.Patch} {
		number, err := strconv.Atoi(groups[i+1])
		if err != nil {
			return Version{}, false
		}
		*field = number
	}
	return v, true
}

// String renders the version with a leading "v", as release tags do.
func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare returns -1, 0 or 1.
func (v Version) Compare(other Version) int {
	for _, field := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}} {
		if field[0] < field[1] {
			return -1
		}
		if field[0] > field[1] {
			return 1
		}
	}
	return 0
}
