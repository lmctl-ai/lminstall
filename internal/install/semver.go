package install

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A small, dependency-free implementation of the node-semver subset used by
// npm dependency ranges: exact versions, comparators, ^, ~, x-ranges, hyphen
// ranges and || alternatives.

type semver struct {
	major, minor, patch int
	pre                 []string
}

var semverPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func parseSemver(s string) (semver, bool) {
	m := semverPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return semver{}, false
	}
	v := semver{}
	v.major, _ = strconv.Atoi(m[1])
	v.minor, _ = strconv.Atoi(m[2])
	v.patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (v semver) compare(o semver) int {
	if c := cmpInt(v.major, o.major); c != 0 {
		return c
	}
	if c := cmpInt(v.minor, o.minor); c != 0 {
		return c
	}
	if c := cmpInt(v.patch, o.patch); c != 0 {
		return c
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(v.pre), len(o.pre))
}

type comparator struct {
	op string
	v  semver
}

// semverRange is a disjunction of conjunctions of comparators.
type semverRange [][]comparator

var partialPattern = regexp.MustCompile(`^v?(\d+|[xX*])(?:\.(\d+|[xX*]))?(?:\.(\d+|[xX*]))?(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func isWild(s string) bool { return s == "" || s == "x" || s == "X" || s == "*" }

// expandPartial turns a possibly partial version like "1.2" or "1.x" into
// comparators. op is one of "", "^", "~", ">", ">=", "<", "<=", "=".
func expandPartial(op, text string) ([]comparator, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "*" || text == "x" || text == "X" {
		if op == "<" || op == ">" {
			return nil, fmt.Errorf("unsatisfiable range %q", op+text)
		}
		return []comparator{{op: ">=", v: semver{}}}, nil
	}
	m := partialPattern.FindStringSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("invalid semver range %q", text)
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	major, minor, patch := m[1], m[2], m[3]
	var pre []string
	if m[4] != "" {
		pre = strings.Split(m[4], ".")
	}
	lowerOnly := func(v semver) []comparator { return []comparator{{op: ">=", v: v}} }
	switch {
	case isWild(major):
		return lowerOnly(semver{}), nil
	case isWild(minor): // 1 / 1.x
		lo := semver{major: num(major)}
		hi := semver{major: num(major) + 1}
		switch op {
		case ">":
			return []comparator{{">=", hi}}, nil
		case "<":
			return []comparator{{"<", lo}}, nil
		case "<=":
			return []comparator{{"<", hi}}, nil
		case ">=":
			return lowerOnly(lo), nil
		}
		return []comparator{{">=", lo}, {"<", hi}}, nil
	case isWild(patch): // 1.2 / 1.2.x
		lo := semver{major: num(major), minor: num(minor)}
		hi := semver{major: num(major), minor: num(minor) + 1}
		switch op {
		case ">":
			return []comparator{{">=", hi}}, nil
		case "<":
			return []comparator{{"<", lo}}, nil
		case "<=":
			return []comparator{{"<", hi}}, nil
		case ">=":
			return lowerOnly(lo), nil
		case "^":
			return []comparator{{">=", lo}, {"<", semver{major: num(major) + 1}}}, nil
		}
		return []comparator{{">=", lo}, {"<", hi}}, nil
	}
	v := semver{major: num(major), minor: num(minor), patch: num(patch), pre: pre}
	switch op {
	case "", "=":
		return []comparator{{"=", v}}, nil
	case ">", ">=", "<", "<=":
		return []comparator{{op, v}}, nil
	case "~":
		return []comparator{{">=", v}, {"<", semver{major: v.major, minor: v.minor + 1}}}, nil
	case "^":
		var hi semver
		switch {
		case v.major > 0:
			hi = semver{major: v.major + 1}
		case v.minor > 0:
			hi = semver{minor: v.minor + 1}
		default:
			hi = semver{patch: v.patch + 1}
		}
		return []comparator{{">=", v}, {"<", hi}}, nil
	}
	return nil, fmt.Errorf("invalid semver operator %q", op)
}

func parseSemverRange(spec string) (semverRange, error) {
	var out semverRange
	for _, alt := range strings.Split(spec, "||") {
		alt = strings.TrimSpace(alt)
		if i := strings.Index(alt, " - "); i >= 0 { // hyphen range
			lo, e := expandPartial(">=", alt[:i])
			if e != nil {
				return nil, e
			}
			hi, e := expandPartial("<=", alt[i+3:])
			if e != nil {
				return nil, e
			}
			out = append(out, append(lo, hi...))
			continue
		}
		// Re-join an operator separated from its version by whitespace.
		alt = regexp.MustCompile(`([<>=~^]+)\s+`).ReplaceAllString(alt, "$1")
		var set []comparator
		for _, token := range strings.Fields(alt) {
			op := ""
			for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
				if strings.HasPrefix(token, o) {
					op = o
					break
				}
			}
			cs, e := expandPartial(op, token[len(op):])
			if e != nil {
				return nil, e
			}
			set = append(set, cs...)
		}
		if len(set) == 0 {
			set = []comparator{{op: ">=", v: semver{}}}
		}
		out = append(out, set)
	}
	return out, nil
}

func (c comparator) match(v semver) bool {
	n := v.compare(c.v)
	switch c.op {
	case "=":
		return n == 0
	case ">":
		return n > 0
	case ">=":
		return n >= 0
	case "<":
		return n < 0
	case "<=":
		return n <= 0
	}
	return false
}

// satisfies follows node-semver: a prerelease only matches a set that has a
// comparator on the same major.minor.patch tuple with a prerelease tag.
func (r semverRange) satisfies(v semver) bool {
	for _, set := range r {
		ok := true
		for _, c := range set {
			if !c.match(v) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if len(v.pre) == 0 {
			return true
		}
		for _, c := range set {
			if len(c.v.pre) > 0 && c.v.major == v.major && c.v.minor == v.minor && c.v.patch == v.patch {
				return true
			}
		}
	}
	return false
}

// maxSatisfying returns the highest version string among candidates.
func maxSatisfying(candidates []string, r semverRange) (string, bool) {
	best, bestText, found := semver{}, "", false
	for _, text := range candidates {
		v, ok := parseSemver(text)
		if !ok || !r.satisfies(v) {
			continue
		}
		if !found || v.compare(best) > 0 {
			best, bestText, found = v, text, true
		}
	}
	return bestText, found
}
