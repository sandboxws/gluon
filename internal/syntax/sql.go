package syntax

import "strings"

// sqlTokens scans SQL for display.
//
// This is a hand-written scanner, which the ROADMAP's rejected list is wary of
// — but what it rejected was a hand-rolled YAML scanner used as a *detector*,
// and the reason it gives is the distinction that matters: "a detector that is
// confidently wrong is worse than one that says nothing". That scanner was
// asked a question, and its wrong answer was indistinguishable from a right one
// because an empty environment list looks exactly like a file with no
// environment in it.
//
// This one is asked nothing. Its output is its input with colour, its failures
// are visible in the same glance as its output, and Paint's contract means a
// misread token is a wrong colour and never a wrong byte. There is also no
// alternative to reach for: encoding/json and BurntSushi/toml decode, and a
// decoder cannot say where a token started.
//
// Every branch advances i by at least one and every unterminated construct runs
// to the end, so the loop is bounded by len(src).
func sqlTokens(src string) []Token {
	var toks []Token
	add := func(a, b int, r Role) {
		if b > a {
			toks = append(toks, Token{Start: a, End: b, Role: r})
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				add(i, len(src), RoleComment)
				return toks
			}
			add(i, i+j, RoleComment)
			i += j

		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			// Postgres nests these; the first */ ends it here and the tail is
			// scanned as SQL, which is what it looks like. No byte moves.
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				add(i, len(src), RoleComment)
				return toks
			}
			add(i, i+2+j+2, RoleComment)
			i += 2 + j + 2

		case c == '\'':
			// '' is an escaped quote and does not close. MySQL's backslash
			// escape is not honoured: 'a\'' mis-splits into two literals,
			// cosmetically, and loses nothing.
			j := i + 1
			for j < len(src) {
				if src[j] != '\'' {
					j++
					continue
				}
				if j+1 < len(src) && src[j+1] == '\'' {
					j += 2
					continue
				}
				j++
				break
			}
			add(i, j, RoleString)
			i = j

		case c == '"' || c == '`':
			// A quoted run is painted as a quoted run. "x" is a delimited
			// identifier in Postgres and a string in MySQL, and that reading is
			// the one that is right in both.
			j := strings.IndexByte(src[i+1:], c)
			if j < 0 {
				add(i, len(src), RoleString)
				return toks
			}
			add(i, i+1+j+1, RoleString)
			i += 1 + j + 1

		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := scanNumberRun(src, i)
			add(i, j, RoleNumber)
			i = j

		case c == '$' || c == ':' || c == '@':
			// A bind marker: $1, :name, @name. A bare ':' is punctuation.
			j := i + 1
			for j < len(src) && isWordByte(src[j]) {
				j++
			}
			add(i, j, RolePunct)
			i = j

		case isWordStart(c):
			j := i + 1
			for j < len(src) && (isWordByte(src[j]) || src[j] == '$') {
				j++
			}
			word := strings.ToUpper(src[i:j])
			switch {
			case sqlKeywords[word]:
				add(i, j, RoleKeyword)
			case sqlTypes[word]:
				add(i, j, RoleType)
			case sqlFuncs[word] && nextNonSpace(src, j) == '(':
				add(i, j, RoleBuiltin)
			default:
				add(i, j, RoleIdent)
			}
			i = j

		case strings.IndexByte(",;().*=<>+-/%[]|", c) >= 0:
			n := 1
			if i+1 < len(src) {
				switch src[i : i+2] {
				case "<=", ">=", "<>", "!=", "||", "::":
					n = 2
				}
			}
			add(i, i+n, RolePunct)
			i += n

		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				add(i, i+2, RolePunct)
				i += 2
				break
			}
			i++

		default:
			i++
		}
	}
	return toks
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isWordByte(c byte) bool { return isWordStart(c) || isDigit(c) }

