package contentpolicy

import (
	"regexp"
	"strings"
)

// Builtin rule names (China-scenario PII and credential shapes).
const (
	BuiltinPhoneCN   = "phone_cn"
	BuiltinIDCardCN  = "id_card_cn"
	BuiltinBankCard  = "bank_card"
	BuiltinEmail     = "email"
	BuiltinSecretKey = "secret_key"
)

// BuiltinNames lists the builtin detector names, for UI and validation.
func BuiltinNames() []string {
	return []string{BuiltinPhoneCN, BuiltinIDCardCN, BuiltinBankCard, BuiltinEmail, BuiltinSecretKey}
}

type detector struct {
	placeholder string
	find        func(s string) [][2]int
}

var builtinDetectors = map[string]detector{
	BuiltinPhoneCN:   {"[PHONE]", findPhoneCN},
	BuiltinIDCardCN:  {"[ID_CARD]", findIDCardCN},
	BuiltinBankCard:  {"[BANK_CARD]", findBankCard},
	BuiltinEmail:     {"[EMAIL]", findEmail},
	BuiltinSecretKey: {"[SECRET]", findSecretKey},
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isAlnum(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// boundedBy reports whether s[start:end] is not glued to a neighbouring
// character satisfying glued (RE2 has no lookaround, so it is done by hand).
func boundedBy(s string, start, end int, glued func(byte) bool) bool {
	if start > 0 && glued(s[start-1]) {
		return false
	}
	if end < len(s) && glued(s[end]) {
		return false
	}
	return true
}

var phoneRe = regexp.MustCompile(`1[3-9][0-9]{9}`)

// findPhoneCN matches mainland mobile numbers (11 digits, 1[3-9]...) that are
// not part of a longer digit run.
func findPhoneCN(s string) [][2]int {
	var out [][2]int
	for _, l := range phoneRe.FindAllStringIndex(s, -1) {
		if boundedBy(s, l[0], l[1], isDigit) {
			out = append(out, [2]int{l[0], l[1]})
		}
	}
	return out
}

var idCardRe = regexp.MustCompile(`[0-9]{17}[0-9Xx]`)

var idWeights = [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}

const idCheckChars = "10X98765432"

// ValidIDCardCN verifies an 18-character resident ID: plausible birth date
// and the ISO 7064 mod 11-2 check character.
func ValidIDCardCN(id string) bool {
	if len(id) != 18 {
		return false
	}
	sum := 0
	for i := 0; i < 17; i++ {
		if !isDigit(id[i]) {
			return false
		}
		sum += int(id[i]-'0') * idWeights[i]
	}
	last := id[17]
	if last == 'x' {
		last = 'X'
	}
	if idCheckChars[sum%11] != last {
		return false
	}
	year := atoi(id[6:10])
	month := atoi(id[10:12])
	day := atoi(id[12:14])
	if year < 1900 || year > 2200 || month < 1 || month > 12 || day < 1 || day > daysIn(year, month) {
		return false
	}
	return true
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

func findIDCardCN(s string) [][2]int {
	var out [][2]int
	for _, l := range idCardRe.FindAllStringIndex(s, -1) {
		if boundedBy(s, l[0], l[1], isAlnum) && ValidIDCardCN(s[l[0]:l[1]]) {
			out = append(out, [2]int{l[0], l[1]})
		}
	}
	return out
}

// bankRe finds 16-19 digit runs optionally grouped by single spaces/dashes.
var bankRe = regexp.MustCompile(`[0-9](?:[ -]?[0-9]){15,18}`)

// LuhnValid reports whether the digit string passes the Luhn checksum.
func LuhnValid(digits string) bool {
	if len(digits) < 2 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		if !isDigit(digits[i]) {
			return false
		}
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

func findBankCard(s string) [][2]int {
	var out [][2]int
	for _, l := range bankRe.FindAllStringIndex(s, -1) {
		raw := s[l[0]:l[1]]
		digits := strings.NewReplacer(" ", "", "-", "").Replace(raw)
		if len(digits) < 16 || len(digits) > 19 {
			continue
		}
		if !boundedBy(s, l[0], l[1], isDigit) {
			continue
		}
		if LuhnValid(digits) {
			out = append(out, [2]int{l[0], l[1]})
		}
	}
	return out
}

var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)

func findEmail(s string) [][2]int {
	var out [][2]int
	for _, l := range emailRe.FindAllStringIndex(s, -1) {
		out = append(out, [2]int{l[0], l[1]})
	}
	return out
}

// secretRe covers common credential shapes: sk- style API keys, AWS access
// key ids, GitHub tokens and PEM private key headers.
var secretRe = regexp.MustCompile(
	`sk-[A-Za-z0-9_\-]{20,}` +
		`|AKIA[0-9A-Z]{16}` +
		`|gh[pousr]_[A-Za-z0-9]{36,}` +
		`|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

func findSecretKey(s string) [][2]int {
	var out [][2]int
	for _, l := range secretRe.FindAllStringIndex(s, -1) {
		out = append(out, [2]int{l[0], l[1]})
	}
	return out
}
