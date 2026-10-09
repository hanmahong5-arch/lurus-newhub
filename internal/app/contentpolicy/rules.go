package contentpolicy

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
)

// Rule field vocabularies (mirrored by the content_rules column defaults).
const (
	ScopePlatform = "platform"
	ScopeTenant   = "tenant"

	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleAny       = "any"

	KindMask   = "mask"
	KindReject = "reject"

	PatternBuiltin = "builtin"
	PatternRegex   = "regex"

	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

const (
	// MaxPatternLen bounds a custom regex source. Go's regexp is RE2 (linear
	// time) so this is defence in depth, not the only ReDoS control.
	MaxPatternLen = 512
	// MaxRulesPerScope bounds the rules one tenant (or the platform) holds.
	MaxRulesPerScope = 50
	// maxProgInsts bounds the compiled program size of a custom regex.
	maxProgInsts = 2000
	// MaxReplacementLen bounds the mask placeholder.
	MaxReplacementLen = 64
)

// Rule is one content rule. It is the persisted shape (repo.ContentRule
// mirrors it) so the engine and storage cannot drift.
type Rule struct {
	Id          int64
	Scope       string
	TenantId    string
	Ordinal     int64
	Name        string
	RoleScope   string
	Kind        string
	PatternType string
	Builtin     string
	Pattern     string
	Replacement string
	Mode        string
	Enabled     bool
}

// ErrInvalidRule wraps every validation failure.
var ErrInvalidRule = errors.New("invalid content rule")

func invalid(format string, a ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalidRule, fmt.Sprintf(format, a...))
}

// ValidateRule checks a rule before it is stored. It compiles custom
// patterns so a bad regex is refused at write time, not at request time.
func ValidateRule(r *Rule) error {
	switch r.Scope {
	case ScopePlatform:
		if r.TenantId != "" {
			return invalid("platform rule must not carry a tenant_id")
		}
	case ScopeTenant:
		if r.TenantId == "" {
			return invalid("tenant rule needs a tenant_id")
		}
	default:
		return invalid("scope must be platform or tenant")
	}
	switch r.RoleScope {
	case RoleSystem, RoleUser, RoleAssistant, RoleAny:
	default:
		return invalid("role_scope must be system, user, assistant or any")
	}
	switch r.Kind {
	case KindMask, KindReject:
	default:
		return invalid("kind must be mask or reject")
	}
	switch r.Mode {
	case ModeObserve, ModeEnforce:
	default:
		return invalid("mode must be observe or enforce")
	}
	if len(r.Name) > 64 {
		return invalid("name longer than 64 bytes")
	}
	if len(r.Replacement) > MaxReplacementLen {
		return invalid("replacement longer than %d bytes", MaxReplacementLen)
	}
	switch r.PatternType {
	case PatternBuiltin:
		if _, ok := builtinDetectors[r.Builtin]; !ok {
			return invalid("unknown builtin %q", r.Builtin)
		}
		if r.Pattern != "" {
			return invalid("builtin rule must not carry a pattern")
		}
	case PatternRegex:
		if r.Builtin != "" {
			return invalid("regex rule must not name a builtin")
		}
		if _, err := compileCustom(r.Pattern); err != nil {
			return err
		}
	default:
		return invalid("pattern_type must be builtin or regex")
	}
	return nil
}

// compileCustom compiles a user-supplied regex under the length, program-size
// and non-empty-match limits.
func compileCustom(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, invalid("pattern is empty")
	}
	if len(p) > MaxPatternLen {
		return nil, invalid("pattern longer than %d bytes", MaxPatternLen)
	}
	re, err := syntax.Parse(p, syntax.Perl)
	if err != nil {
		return nil, invalid("pattern does not compile")
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, invalid("pattern does not compile")
	}
	if len(prog.Inst) > maxProgInsts {
		return nil, invalid("pattern too complex")
	}
	rx, err := regexp.Compile(p)
	if err != nil {
		return nil, invalid("pattern does not compile")
	}
	if rx.MatchString("") {
		return nil, invalid("pattern must not match the empty string")
	}
	return rx, nil
}

// compiledRule pairs a rule with its matcher.
type compiledRule struct {
	Rule
	find func(s string) [][2]int
	repl string
}

// Ruleset is an ordered, compiled set of rules for one request's tenant.
type Ruleset struct {
	rules []compiledRule
}

// Len is the number of active rules.
func (rs *Ruleset) Len() int {
	if rs == nil {
		return 0
	}
	return len(rs.rules)
}

// Compile orders (platform first, then tenant; by ordinal, then id) and
// compiles the enabled rules. A rule that fails to compile is skipped and
// reported in the returned error list rather than disabling the whole set:
// one bad row must not silently turn every other rule off.
func Compile(rules []Rule) (*Ruleset, []error) {
	sorted := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			sorted = append(sorted, r)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Scope != b.Scope {
			return a.Scope == ScopePlatform
		}
		if a.Ordinal != b.Ordinal {
			return a.Ordinal < b.Ordinal
		}
		return a.Id < b.Id
	})
	rs := &Ruleset{}
	var errs []error
	for _, r := range sorted {
		cr := compiledRule{Rule: r, repl: r.Replacement}
		switch r.PatternType {
		case PatternBuiltin:
			d, ok := builtinDetectors[r.Builtin]
			if !ok {
				errs = append(errs, invalid("rule %d: unknown builtin %q", r.Id, r.Builtin))
				continue
			}
			cr.find = d.find
			if cr.repl == "" {
				cr.repl = d.placeholder
			}
		case PatternRegex:
			rx, err := compileCustom(r.Pattern)
			if err != nil {
				errs = append(errs, fmt.Errorf("rule %d: %w", r.Id, err))
				continue
			}
			cr.find = func(s string) [][2]int {
				locs := rx.FindAllStringIndex(s, -1)
				out := make([][2]int, 0, len(locs))
				for _, l := range locs {
					out = append(out, [2]int{l[0], l[1]})
				}
				return out
			}
			if cr.repl == "" {
				cr.repl = "[MASKED]"
			}
		default:
			errs = append(errs, invalid("rule %d: bad pattern_type", r.Id))
			continue
		}
		rs.rules = append(rs.rules, cr)
	}
	return rs, errs
}

func roleMatches(scope, role string) bool {
	return scope == RoleAny || scope == role
}

// maskSpans replaces the spans of s with repl. Spans must be sorted and
// non-overlapping (every detector guarantees it).
func maskSpans(s string, spans [][2]int, repl string) string {
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		if sp[0] < last {
			continue
		}
		b.WriteString(s[last:sp[0]])
		b.WriteString(repl)
		last = sp[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