// scanNumberRun consumes a numeric literal loosely: digits, one radix prefix,
// separators, a decimal point and an exponent. Loosely is right — the reader
// wants the number to be one colour, not a lexer's opinion of its validity.
func scanNumberRun(src string, i int) int {
	j := i
	if src[j] == '+' || src[j] == '-' {
		j++
	}
	if j+1 < len(src) && src[j] == '0' && (src[j+1]|0x20 == 'x' || src[j+1]|0x20 == 'o' || src[j+1]|0x20 == 'b') {
		j += 2
		for j < len(src) && (isHex(src[j]) || src[j] == '_') {
			j++
		}
		return j
	}
	seenDot := false
	for j < len(src) {
		c := src[j]
		switch {
		case isDigit(c) || c == '_':
			j++
		case c == '.' && !seenDot:
			seenDot = true
			j++
		case (c|0x20) == 'e' && j+1 < len(src) && (isDigit(src[j+1]) || ((src[j+1] == '+' || src[j+1] == '-') && j+2 < len(src) && isDigit(src[j+2]))):
			j += 2
		default:
			return j
		}
	}
	return j
}

func isHex(c byte) bool {
	return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

func nextNonSpace(src string, i int) byte {
	for ; i < len(src); i++ {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
		default:
			return src[i]
		}
	}
	return 0
}

func words(list string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(list) {
		m[w] = true
	}
	return m
}

var sqlKeywords = words(`
SELECT FROM WHERE JOIN LEFT RIGHT INNER OUTER FULL CROSS LATERAL ON USING
GROUP ORDER BY HAVING LIMIT OFFSET UNION INTERSECT EXCEPT ALL DISTINCT AS
AND OR NOT IN LIKE ILIKE SIMILAR BETWEEN IS NULL CASE WHEN THEN ELSE END
WITH RECURSIVE INSERT INTO VALUES UPDATE SET DELETE TRUNCATE RETURNING
CREATE ALTER DROP RENAME ADD COLUMN TABLE INDEX VIEW MATERIALIZED SEQUENCE
SCHEMA DATABASE TRIGGER FUNCTION PROCEDURE PRIMARY FOREIGN KEY REFERENCES
UNIQUE CHECK DEFAULT CONSTRAINT CASCADE RESTRICT EXPLAIN ANALYZE VACUUM
SHOW DESCRIBE DESC PRAGMA BEGIN COMMIT ROLLBACK SAVEPOINT TRANSACTION
ASC EXISTS ANY SOME CAST OVER PARTITION WINDOW ROW ROWS RANGE FETCH NEXT
FIRST LAST ONLY CONFLICT DO NOTHING IF TEMP TEMPORARY GRANT REVOKE
`)

var sqlTypes = words(`
INT INTEGER BIGINT SMALLINT TINYINT SERIAL BIGSERIAL TEXT VARCHAR CHAR
CHARACTER BOOLEAN BOOL DATE TIME TIMESTAMP TIMESTAMPTZ DATETIME INTERVAL
NUMERIC DECIMAL REAL DOUBLE PRECISION FLOAT JSON JSONB UUID BYTEA BLOB
CLOB ENUM ARRAY MONEY INET CIDR MACADDR XML
`)

var sqlFuncs = words(`
COUNT SUM AVG MIN MAX COALESCE NULLIF GREATEST LEAST NOW CURRENT_TIMESTAMP
LENGTH CHAR_LENGTH LOWER UPPER TRIM LTRIM RTRIM SUBSTRING SUBSTR REPLACE
CONCAT CONCAT_WS ROUND FLOOR CEIL CEILING ABS MOD POWER SQRT RANDOM
ARRAY_AGG STRING_AGG JSON_AGG JSONB_AGG TO_CHAR TO_DATE TO_TIMESTAMP
DATE_TRUNC DATE_PART EXTRACT ROW_NUMBER RANK DENSE_RANK NTILE LAG LEAD
FIRST_VALUE LAST_VALUE GENERATE_SERIES UNNEST
`)
