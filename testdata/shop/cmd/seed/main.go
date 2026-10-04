// Command seed writes data/shop.db: the migrations, then the same customers
// and orders every time.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

var names = []string{
	"ada", "grace", "katherine", "margaret", "hedy", "barbara", "frances", "jean",
	"radia", "sophie", "annie", "joan", "mary", "evelyn", "adele", "karen",
	"lynn", "shafi", "donna", "anita", "carol", "susan", "ellen", "fran",
	"betty", "kathleen", "marlyn", "ruth", "dorothy", "ida", "edith", "hilda",
	"emmy", "lise", "chien", "rosalind", "dora", "rozsa", "maryam", "cynthia",
}

func main() {
	path := filepath.Join("data", "shop.db")
	if err := os.MkdirAll("data", 0o755); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		log.Fatal(err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		log.Fatal(err)
	}
	// goose stamps each migration with the time it ran; the fixture's are
	// fixed, so a session that lists them reads the same every time.
	if _, err := db.Exec(`UPDATE goose_db_version SET tstamp = datetime('2026-09-01 09:00:00', '+' || version_id || ' minutes')`); err != nil {
		log.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	start := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 480; i++ {
		name := names[(i-1)%len(names)]
		if n := (i - 1) / len(names); n > 0 {
			name = fmt.Sprintf("%s%d", name, n+1)
		}
		plan := "free"
		switch {
		case i%7 == 0:
			plan = "team"
		case i%3 == 0:
			plan = "pro"
		}
		created := start.Add(time.Duration(i) * 18 * time.Hour)
		if _, err := tx.Exec(`INSERT INTO users (id, email, plan, created) VALUES (?, ?, ?, ?)`,
			i, name+"@example.com", plan, created); err != nil {
			log.Fatal(err)
		}
	}
	statuses := []string{"placed", "paid", "shipped"}
	id := 0
	for u := 1; u <= 40; u++ {
		for k := 0; k < 1+u%3; k++ {
			id++
			cents := 1250 + (u*733+k*411)%9000
			placed := start.Add(time.Duration(u*18+k*30) * time.Hour)
			if _, err := tx.Exec(`INSERT INTO orders (id, user_id, total, status, placed_at) VALUES (?, ?, ?, ?, ?)`,
				id, u, fmt.Sprintf("%d.%02d", cents/100, cents%100), statuses[(u+k)%3], placed); err != nil {
				log.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
}
