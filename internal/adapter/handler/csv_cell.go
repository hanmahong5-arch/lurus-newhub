package handler

import "strings"

// csvFormulaTriggers are the leading characters a spreadsheet treats as the
// start of a formula rather than as data. Excel, LibreOffice Calc and
// Google Sheets all evaluate a cell beginning with one of these on import.
//
// The tab and carriage return are in the list because an importer strips
// those two from the head of the cell before the formula parser looks at
// it, so "\t=cmd|…" is evaluated exactly like "=cmd|…" while sailing past a
// naive check that only looks for the four visible characters. A leading
// SPACE is not stripped that way — " =1+1" stays text — which is why space
// is deliberately absent from this list; csv_cell_test.go's leading_space
// row pins that it is left alone.
const csvFormulaTriggers = "=+-@\t\r"

// csvCell neutralises one CSV cell against formula injection by prefixing a
// single quote, which every mainstream spreadsheet reads as "the rest of
// this cell is literal text".
//
// Why this is not encoding/csv's job: CSV quoting protects the CSV GRAMMAR
// (a comma or a newline inside a field). The spreadsheet's formula parser
// runs AFTER the field has been unquoted, so a perfectly quoted
// "=HYPERLINK(…)" is still a live formula when the file is opened — which
// is how an exported token name or audit details blob becomes code running
// on a finance or compliance reader's machine.
//
// Cost, stated plainly: a cell whose real value is a negative number
// ("-500") is exported as "'-500" and read as text, not as a number, by a
// spreadsheet. That is the accepted trade — the alternative (exempting
// anything that parses as a number) is a second parser to keep in sync with
// the spreadsheet's, and every numeric column in these three exports is
// produced by strconv from an integer the gateway itself computed.
// csv_cell_test.go pins both halves of this behaviour.
func csvCell(s string) string {
	if s == "" {
		return s
	}
	if strings.ContainsRune(csvFormulaTriggers, rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvRow applies csvCell to every cell of one row. The exports call this
// instead of building a []string literal so that adding a column to an
// export cannot quietly add an unescaped one: there is no per-cell call site
// to forget.
func csvRow(cells ...string) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = csvCell(cell)
	}
	return out
}
