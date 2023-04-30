package main

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"text/template"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
)

type User struct {
	Email     string
	FirstName string
	LastName  string
	Address   string
	City      string
	Zip       string
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

	result := make([]byte, length)
	for i := range result {
		result[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(result)
}

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

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Email, &user.FirstName, &user.LastName, &user.Address, &user.City, &user.Zip); err != nil {
			return err
		}
		users = append(users, user)
	}

	tmpl, err := template.New("markdown").Parse(markdownTemplate)
	if err != nil {
		return err
	}

	err = tmpl.Execute(os.Stdout, users)

	return nil
}

func createDb() error {
	// delete users.db
	if _, err := os.Stat("users.db"); err == nil {
		err := os.Remove("users.db")
		if err != nil {
			return err
		}
	}

	db, err := sql.Open("sqlite3", "users.db")
	if err != nil {
		return err
	}
	defer db.Close()

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT NOT NULL,
		first_name TEXT NOT NULL,
		last_name TEXT NOT NULL,
		address TEXT NOT NULL,
		city TEXT NOT NULL,
		zip TEXT NOT NULL
	);
	`
	_, err = db.Exec(createTableQuery)
	if err != nil {
		return err
	}

	insertUserQuery := `
	INSERT INTO users (email, first_name, last_name, address, city, zip)
	VALUES %s
	`

	totalUsers := 100000
	usersPerBatch := 1000
	rowsToInsert := make([]string, usersPerBatch)

	for i := 0; i < totalUsers; i++ {
		user := User{
			Email:     fmt.Sprintf("%s@example.com", randomString(10)),
			FirstName: randomString(5),
			LastName:  randomString(7),
			Address:   fmt.Sprintf("%s %s", randomString(5), randomString(10)),
			City:      randomString(6),
			Zip:       randomString(5),
		}

		rowsToInsert[i%usersPerBatch] = fmt.Sprintf("('%s', '%s', '%s', '%s', '%s', '%s')", user.Email, user.FirstName, user.LastName, user.Address, user.City, user.Zip)

		if i%usersPerBatch == 0 {
			_, err = db.Exec(fmt.Sprintf(insertUserQuery, strings.Join(rowsToInsert, ",")))
			fmt.Printf("Inserted %d/%d users\n", i, totalUsers)
		}
	}

	fmt.Println("Successfully inserted 1M random users into the database.")
	return nil
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "go-template-streaming",
		Short: "Simple demonstration of streaming from a DB into a template",
	}

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a test database",
		Run: func(cmd *cobra.Command, args []string) {
			err := createDb()
			cobra.CheckErr(err)
		},
	}

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a markdown table from the test database",
		Run: func(cmd *cobra.Command, args []string) {
			err := generateMarkdown()
			cobra.CheckErr(err)
		},
	}

	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(generateCmd)

	err := rootCmd.Execute()
	cobra.CheckErr(err)
}
