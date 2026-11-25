package plugins

import (
	"strconv"
	"strings"
)

// Semantic versions, as the catalog and engines.textus write them
// (semver.org 2.0): MAJOR.MINOR.PATCH, a pre-release after "-", build
// metadata after "+" (which doesn't order).

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string
}

func numeric(s string) (uint64, bool) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return true
}

// ParseVersion reads s strictly; false for anything else.
func ParseVersion(s string) (Version, bool) {
	s, build, hasBuild := strings.Cut(s, "+")
	if hasBuild {
		for _, b := range strings.Split(build, ".") {
			if !identifier(b) {
				return Version{}, false
			}
		}
	}
	core, pre, hasPre := strings.Cut(s, "-")
	nums := strings.Split(core, ".")
	if len(nums) != 3 {
		return Version{}, false
	}
	var v Version
	for i, p := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		n, ok := numeric(nums[i])
		if !ok {
			return Version{}, false
		}
		*p = n
	}
	if hasPre {
		for _, id := range strings.Split(pre, ".") {
			if !identifier(id) {
				return Version{}, false
			}
			if _, err := strconv.ParseUint(id, 10, 64); err == nil && len(id) > 1 && id[0] == '0' {
				return Version{}, false
			}
			v.Pre = append(v.Pre, id)
		}
	}
	return v, true
}

func comparePre(a, b string) int {
	an, aNum := numeric(a)
	bn, bNum := numeric(b)
	switch {
	case aNum && bNum:
		return compareUint(an, bn)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Compare orders v and w: -1, 0 or 1. A pre-release comes before its
// release.
func (v Version) Compare(w Version) int {
	for _, c := range []int{compareUint(v.Major, w.Major), compareUint(v.Minor, w.Minor), compareUint(v.Patch, w.Patch)} {
		if c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		if c := comparePre(v.Pre[i], w.Pre[i]); c != 0 {
			return c
		}
	}
	return compareUint(uint64(len(v.Pre)), uint64(len(w.Pre)))
}

// String is v as written, build metadata left out.
func (v Version) String() string {
	s := strconv.FormatUint(v.Major, 10) + "." + strconv.FormatUint(v.Minor, 10) + "." + strconv.FormatUint(v.Patch, 10)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// IsNewer is whether offered is a later version than have, both semver; a
// version either can't be read as is never newer.
func IsNewer(have, offered string) bool {
	h, ok1 := ParseVersion(strings.TrimSpace(have))
	o, ok2 := ParseVersion(strings.TrimSpace(offered))
	return ok1 && ok2 && o.Compare(h) > 0
}

// RunsOn is whether Textus version is in range, as engineSatisfied
// (versions.ts) reads it: `^1.3.0`, `>=1.3.0`, `1.3.x`, exact or `*`. A
// pre-release of Textus counts as its release unless the range names a
// pre-release.
func RunsOn(rangeText, version string) bool {
	r := strings.TrimSpace(rangeText)
	if r == "*" {
		return true
	}
	base := strings.TrimSpace(trimRepeated(trimRepeated(r, ">="), "^"))
	release := version
	if !strings.Contains(base, "-") {
		release, _, _ = strings.Cut(version, "-")
	}
	have, ok := ParseVersion(release)
	if !ok {
		return false
	}
	if minor, ok := strings.CutSuffix(r, ".x"); ok {
		return strconv.FormatUint(have.Major, 10)+"."+strconv.FormatUint(have.Minor, 10) == minor
	}
	want, ok := ParseVersion(base)
	if !ok {
		return false
	}
	switch {
	case strings.HasPrefix(r, ">="):
		return have.Compare(want) >= 0
	case strings.HasPrefix(r, "^"):
		return have.Compare(want) >= 0 && have.Major == want.Major
	}
	return have.Compare(want) == 0
}

// trimRepeated is s without every leading copy of prefix.
func trimRepeated(s, prefix string) string {
	for strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	return s
}
