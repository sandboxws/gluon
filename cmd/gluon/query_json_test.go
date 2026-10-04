package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sandboxws/gluon/internal/db"
	"github.com/sandboxws/gluon/internal/repl"
)

// goldenQuery is a result set with every distinction the envelope exists to
// carry: a declared type, a driver that would not declare one, a cell holding
// the four letters NULL, a cell that was SQL NULL, the fetch cap binding, and a
// note.
func goldenQuery() *repl.QueryData {
	return &repl.QueryData{
		Result: db.Result{
			Cols:  []string{"id", "label", "seen"},
			Types: []string{"INTEGER", "TEXT", ""},
			Rows:  [][]string{{"1", "NULL", "2026-09-01T00:00:00Z"}, {"2", "NULL", "NULL"}},
			Null:  [][]bool{{false, false, false}, {false, true, true}},
			More:  true,
			Note:  "this driver has no read-only transaction; the statement ran outside one",
		},
		Target: "postgres://app@db.internal:5432/acme",
	}
}

const goldenQueryJSON = `{
  "ok": true,
  "command": ":query",
  "target": "postgres://app@db.internal:5432/acme",
  "columns": [
    {
      "name": "id",
      "type": "INTEGER"
    },
    {
      "name": "label",
      "type": "TEXT"
    },
    {
      "name": "seen",
      "type": ""
    }
  ],
  "rows": [
    [
      "1",
      "NULL",
      "2026-09-01T00:00:00Z"
    ],
    [
      "2",
      null,
      null
    ]
  ],
  "truncated": true,
  "note": "this driver has no read-only transaction; the statement ran outside one"
}
`

// TestQueryJSONIsPinned.
//
// The envelope is a contract, so it is pinned rather than described: a field
// may be added and the golden updated to match, and nothing else can change
// without this failing first. The two rows say the thing metaJSON's `text`
// could not — cell [0][1] is the string "NULL" and cell [1][1] is JSON null,
// and they mean different things.
func TestQueryJSONIsPinned(t *testing.T) {
	var buf bytes.Buffer
	if err := repl.WriteQueryJSON(&buf, goldenQuery()); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != goldenQueryJSON {
		t.Errorf("the envelope moved.\n got:\n%s\nwant:\n%s", got, goldenQueryJSON)
	}
}

// TestBothDriversEmitTheSameEnvelope.
//
// `gluon -e` marshals through emit and the piped loop through WriteQueryJSON,
// on opposite sides of an import boundary. Same struct, same indentation, same
// bytes — or the flag means something slightly different depending on how the
// statement was fed in, which is the failure a script would find last.
func TestBothDriversEmitTheSameEnvelope(t *testing.T) {
	var piped bytes.Buffer
	if err := repl.WriteQueryJSON(&piped, goldenQuery()); err != nil {
		t.Fatal(err)
	}
	// What emit encodes, which is the same encoder with the same indent.
	oneShot, err := json.MarshalIndent(queryJSON(repl.NewQueryJSON(goldenQuery())), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Encode appends the newline MarshalIndent does not.
	if got, want := string(oneShot)+"\n", piped.String(); got != want {
		t.Errorf("the two drivers disagree.\n -e:\n%s\npipe:\n%s", got, want)
	}
}

// TestAFailedStatementIsAnEnvelopeWithNoRows.
//
// The flag chose the shape of the answer, and a consumer should not have to
// decode a second envelope to find out the statement did not run.
func TestAFailedStatementIsAnEnvelopeWithNoRows(t *testing.T) {
	q := &repl.QueryData{
		Result: db.Result{Err: `relation "userz" does not exist`},
		Target: "postgres://app@db.internal:5432/acme",
	}
	var buf bytes.Buffer
	if err := repl.WriteQueryJSON(&buf, q); err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK      bool            `json:"ok"`
		Rows    [][]*string     `json:"rows"`
		Columns json.RawMessage `json:"columns"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("the failure envelope does not decode: %v\n%s", err, buf.String())
	}
	if got.OK {
		t.Error("a rejected statement reported ok")
	}
	if len(got.Rows) != 0 {
		t.Errorf("a failure carries rows: %v", got.Rows)
	}
	if got.Error != `relation "userz" does not exist` {
		t.Errorf("error = %q", got.Error)
	}
	// Empty rather than absent: a consumer ranges over them either way.
	if string(got.Columns) != "[]" {
		t.Errorf("columns = %s, want []", got.Columns)
	}
}
