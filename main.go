package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
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

func createDb() error {
	db, err := sql.Open("sqlite3", "users.db")
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}

	insertUserQuery := `
	INSERT INTO users (email, first_name, last_name, address, city, zip)
	VALUES (?, ?, ?, ?, ?, ?);
	`
	stmt, err := db.Prepare(insertUserQuery)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()

	totalUsers := 1000000
	for i := 0; i < totalUsers; i++ {
		user := User{
			Email:     fmt.Sprintf("%s@example.com", randomString(10)),
			FirstName: randomString(5),
			LastName:  randomString(7),
			Address:   fmt.Sprintf("%s %s", randomString(5), randomString(10)),
			City:      randomString(6),
			Zip:       randomString(5),
		}

		_, err = stmt.Exec(user.Email, user.FirstName, user.LastName, user.Address, user.City, user.Zip)
		if err != nil {
			log.Fatal(err)
		}

		if i%100000 == 0 {
			fmt.Printf("Inserted %d users\n", i)
		}
	}

	fmt.Println("Successfully inserted 1M random users into the database.")
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "go-template-streamin",
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

	rootCmd.AddCommand(createCmd)

	err := rootCmd.Execute()
	cobra.CheckErr(err)
}
