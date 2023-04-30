package main

import (
	"database/sql"
	"log"
	"os"
	"text/template"
	"unsafe"

	_ "github.com/mattn/go-sqlite3"
)

const markdownTemplate = `
| Email | First Name | Last Name | Address | City | Zip |
|-------|------------|-----------|---------|------|-----|
{{range .}}
| {{.Email}} | {{.FirstName}} | {{.LastName}} | {{.Address}} | {{.City}} | {{.Zip}} |{{end}}
`

func generateMarkdown() error {
	db, err := sql.Open("sqlite3", "users.db")
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT email, first_name, last_name, address, city, zip FROM users`)
	if err != nil {
		return err
	}
	defer rows.Close()

	totalSize := 0
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Email, &user.FirstName, &user.LastName, &user.Address, &user.City, &user.Zip); err != nil {
			return err
		}
		totalSize += int(unsafe.Sizeof(user))
		users = append(users, user)
	}

	totalSize += int(unsafe.Sizeof(users))
	log.Printf("Size of users: %d bytes\n", totalSize)

	tmpl, err := template.New("markdown").Parse(markdownTemplate)
	if err != nil {
		return err
	}

	err = tmpl.Execute(os.Stdout, users)
	log.Println("Successfully generated markdown table.")

	err = writeProfile("mem.prof")
	if err != nil {
		return err
	}

	return nil
}
