package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/render"
)

// EnvDSN is the variable the generated program reads its connection string
// from.
//
// The name is the whole point. A DSN interpolated into the source would land in
// <tmpdir>/main.go, in :src, in what :save writes to a scratch module, and in
// the build cache keyed to that text. A program that reads an environment
// variable holds no secret at all, so there is nothing in any of those places
// to leak — and a build error, which quotes source, cannot carry one either.
const EnvDSN = "GLUON_DB_DSN"

// MaxRows is how many rows one statement fetches.
//
// It is a policy, not a limit the encoder imposes: the result arrives as a
// single string, which the child's value encoder sends whole, so neither
// maxItems nor maxNodes applies. What it is really bounding is how much of a
// table is useful in a REPL — and when it binds, the report says so rather than
// implying the query returned exactly this much.
const MaxRows = 200

// Wire markers. The child describes; gluon formats. The same split the whole
// project runs on, and the reason a driver-specific value never has to be
// rendered twice.
const (
	sep      = "\x1f" // between cells, and after a line's tag
	tagCols  = "cols"
	tagTypes = "types"
	tagRow   = "row"
	tagErr   = "err"
	tagNote  = "note"
	tagMore  = "more"
	nullCell = "\x00" // a column that was SQL NULL, not the text "NULL"
)

// Source-literal forms of the wire markers.
//
// The separator is \x1f, and it has to reach the generated program as an escape
// sequence rather than a raw byte. A literal control character would still
// compile — Go allows one inside an interpreted string — but it is invisible in
// :src, and gofmt runs over that source on every line.
var (
	litCols  = strconv.Quote(tagCols + sep)
	litTypes = strconv.Quote(tagTypes + sep)
	litRow   = strconv.Quote(tagRow + sep)
	litErr   = strconv.Quote(tagErr + sep)
	litNote  = strconv.Quote(tagNote + sep)
	litMore  = strconv.Quote(tagMore + sep)
	litSep   = strconv.Quote(sep)
	litNull  = strconv.Quote(nullCell)
)

// Imports are what the generated program needs, beyond the driver.
//
// They are supplied rather than left to goimports for two reasons: it costs
// ~135ms, and the blank driver import has to be written explicitly anyway since
// no qualifier names it. Every one of these is used by every generated program,
// so none of them can become an "imported and not used".
func Imports(drv Driver) []render.ImportSpec {
	return []render.ImportSpec{
		{Path: "database/sql"},
		{Path: "context"},
		{Path: "fmt"},
		{Path: "os"},
		{Path: "strings"},
		{Path: "time"},
		{Name: "_", Path: drv.Import},
	}
}

// readOnlyFirstWord is the statements :query runs without -w.
//
// It is a keyword check and it says so: it is not a SQL parser, and it does not
// claim to be one. What it catches is the typo and the paste — DROP where a
// SELECT was meant — which is the failure that actually happens at a REPL. The
// read-only transaction underneath it is what a server enforces.
var readOnlyFirstWord = map[string]bool{
	"select": true, "with": true, "show": true, "explain": true,
	"describe": true, "desc": true, "pragma": true, "table": true, "values": true,
}

// Statement is what to run and how.
type Statement struct {
	SQL   string
	Write bool // the user passed -w
	// Timeout bounds the query inside the child, so a hung connect fails with a
	// message instead of wedging the REPL for the evaluator's whole timeout.
	// A Go duration string; empty means defaultTimeout.
	Timeout string
}

// defaultTimeout bounds a statement inside the child.
//
// The evaluator's own timeout would eventually fire too, but it is measured for
// compiling and running a program, not for waiting on a network. A hung connect
// should say so in seconds rather than wedge the prompt for the full session
// timeout with no explanation.
const defaultTimeout = 15 * time.Second

