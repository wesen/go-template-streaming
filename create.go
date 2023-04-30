package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

type User struct {
	Email     string
	FirstName string
	LastName  string
	Address   string
	City      string
	Zip       string
}

func createDb(totalUsers int) error {
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

	usersPerBatch := 10000
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

	fmt.Printf("Successfully inserted %d random users into the database.\n", totalUsers)
	return nil
}
