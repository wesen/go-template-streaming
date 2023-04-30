# Rendering a template in go: a performance optimization tale

Points:

## Introduction

I have been building web functionality of [sqleton](https://github.com/go-go-golems/sqleton)
this week. Sqleton is an application that allows you to define CLI commands in a [YAML file](https://github.com/go-go-golems/sqleton/blob/main/cmd/sqleton/queries/wp/ls-posts.yaml)
to render a SQL template and fire it off against a database.

It is now possible to [serve these commands](https://github.com/go-go-golems/sqleton/blob/main/cmd/sqleton/cmds/serve.go) as little webpages:
the CLI flags are rendered as a HTML form, and the resulting structured data (which is served 
by the [glazed](https://github.com/go-go-golems/glazed) library) is rendered as a HTML table.
To render this form, I of course use a go template, as go templating is a central concept used in 
the [go-go-golems ecosystem](https://github.com/go-go-golems). 
You can find the template [here](https://github.com/go-go-golems/sqleton/blob/main/cmd/sqleton/cmds/templates/data-tables.tmpl.html).

A first version simply used the [go-pretty](https://github.com/jedib0t/go-pretty) library to render the table as HTML,
passing it off as a string to the template. This was all fine and good until I rendered a table with 300000 rows,
which would lead chrome (or any browser for that matter) to crash. Seeing how I am using the [jquery datatables library](https://datatables.net/)
to render the table, I figured I could just render the data as JSON and embed it into the javascript of the HTML template
(I wanted to avoid AJAX API calls for now, as it would require decoupling rendering from the actual database query).

This worked well on the browser side (leaving aside the elegance of rendering 300k rows of JSON into a `script` tag).
Off we go I decided, and deployed the application to a [256 vCPU and 512 MB memory task](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html#task_size) on [AWS ECS](https://aws.amazon.com/ecs/).
Unsurprisingly in hindsight, that container did not fare very well under load, getting killed because it exceeded its memory limit.

This article is a little story of memory optimization and some of the tricks I've learned over the years to make go programs
(or any kind of programs, for that matter) run with constrained memory.

## Premature optimization is the source of all great optimizations

Forgive me the clickbait subtitle, but I do think there is a lot of value in thinking about performance early
on in the development process. **Thinking is cheap! And who doesn't like cheap things?**

The conventional wisdom starts with the often stated maxim that you should get your program to work first, and optimize it later,
because optimization is expensive, slows down the development process, leads to convoluted code, is often unnecessary, and most importantly,
is evil (who doesn't like a strongly worded value judgment here and there?). Conventional wisdom then prescribes using a combination
of profiling and benchmarking to find the bottlenecks in your program, optimizing these bottlenecks away, repeating the process
and patting yourself on the back for having been so Good (not Evil).

This is exactly how we are going to start, before taking a step back to actually prematurely optimize, and see where this gets us.

## Our toy example: rendering a database table as markdown

In this toy example, created specially for this article, we are going to render 1M rows of user data into a markdown table.

### Creating and populating the database

First, we need to create and populate our database, which is done in [create.go](https://github.com/wesen/go-template-streaming/blob/main/create.go).

We use sqlite3 as an easy way to create our on disk database "users.db". We then use a very elegant string-based batch
insert to insert 10000 users at once.

```go
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
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
```

You can run this program by running `go run . create`, which will takes its sweet time, but this is not the part we want
to optimize (at least not prematurely).

```
go-template-streaming on  main via 🐹 v1.20.3 
❯ go run . create
Inserted 0/1000000 users
Inserted 10000/1000000 users
Inserted 20000/1000000 users
Inserted 30000/1000000 users
...
Inserted 990000/1000000 users
Successfully inserted 1000000 random users into the database.

go-template-streaming on  main via 🐹 v1.20.3 took 1m7s 
❯ ls -lah users.db
-rw-r--r-- 1 manuel manuel 72M Apr 30 18:40 users.db
```

Once this has finished running, we end up with a 72 MB database file.

### Rendering the database as markdown (naively, but not evil)

Our first attempt at rendering will be quite simple:
- we read the table into memory, as an array of `User` structs
- we pass this array to a template, which renders the markdown table by using a `range` construct.

First, the [template](https://github.com/wesen/go-template-streaming/blob/main/naive.go#L13):

```go

const markdownTemplate = `
| Email | First Name | Last Name | Address | City | Zip |
|-------|------------|-----------|---------|------|-----|
{{range .}}
| {{.Email}} | {{.FirstName}} | {{.LastName}} | {{.Address}} | {{.City}} | {{.Zip}} |{{end}}
```

Second, the rendering code:

```go
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
```

We were already very clever to stream the template straight to os.Stdout (does this count as premature?), but it is clear
from the structure of the code (accumulating all rows into an array) that we are going to use O(n) memory here. 

We can run this program by running `go run . generate`:

```markdown

| Email | First Name | Last Name | Address | City | Zip |
|-------|------------|-----------|---------|------|-----|

| 6M5G0xdYtS@example.com | p1keu | O8Y37ew | M5nv8 cTs4DixwIM | hZMpGx | LuIYM |
| tVBZMyaC3g@example.com | Ux7N8 | sm6iRYU | HzbHn fV09wPxRug | 9d6j8x | mnXKt |
| KX5UV0eeR5@example.com | jT1D7 | gCvyC00 | syrw0 Yqb9lgQcL7 | 3ueWIF | QEyfV |
| 0m4lcQ16ut@example.com | QBkyA | 2awv7pN | rvXJk BxX5aJQvoF | lRZsHG | U4ihz |
| 1QSdZLNAmN@example.com | T1CCd | Bnk9LiG | 1wY7l 2U20Jfsxle | mfnac7 | RZA79 |
| vCaw5qrJZv@example.com | 3x6Hn | lKFgPZj | ktV3T 7EEdBgPFp8 | 5hXjB6 | tFzor |
| 6e6ImBw6qk@example.com | qCuIL | hssRqXq | qYrQ3 hKQUcsRULm | 2MxUqL | 42JK7 |
...

```

### Profiling our naive solution

In order to measure the memory consumption, we are going to apply two strategies.
First, we call the function [writeProfile](https://github.com/wesen/go-template-streaming/blob/main/utils.go#L48)
which generates a [pprof file](https://go.dev/blog/pprof) that can be analyzed with `go tool`. We use `pprof.WriteHeapProfile`
which gives us an account of how much memory each function in our program has allocated.

```go
func writeProfile(filepath string) error {
	// write mem profile
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	err = pprof.WriteHeapProfile(f)
	if err != nil {
		return err
	}

	log.Printf(`
	To view the memory profile, run the following command:
	go tool pprof %s
	go tool pprof -alloc_space %s
`, filepath, filepath)
	return nil
}
```

As told, we execute `go tool pprof -alloc_space mem.prof` command, and then use `top` to see the top memory consumers:

```
go-template-streaming on  main via 🐹 v1.20.3 
❯ go tool pprof -alloc_space mem.prof
File: go-template-streaming
Build ID: 8b897351543dfccf4446628bf449e314479a4929
Type: alloc_space
Time: Apr 30, 2023 at 6:48pm (EDT)
Entering interactive mode (type "help" for commands, "o" for options)
(pprof) top
Showing nodes accounting for 843.12MB, 99.94% of 843.65MB total
Dropped 9 nodes (cum <= 4.22MB)
Showing top 10 nodes out of 18
      flat  flat%   sum%        cum   cum%
  590.11MB 69.95% 69.95%   843.12MB 99.94%  main.generateMarkdown
   78.50MB  9.30% 79.25%      253MB 29.99%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked
   60.50MB  7.17% 86.42%    60.50MB  7.17%  github.com/mattn/go-sqlite3._Cfunc_GoStringN (inline)
   41.50MB  4.92% 91.34%    41.50MB  4.92%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func10
   33.50MB  3.97% 95.31%    33.50MB  3.97%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func3
   33.50MB  3.97% 99.29%    33.50MB  3.97%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func9
    5.50MB  0.65% 99.94%     5.50MB  0.65%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func1
         0     0% 99.94%      253MB 29.99%  database/sql.(*Rows).Next
         0     0% 99.94%      253MB 29.99%  database/sql.(*Rows).Next.func1
         0     0% 99.94%      253MB 29.99%  database/sql.(*Rows).nextLocked
(pprof) %                                                                                                                                           
```

This tells us that the `generateMarkdown` function allocated 590 MB of memory on its, and that cumulatively (meaning, 
including the memory allocated by the functions it called), allocated 843 MB of memory. The next cluprit is
`nextSyncLocked` which allocated 78.5 MB of memory. These values represent the amount of memory allocated over the duration
of the program, they don't imply that the program was using 843 MB of memory at any given time. 

In order to measure the maximum size of the heap, we can use the function `runtime.ReadMemStats` which gives us the `HeapAlloc`
variable. In order to measure the maximum amount reached over the entire duration, we use a little goroutine polling the 




### Start from the hardware

I like to start with the opposite approach. My background is in embedded, where performance only matters in as far as you need to ensure
realtime constraints and memory limits are met. This is not something you can do with iterative benchmarking after the fact:
if you are too slow or eat too much memory, there is no after the fact because your program simply doesn't get to run.

What I do instead is ponder: given the hardware that is given to me, what is the fastest possible way to get the job done.
Most of my programming being in backend and embedded programming, the job usually consists of retrieving some bytes from A
and shoveling them over to B. In the concrete case we are dealing with here, we need to read some bytes from storage (our database)
and send them to the network interface. Even if we were a magical genie capable of executing NP-complete algorithms in constant time,
there is no way we can rise above these physical limitations.

### No software fastest software
What this initial consideration allows us to do is figure out the tradeoffs we are making as we start building our program. Imagine
for a minute that we actually need to hit that physical limit: how would we do so? The fastest program is no program at all, 
and in fact a magical piece of machinery would allow us to simply connect the database to the network interface: DMA. This however
would require the data in storage to be in a format appropriate for the DMA controller to transfer to the network interface as is.
This is certainly a challenge we could take up in the embedded space. Not here, as we need to not only speak HTTP, we also need to send our
data as JSON; two tasks which are probably not within the capabilities of off the shelf DMA controllers.

### Bypassing the kernel and the network stack

We can continue down this line of thinking, and figure out if we can stream network traffic from central memory, which would allow us
to shape the data a bit more easily. This is an active area of development (and way out of my depth), but the keywords to look for
are "zero-copy networking", "kernel bypass" and for example io_uring, which allow userland programs to stream data straight to the
network card. This would require our memory to be available in memory (which could very well already be the case by the simple virtue of
the operating system providing a file system memory cache) and to be in the right format for efficient streaming.

This consideration however surfaces the two techniques we are going to use to optimize our program: memory-bounded streaming
and preparing data to be in the right format. Five minutes of thinking.






- create a db with users
- render template naively
- measuring the allocations
- measuring the top heap size
- stream user structures to the template
- have the DB do the string concatenation
- measuring the database performance
- measuring the allocations