// goDuration renders a duration as Go source.
//
// Whole seconds become 15*time.Second rather than time.Duration(15000000000),
// because :src shows this program to the person who typed the query and a
// nanosecond count is not something anyone reads.
func goDuration(spec string) string {
	d := defaultTimeout
	if spec != "" {
		if parsed, err := time.ParseDuration(spec); err == nil && parsed > 0 {
			d = parsed
		}
	}
	if d%time.Second == 0 {
		return strconv.FormatInt(int64(d/time.Second), 10) + "*time.Second"
	}
	return "time.Duration(" + strconv.FormatInt(int64(d), 10) + ")"
}

// CheckStatement reports whether a statement may run without -w.
func CheckStatement(sql string, write bool) error {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return fmt.Errorf("usage: :query <sql>   e.g. :query SELECT id, email FROM users LIMIT 5")
	}
	if write {
		return nil
	}
	first := strings.ToLower(strings.TrimLeft(strings.Fields(trimmed)[0], "("))
	if readOnlyFirstWord[first] {
		return nil
	}
	return fmt.Errorf("%s is not a read-only statement — :query -w %s runs it anyway.\n"+
		"      The check is on the first word, not a parse: it is here to catch the paste "+
		"you did not mean, not to prove a statement is safe", strings.ToUpper(first), trimmed)
}

// execBlock is how the statement actually reaches the database.
//
// A read runs inside a read-only transaction, which is the thing a server
// enforces rather than a promise gluon makes. A write runs with no wrapper at
// all.
//
// The first version wrapped both, with the ReadOnly flag tracking -w and a
// deferred Rollback either way — so every write was silently discarded. A
// CREATE TABLE reported success and the table did not exist, which is precisely
// the quiet wrongness this project rejected an interpreter to avoid, and an
// integration test is what caught it. Committing instead would also work, but a
// transaction the user did not ask for is behaviour gluon would then have to
// explain, and the wrapper exists only for the guarantee -w has opted out of.
func execBlock(write bool, q string) string {
	if write {
		return "	__rows, __err = __db.QueryContext(__ctx, " + q + ")\n"
	}
	return "	__tx, __terr := __db.BeginTx(__ctx, &sql.TxOptions{ReadOnly: true})\n" +
		"	if __terr == nil {\n" +
		"		defer __tx.Rollback()\n" +
		"		__rows, __err = __tx.QueryContext(__ctx, " + q + ")\n" +
		"	} else {\n" +
		"		__note = \"this driver has no read-only transaction (\" + __terr.Error() + " +
		"\"); the statement ran outside one\"\n" +
		"		__rows, __err = __db.QueryContext(__ctx, " + q + ")\n" +
		"	}\n"
}

