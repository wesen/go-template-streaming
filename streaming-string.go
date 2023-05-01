package main

import (
	"context"
	"database/sql"
	"golang.org/x/sync/errgroup"
	"log"
	"os"
	"text/template"

	_ "github.com/mattn/go-sqlite3"
)

const markdownStringTemplate = `
| Email | First Name | Last Name | Address | City | Zip |
|-------|------------|-----------|---------|------|-----|
{{range .}}|{{.}}|{{end}}
`

func generateStreamingStringMarkdown() error {
	eg, ctx := errgroup.WithContext(context.Background())

	c := make(chan string)

	eg.Go(func() error {
		defer close(c)

		db, err := sql.Open("sqlite3", "users.db")
		if err != nil {
			return err
		}
		defer db.Close()
		rows, err := db.QueryContext(ctx, `SELECT (email || '|' || first_name || '|' || last_name || '|' || address || '|' || city || '|' || zip) FROM users`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			c <- s
		}

		return nil
	})

	eg.Go(func() error {
		tmpl, err := template.New("markdown").Parse(markdownStringTemplate)
		if err != nil {
			return err
		}

		err = tmpl.Execute(os.Stdout, c)
		log.Println("Successfully generated markdown table.")

		return err
	})

	err := eg.Wait()
	if err != nil {
		return err
	}

	err = writeProfile("mem-streaming.prof")
	if err != nil {
		return err
	}

	return nil
}
