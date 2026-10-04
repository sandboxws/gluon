package repl

import (
	"encoding/json"
	"io"
)

// The `:query -json` envelope.
//
// It lives here rather than in cmd/gluon beside its siblings because two
// drivers emit it — `gluon -e` and the piped loop, which is in this package —
// and internal/repl cannot import a main package. One definition is the whole
// point: an envelope that two drivers spell differently is two envelopes.
// cmd/gluon/json.go names it alongside the others it does own.

// QueryJSON is what `:query -json` answers with.
//
// A new envelope rather than fields grown on metaJSON: that one is frozen, and
// `rows` on it would be add-only in letter and a second shape in practice,
// absent from every other command's answer. This one starts its own add-only
// life, exactly as metaJSON did beside evalJSON.
type QueryJSON struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	// Target is the redacted connection string. Never the resolved DSN, which
	// exists only in the child's environment — invariants 24 and 25.
	Target  string         `json:"target"`
	Columns []QueryColJSON `json:"columns"`
	// Rows are cells in column order. []*string rather than []string so a SQL
	// NULL marshals as null with no custom marshaller, while a cell holding the
	// four letters NULL stays the JSON string "NULL". They are different
	// answers, and telling them apart is most of why this envelope exists.
	Rows [][]*string `json:"rows"`
	// Truncated reports that the fetch cap bound, so a consumer never reads
	// db.MaxRows as the number of rows the statement matched.
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
	Error     string `json:"error,omitempty"`
}

// QueryColJSON is a column's name and the type the database declared for it.
//
// Type is "" when the driver would not say. Every cell arrives as text — the
// child's own scan already decided that — so the declared type is what lets a
// consumer convert deliberately rather than guess from the characters.
type QueryColJSON struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// NewQueryJSON builds the envelope from what Core answered with.
func NewQueryJSON(q *QueryData) QueryJSON {
	out := QueryJSON{
		OK:      q.Err == "",
		Command: ":query",
		Target:  q.Target,
		// Made rather than declared: an empty result set is `[]`, which a
		// consumer can range over, and not `null`, which it has to test for.
		Columns:   make([]QueryColJSON, len(q.Cols)),
		Rows:      make([][]*string, 0, len(q.Rows)),
		Truncated: q.More,
		Note:      q.Note,
		Error:     q.Err,
	}
	for i, name := range q.Cols {
		col := QueryColJSON{Name: name}
		// Types is nil when the driver would not answer ColumnTypes at all, so
		// the length is checked rather than assumed to match Cols.
		if i < len(q.Types) {
			col.Type = q.Types[i]
		}
		out.Columns[i] = col
	}
	for i, row := range q.Rows {
		cells := make([]*string, len(row))
		for j := range row {
			if i < len(q.Null) && j < len(q.Null[i]) && q.Null[i][j] {
				// Left nil, which marshals as null.
				continue
			}
			// The loop variable's address would be the same one every time.
			cell := row[j]
			cells[j] = &cell
		}
		out.Rows = append(out.Rows, cells)
	}
	return out
}

// WriteQueryJSON writes the envelope the way every driver writes it.
//
// One writer rather than an encoder configured at each call site: `gluon -e`
// and the piped loop have to emit the same bytes, and the indentation is part
// of the bytes.
func WriteQueryJSON(w io.Writer, q *QueryData) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(NewQueryJSON(q))
}