// Rewrite turns a statement into the Go expression that runs it.
//
// The expression is handed to the ordinary evaluator, so it can do nothing a
// typed line could not — the same property that makes a plugin command safe.
// What it returns is one string, which gluon parses back into a table; the
// child describes, gluon formats, and no driver-specific value is rendered
// twice.
func Rewrite(st Statement, drv Driver) (string, error) {
	if err := CheckStatement(st.SQL, st.Write); err != nil {
		return "", err
	}
	timeout := goDuration(st.Timeout)

	// strconv.Quote, never a raw string literal. MySQL quotes identifiers with
	// backticks, so `SELECT `order`.id` inside a raw string ends the literal
	// early and produces a syntax error in Go that blames the wrong language.
	q := strconv.Quote(st.SQL)

	var b strings.Builder
	b.WriteString(`func() string {
	__lines := []string{}
	__fail := func(e error) string { return ` + litErr + ` + e.Error() }
	__db, __err := sql.Open(` + strconv.Quote(drv.Name) + `, os.Getenv(` + strconv.Quote(EnvDSN) + `))
	if __err != nil { return __fail(__err) }
	defer __db.Close()
	__ctx, __cancel := context.WithTimeout(context.Background(), ` + timeout + `)
	defer __cancel()
	__note := ""
	var __rows *sql.Rows
` + execBlock(st.Write, q) + `	if __err != nil { return __fail(__err) }
	defer __rows.Close()
	__cols, __err := __rows.Columns()
	if __err != nil { return __fail(__err) }
	__lines = append(__lines, ` + litCols + `+strings.Join(__cols, ` + litSep + `))
	// ColumnTypes is asked, never required. A driver that cannot answer it has
	// still answered the query, and the rows are what was wanted; so the tag is
	// simply absent and the parser leaves Types nil, rather than a metadata
	// call turning a successful statement into a failure.
	if __ct, __cterr := __rows.ColumnTypes(); __cterr == nil {
		__types := make([]string, len(__ct))
		for __i, __c := range __ct { __types[__i] = __c.DatabaseTypeName() }
		__lines = append(__lines, ` + litTypes + `+strings.Join(__types, ` + litSep + `))
	}
	__vals := make([]any, len(__cols))
	__ptrs := make([]any, len(__cols))
	for __i := range __vals { __ptrs[__i] = &__vals[__i] }
	__n := 0
	__more := false
	for __rows.Next() {
		if __n == ` + strconv.Itoa(MaxRows) + ` { __more = true; break }
		if __err := __rows.Scan(__ptrs...); __err != nil { return __fail(__err) }
		__cells := make([]string, len(__cols))
		for __i, __v := range __vals {
			switch __t := __v.(type) {
			case nil:
				// A sentinel, so a column whose text is literally "NULL" is not
				// reported as SQL NULL. They mean different things and one of
				// them is usually the answer somebody is looking for.
				__cells[__i] = ` + litNull + `
			case []byte:
				// Text columns arrive as []byte from several drivers. Printed
				// as a slice they read as a list of small integers, which is
				// the json.RawMessage failure in another costume.
				__cells[__i] = string(__t)
			case time.Time:
				__cells[__i] = __t.Format(time.RFC3339Nano)
			default:
				__cells[__i] = fmt.Sprint(__t)
			}
		}
		__lines = append(__lines, ` + litRow + `+strings.Join(__cells, ` + litSep + `))
		__n++
	}
	if __err := __rows.Err(); __err != nil { return __fail(__err) }
	if __more { __lines = append(__lines, ` + litMore + `) }
	if __note != "" { __lines = append(__lines, ` + litNote + `+__note) }
	return strings.Join(__lines, "\n")
}()`)
	return b.String(), nil
}

// Result is a parsed answer from the child.
type Result struct {
	Cols []string
	// Types is the type each column was declared with, in Cols order, or nil
	// when the driver would not say. A driver that answers with an empty name
	// for a column leaves "" there: reporting what was said, including nothing,
	// beats inventing a type gluon does not know.
	Types []string
	Rows  [][]string
	// Null[i][j] marks a cell that was SQL NULL rather than the text "NULL".
	Null [][]bool
	// More reports that the fetch cap bound, so the report can say so instead
	// of implying the query returned exactly MaxRows.
	More bool
	Note string
	Err  string
}

// ParseResult reads back what Rewrite's program printed.
func ParseResult(out string) Result {
	var r Result
	for _, line := range strings.Split(out, "\n") {
		tag, rest, ok := strings.Cut(line, sep)
		if !ok {
			continue
		}
		switch tag {
		case tagCols:
			r.Cols = strings.Split(rest, sep)
		case tagTypes:
			r.Types = strings.Split(rest, sep)
		case tagRow:
			cells := strings.Split(rest, sep)
			nulls := make([]bool, len(cells))
			for i, c := range cells {
				if c == nullCell {
					cells[i], nulls[i] = "NULL", true
				}
			}
			r.Rows = append(r.Rows, cells)
			r.Null = append(r.Null, nulls)
		case tagMore:
			r.More = true
		case tagNote:
			r.Note = rest
		case tagErr:
			r.Err = rest
		}
	}
	return r
}
