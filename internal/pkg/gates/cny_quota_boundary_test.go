package gates

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Quota is priced in USD (QuotaPerUnit quota = $1); the platform wallet,
// invoices and topups are in CNY. Until 2026-09-23 twelve lines converted
// between the two with a bare quota/QuotaPerUnit (or amount*QuotaPerUnit),
// which treats CNY 1 as $1 and charged customers 1/7.3 of the real cost.
// The only correct bridge is internal/pkg/currency (LucToLut / QuotaToCNY /
// CNYToQuota), which applies USDExchangeRate.
//
// This gate fails on any non-comment line of production Go code, outside
// internal/pkg/currency, that mentions QuotaPerUnit together with a CNY or
// wallet-side name (balance/spend/available included: the wallet-balance
// endpoints named their fields that way). USD arithmetic (pricing, display
// in USD) is untouched. Run against the unfixed tree it flags all 12 lines.
var cnyBoundaryName = regexp.MustCompile(`(?i)(cny|rmb|yuan|wallet|balance|spend|available|luc\b|lucto|[a-z]LB\b|LB\s*:?=)`)

func TestNoRawQuotaPerUnitAtCNYBoundaries(t *testing.T) {
	root := repoRootForGates(t)
	var hits []string
	scanned := 0
	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if sourceSizeSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root))
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") ||
			strings.Contains(rel, "/internal/pkg/currency/") {
			return nil
		}
		scanned++
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			code := line
			if i := strings.Index(code, "//"); i >= 0 {
				code = code[:i]
			}
			if strings.Contains(code, "QuotaPerUnit") && cnyBoundaryName.MatchString(code) {
				hits = append(hits, rel+":"+itoa(n)+": "+strings.TrimSpace(line))
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned only %d Go files under internal/ — the walk is broken, not the code clean", scanned)
	}
	for _, h := range hits {
		t.Errorf("CNY boundary converts with QuotaPerUnit directly (use currency.QuotaToCNY / CNYToQuota): %s", h)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
