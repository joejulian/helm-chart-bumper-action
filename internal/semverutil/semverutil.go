package semverutil

import (
	"fmt"
	"strconv"
	"strings"
)

type ChangeLevel int

const (
	NoChange ChangeLevel = iota
	PatchChange
	MinorChange
	MajorChange
)

func Max(a, b ChangeLevel) ChangeLevel {
	if a > b {
		return a
	}
	return b
}

type Version struct {
	Major int
	Minor int
	Patch int
}

func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid semver: %q", s)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid semver major: %w", err)
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid semver minor: %w", err)
	}
	pat, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid semver patch: %w", err)
	}
	return Version{Major: maj, Minor: min, Patch: pat}, nil
}

// Compare returns the semantic version change level from a -> b.
// If either version is not parseable semver (x.y.z or vx.y.z), it returns NoChange.
func Compare(a, b string) ChangeLevel {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return NoChange
	}
	va, errA := Parse(a)
	vb, errB := Parse(b)
	if errA != nil || errB != nil {
		return NoChange
	}
	if va.Major != vb.Major {
		return MajorChange
	}
	if va.Minor != vb.Minor {
		return MinorChange
	}
	if va.Patch != vb.Patch {
		return PatchChange
	}
	return NoChange
}

func BumpChartVersion(current string, lvl ChangeLevel) (string, error) {
	v, err := Parse(current)
	if err != nil {
		return "", err
	}
	switch lvl {
	case MajorChange:
		return fmt.Sprintf("%d.0.0", v.Major+1), nil
	case MinorChange:
		return fmt.Sprintf("%d.%d.0", v.Major, v.Minor+1), nil
	case PatchChange:
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch+1), nil
	default:
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch), nil
	}
}
