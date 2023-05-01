# Rendering a template in go: a performance optimization tale

Points:

## Introduction

I have been building out the web functionality of [sqleton](https://github.com/go-go-golems/sqleton)
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
to render the table, I figured I could just render the data as JSON and embed it into the javascript of the HTML template to
avoid having the DOM renderer crash. I want to avoid AJAX API calls for now, as it would require decoupling rendering from the actual database query.

This worked quite well on the browser side (leaving aside the elegance of rendering 300k rows of JSON into a `script` tag)!

"Off we go!" I thought, and deployed the application to a [256 vCPU and 512 MB memory task](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definition_parameters.html#task_size) on [AWS ECS](https://aws.amazon.com/ecs/).
Unsurprisingly (in hindsight), that container did not fare very well under load, getting killed because it exceeded its memory limit as soon as someone removed the pagination limit.

This article tells the story of optimizing the memory consumption of that template rendering and recounts some of the
tricks I've learned over the years to make go programs (or any kind of programs, for that matter) run with constrained
memory.

The entire sourcecode of the toy example can be found at [github.com/wesen/go-template-streaming](https://github.com/wesen/go-template-streaming).

## Premature optimization is the source of all great optimizations

Forgive me the clickbait subtitle, but I do think there is a lot of value in thinking about performance early
on in the development process. **Thinking is cheap! And who doesn't like cheap things?**

The conventional wisdom starts with the often stated maxim that you should get your program to work first, and optimize it later,
because optimization is expensive, slows down the development process, leads to convoluted code, is often unnecessary, and most importantly,
is evil (who doesn't like a strongly worded value judgment here and there?). Conventional wisdom instead prescribes getting a
version (any version?) of your program to run first and then using a combination
of profiling and benchmarking to find the bottlenecks, optimizing these bottlenecks away, repeating the process
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

Second, the rendering code, which does nothing very special at all:
- open the users.db file
- query the users table
- iterate over the rows, reading in the value into a `User` struct and appending it to a list
- rendering the template to stdout by passing the list of users to it

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

We were already very clever to stream the template straight to os.Stdout (does this count as premature optimization?),
but it is clear from the structure of the code (accumulating all rows into an array)
that we are going to use O(n) memory here. 

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

### pprof educational interlude

`pprof` is the name of go profiling subsystem. It can be used to collect information about CPU usage, memory allocation,
thread creation and lock contention. The subsystem collects its profiling data by sampling (periodically collecting)
the information it needs. The [pprof documentation](https://pkg.go.dev/runtime/pprof#Profile),
which gives more details: it can collect stack traces of running goroutines, memory allocation of live objects,
past allocations, thread creations, etc...

The information that interests us is the Heap profile:

> The heap profile reports statistics as of the most recently completed garbage collection; it elides more recent
> allocation to avoid skewing the profile away from live data and toward garbage. If there has been no garbage collection
> at all, the heap profile reports all known allocations. This exception helps mainly in programs running without garbage
> collection enabled, usually for debugging purposes.
>
> The heap profile tracks both the allocation sites for all live objects in the application memory and for all objects
> allocated since the program start. Pprof's -inuse_space, -inuse_objects, -alloc_space, and -alloc_objects flags select
> which to display, defaulting to -inuse_space (live objects, scaled by size).

While the `pprof` package can make its sampling data accessible on [request over HTTP](https://pkg.go.dev/net/http/pprof),
we just write it out to disk at the end of our program. While CPU profiling needs to be explicitly enabled, 
heap profiling is always on: it is triggered to run every `MemProfileRate`
bytes, which is defaulting to every 512 kB. The `MemProfileRate` value can be configured at runtime or by setting the 
[GODEBUG](https://pkg.go.dev/runtime@master) environment variable to `memprofilerate=XX`.

### Looking at the heap profile of our naive template render

As told by our program once it finishes running, we execute `go tool pprof -alloc_space mem.prof` command 
(`alloc_space` tracks the number of bytes allocated since program start), and then use `top` to see the top memory consumers:

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

### Measuring the max heap size

In order to measure the maximum size of the heap, we can use the function `runtime.ReadMemStats` which gives us the `HeapAlloc`
variable. In order to measure the maximum amount reached over the entire duration, we use a little goroutine polling the 
`HeapAlloc` and keeping its high watermark:

```go
func monitorHeapSize(ctx context.Context, withTriggerGC bool) {
var mem runtime.MemStats

	maxAlloc := uint64(0)

	t := time.NewTicker(200 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// trigger gc
			if withTriggerGC {
				runtime.GC()
			}
			runtime.ReadMemStats(&mem)
			if mem.HeapAlloc > maxAlloc {
				maxAlloc = mem.HeapAlloc
				memSize := convertHumanReadable(maxAlloc)
				fmt.Fprintf(os.Stderr, "MaxAlloc: %s\n", memSize)
			}
		}
	}
}
```

You'll note two things here:
- we use a 200 ms time interval, which is by no means perfect. We should be aware of potential aliasing issues here.
- we pass in a flag to trigger [garbage collection](https://publish.obsidian.md/manuel/Wiki/Programming/Garbage+Collection).
    This is because the Go runtime is lazy about [garbage collection](https://tip.golang.org/doc/gc-guide),
    and we want to make sure we are measuring the maximum
    heap size, not the current heap size. Both values are important to know about. Churning through lots of temporary 
    allocations can be a source of performance issues, even if the maximum heap size is low. 

Running generate again, we can now see the maximum heap size reached, first without triggering the GC:

``` 
❯ go run . generate  > /dev/null
MaxAlloc: 22.7 MiB
MaxAlloc: 45.7 MiB
MaxAlloc: 65.5 MiB
MaxAlloc: 131.9 MiB
MaxAlloc: 164.9 MiB
MaxAlloc: 198.5 MiB
2023/05/01 07:34:47 Size of users: 95040024 bytes
MaxAlloc: 232.6 MiB
...
MaxAlloc: 329.5 MiB
MaxAlloc: 335.9 MiB
MaxAlloc: 342.8 MiB
MaxAlloc: 349.6 MiB
MaxAlloc: 356.4 MiB
MaxAlloc: 363.3 MiB
MaxAlloc: 370.1 MiB
2023/05/01 07:34:51 Successfully generated markdown table.
2023/05/01 07:34:51 Time elapsed: 6.360070157s
```

And with triggering the GC every 200 ms. As you can see, with the GC triggering, the second half of the program
(streaming the template out to stdout) doesn't allocate any more memory than it did before. This makes sense,
because all the data needed to render the template is now contained in the Users array, and all allocations happening
would (hopefully!) be small allocations inside the rendering engine.

```
❯ go run . generate --trigger-gc > /dev/null
MaxAlloc: 18.6 MiB
MaxAlloc: 42.0 MiB
MaxAlloc: 64.9 MiB
MaxAlloc: 81.2 MiB
MaxAlloc: 99.7 MiB
MaxAlloc: 108.9 MiB
MaxAlloc: 134.0 MiB
MaxAlloc: 160.6 MiB
MaxAlloc: 169.5 MiB
MaxAlloc: 202.0 MiB
2023/05/01 07:33:51 Size of users: 95040024 bytes
2023/05/01 07:33:55 Successfully generated markdown table.
```

## Start from the hardware

While the conventional approach, now that we have a slow bulky solution, would be to use the profiles we have and 
start optimizing, I like to start with the opposite approach.

My background is in embedded, where performance only matters in as far as you need to ensure
realtime constraints and memory limits are met. This is not something you can do with iterative benchmarking after the fact:
if you are too slow or eat too much memory, there is no after the fact because your program simply doesn't get to run.

What I do instead is stare at my orb and ponder (or, more often, go for a walk): 

> given the hardware that is given to me, what is the fastest possible way to get the job done?

(Hardware in this context means the constraints that I absolutely cannot change. In our case, these constraints are
more likely to be the database storage engine, but the principle is the same).

Since most of my programming has been in backend and embedded programming, my programs usually consists of retrieving some bytes from A
and shoveling them over to B. In our "markdown users rendering" situation, we need to read some bytes from storage (our database)
and send them to the standard output (or, in real life, to a network interface over HTTP, maybe in the JSON format). 

**Even if we were a magical genie capable of executing NP-complete algorithms in constant time,
there is no way we can rise above these physical limitations.**

### No software fastest software

Approaching the problem from the hardware up allows us to do be deliberate about the tradeoffs
we are making as we start building our program.

Imagine that we actually need to hit that physical limit of sending bytes directly from storage to our network interface. 
How would we do so? The fastest program is no program at all, and in fact a magical piece of machinery could do the job for us: the DMA controller.
This would however require the data in storage to be in a format appropriate for the DMA controller to transfer to the network interface as is.
This is certainly a challenge we could take up in the embedded space. Not here, as we need to not only speak HTTP,
we also need to send our data as JSON (or markdown, in today's case); 
two tasks which are probably not within the capabilities of off-the-shelf DMA controllers.

If you think this is all lunacy, look at the architectures developed by high-frequency-trading companies.

### Bypassing the kernel and the network stack

We can continue down this line of thinking and take the next best step:
how can we get our data from storage into central memory, transform it, and stream it back out straight to the network interface?
This would allow us to shape the data a bit more easily, and it is the approach we are going to take today.

Sending data straight from a user-space program to a network interface is an active area of development (and way out of my depth). 
The keywords to look for are "zero-copy networking", "kernel bypass" and for example io_uring, 
which allow linux userland programs to stream data straight to different IO subsystems in the kernel.
This would also require our memory to be in the right format for efficient streaming.

Reading data from storage into main memory is something the kernel already does very well, but for best performance we need
to be aware of cache architecture and access patterns. On the userland side, calls like `mmap()` exist to give us access to
the kernel's file system cache. In our case, we use the `mattn/go-sqlite3` package, which is a wrapper around the C sqlite3 library.
This doesn't give us direct control over storage access, and we will have to rely on the C library to do the right thing (see however
the note at the end of this article).

Thinking about the best to solve our problem if we had access to the kernel however surfaces the two techniques
we are going to use to optimize our program: memory-bounded streaming and preparing data to be in the right format.

We are going to:
- create a streaming channel that can be handed off to the rendering engine (similar to how we would set up a DMA channel and hand it off to the DMA controller)
- read data in chunks, trusting the lower layer to efficiently handle reading from storage
- transform each chunk into a format that is easy to stream
- hand the chunk off to the streaming channel

## Optimization 1: memory-bounded streaming

We've seen that the biggest memory hog in our program is the `Users` array. This is because we are reading all the data
from storage into memory up front, then passing the entire data to the templating engine. Reading from storage
is entirely decoupled from the template rendering. This is nice from a separation of concerns point of view (we could
easily replace the database with another one, or rendering things as HTML or JSON or whatever, without having to 
change the other part), but it is not very efficient.

Wouldn't it be great if we could instead do something like our DMA controller, and have the data go straight from the DB
to the template engine? But how can we do so? It turns out that the [go template rendering engine](https://pkg.go.dev/text/template) 
can accept a channel as iterator:

> {{range pipeline}} T1 {{end}}
> 
> The value of the pipeline must be an array, slice, map, or channel.
> If the value of the pipeline has length zero, nothing is output;
> otherwise, dot is set to the successive elements of the array,
> slice, or map and T1 is executed. If the value is a map and the
> keys are of basic type with a defined order, the elements will be
> visited in sorted key order.

This means that instead of passing the template engine a slice of users, we can pass it a channel of users. This should
help us keep the maximum of allocated memory bounded to the size of a single `User` slice. The downside is that if we
encounter an error while rendering, we won't be able to suppress the data that has already been sent and instead render
a nice error message. We'll have to abort mid-way. A more beneficial side-effect is that the rendering now happens in 
parallel with reading from the database, which should help improve runtime as well.

The new version uses the exact same template, but uses a go routine to read from the database and send the data to the
channel shared with the template rendering engine. I use the `errgroup` package here to run the goroutines and wait on
their exit, because [go concurrency is not easy](https://publish.obsidian.md/manuel/ZK/Claims/2+-+Software/2g+-+Code/ZK+-+2g3a+-+golang+Concurrency+is+not+easy).

```go
func generateStreamingMarkdown() error {
    eg, ctx := errgroup.WithContext(context.Background())

	c := make(chan User)

	eg.Go(func() error {
		defer close(c)

		db, err := sql.Open("sqlite3", "users.db")
		if err != nil {
			return err
		}
		defer db.Close()
		rows, err := db.QueryContext(ctx, `SELECT email, first_name, last_name, address, city, zip FROM users`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user User
			if err := rows.Scan(&user.Email, &user.FirstName, &user.LastName, &user.Address, &user.City, &user.Zip); err != nil {
				return err
			}
			c <- user
		}

		return nil
	})

	eg.Go(func() error {
		tmpl, err := template.New("markdown").Parse(markdownTemplate)
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
```

Running the measurements we did before, we now see that without GC, we are capping out at 3.6 MiB of memory, which is
quite a change! 

```
❯ go run . generate --streaming > /dev/null
MaxAlloc: 2.0 MiB
MaxAlloc: 3.1 MiB
MaxAlloc: 3.6 MiB
2023/05/01 08:21:35 Successfully generated markdown table.
2023/05/01 08:21:35 Time elapsed: 7.622725915s
```

Looking at the profile information, we see that our generateStreamingMarkdown method is now at the same level
as `Next` (from the database driver) and `reflect.Value.recv` (use by the template rendering engine to interpret
our `User` struct):

```
❯ go tool pprof -alloc_space mem-streaming.prof
File: go-template-streaming
Build ID: cd0c79b1712051bd8ef4669842e0c2356efd86ed
Type: alloc_space
Time: May 1, 2023 at 8:21am (EDT)
Entering interactive mode (type "help" for commands, "o" for options)
(pprof) top
Showing nodes accounting for 698.53MB, 98.94% of 706.03MB total
Dropped 6 nodes (cum <= 3.53MB)
Showing top 10 nodes out of 30
      flat  flat%   sum%        cum   cum%
  121.51MB 17.21% 17.21%   121.51MB 17.21%  github.com/mattn/go-sqlite3.(*SQLiteRows).Next
  104.51MB 14.80% 32.01%   104.51MB 14.80%  reflect.Value.recv
  102.01MB 14.45% 46.46%   223.52MB 31.66%  main.generateStreamingMarkdown.func1
     100MB 14.16% 60.62%   322.51MB 45.68%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked
      73MB 10.34% 70.96%       73MB 10.34%  github.com/mattn/go-sqlite3._Cfunc_GoStringN (inline)
   50.50MB  7.15% 78.12%    50.50MB  7.15%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func9
   49.50MB  7.01% 85.13%    49.50MB  7.01%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func3
      46MB  6.52% 91.64%       46MB  6.52%  reflect.(*structType).Field
   42.50MB  6.02% 97.66%    42.50MB  6.02%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func10
       9MB  1.27% 98.94%   160.01MB 22.66%  text/template.(*state).walkRange
```

Out of pure curiosity, let's trigger the GC before measuring max heap size:

``` 
❯ go run . generate --streaming --trigger-gc > /dev/null
MaxAlloc: 222.0 KiB
MaxAlloc: 228.6 KiB
MaxAlloc: 232.5 KiB
MaxAlloc: 233.4 KiB
MaxAlloc: 235.1 KiB
MaxAlloc: 237.3 KiB
MaxAlloc: 240.6 KiB
MaxAlloc: 250.8 KiB
MaxAlloc: 254.3 KiB
MaxAlloc: 255.3 KiB
MaxAlloc: 256.5 KiB
MaxAlloc: 262.1 KiB
MaxAlloc: 263.1 KiB
MaxAlloc: 266.8 KiB
MaxAlloc: 269.9 KiB
2023/05/01 08:25:25 Successfully generated markdown table.
2023/05/01 08:25:25 
        To view the memory profile, run the following command:
        go tool pprof mem-streaming.prof
        go tool pprof -alloc_space mem-streaming.prof
2023/05/01 08:25:25 Time elapsed: 7.61040447s
```

Not bad!

## Optimizing the formatting

We now have a system that efficiently reads from storage (through the sqlite3 C driver) and efficiently streams out
to the output interface (using a channel of `User` structs). The only work our software does is setting up the 
"DMA channels" (the goroutines streaming in and streaming out). But we are still quite slow, 8 seconds to copy 100 MB
of data from disk to stdout is not what I would call "efficient". In fact, we are just as fast as we were without streaming,
despite operating in parallel. 

We could now start CPU profiling, but let's go back to our hardware example. Is there are a way to get our data out
of the storage "hardware" in a format that's already ready to be consumed by the output "hardware"? It turns out that yes,
we can use SQL and SQLite string functions to already render our data in the markdown row format and let sqlite and its low-level
C implementation do the job, instead of relying on our expensive reflection-based go template engine. At this point in our toy
example, it is valid to ask ourselves if a template engine is necessary at all, but the real world is more complex than this.

Here is a final version of the code that uses SQL to render the markdown table. Note that by streamling the rendering
by letting sqlite do part of the formatting and by coupling the database IO to the template rendering IO, we 
significantly couple the rendering on the one hand and the database querying on the other. Passing a channel to the 
template engine is reasonably elegant but by transforming it into a `chan string` from a `chan User`, we are putting
the burden of creating valid markdown on to the SQL query itself. This is a tradeoff worth discussing.


```go 
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
```

And the usual measurements, although I would put quite a bit more effort into making sure I am measuring the right
thing if I set out to actually optimize runtime, and not memory consumption (all we do here is measure wall time 
in the `main()` function, with heap profiling active of all things!).

```
❯ go run . generate --streaming-string --trigger-gc > /dev/null
MaxAlloc: 238.3 KiB
MaxAlloc: 251.8 KiB
MaxAlloc: 256.7 KiB
MaxAlloc: 263.1 KiB
MaxAlloc: 269.5 KiB
MaxAlloc: 275.0 KiB
2023/05/01 08:33:43 Successfully generated markdown table.
2023/05/01 08:33:43 Time elapsed: 2.743605651s
```

From 7 seconds to 3 seconds, a fairly impressive improvement! From a memory perspective, nothing changes very much,
except that we are allocating less temporary data (all those little strings inside the `User` struct) in our rendering
loop.

``` 
❯ go tool pprof -alloc_space mem-streaming.prof

File: go-template-streaming
Build ID: 62a73a3f4794306f68b637fe7bf0470a5a744b34
Type: alloc_space
Time: May 1, 2023 at 8:35am (EDT)
Entering interactive mode (type "help" for commands, "o" for options)
(pprof) top
Showing nodes accounting for 303.52MB, 100% of 303.52MB total
Showing top 10 nodes out of 21
      flat  flat%   sum%        cum   cum%
  135.01MB 44.48% 44.48%   135.01MB 44.48%  github.com/mattn/go-sqlite3.(*SQLiteRows).Next
   68.01MB 22.41% 66.89%    68.01MB 22.41%  github.com/mattn/go-sqlite3._Cfunc_GoStringN (inline)
      36MB 11.86% 78.75%       36MB 11.86%  reflect.Value.recv
      16MB  5.27% 84.02%   151.01MB 49.75%  main.generateStreamingStringMarkdown.func1
      13MB  4.28% 88.30%       13MB  4.28%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func9
      12MB  3.95% 92.26%   109.51MB 36.08%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked
       8MB  2.64% 94.89%        8MB  2.64%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func10
       7MB  2.31% 97.20%       43MB 14.17%  text/template.(*state).walkRange
    4.50MB  1.48% 98.68%     4.50MB  1.48%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func3
       4MB  1.32%   100%        4MB  1.32%  github.com/mattn/go-sqlite3.(*SQLiteRows).nextSyncLocked.func1
```

Through first principles, and with not very much code at all, we were able to turn a program consuming 200 MB of memory
(and being O(n) in terms of peak heap size) and running for 8 seconds into a program consuming 300 kB of peak heap size
and running for 3 seconds!

## What about sqlite and the kernel?

But manuel, you ask, you are talking all sweet about thinking holistically and seeing the system, but you are just
measuring go's heap behaviour, in go itself, of all things. How do you know you are measuring the right thing?
This is indeed a great question, but the article is already long enough (and to be honest, I'm quite rusty in doing
whole system optimization work), so I'll leave you with a couple of links and we'll hopefully revisit this topic in a subsequent
post:

- [EBPF homepage](https://ebpf.io/) - a technology that allows you to write programs that run in the kernel. It comes
  with a huge set of already written program that are a great way to look inside the kernel for performance insights.
- [perf: Linux profiling with performance counters](https://perf.wiki.kernel.org/index.php/Main_Page) - perf is a
  profiler that uses CPU performance counters across the entire system, not just your userland program. It is great to
  see if bottlenecks are hidden in plain sight within the libraries or subsystems your program uses.
- [Every Computer Performance Book](https://bookshop.org/p/books/every-computer-performance-book-how-to-avoid-and-solve-performance-problems-on-the-computers-you-work-with-bob-wescott/9848820?ean=9781482657753) -
  my favourite book about computer systems from the hardware up to the C runtime. It is not an excruciatingly detailed
  reference, but instead comes with a lot of source code examples.
- [Computer Systems: A Programmer's Perspective](https://bookshop.org/p/books/computer-systems-a-programmer-s-perspective-randal-bryant/8982319?ean=9780134092669) -
  a small book that gives you a clear introduction to the philosophy of performance optimization and some pitfalls to
  avoid. Applicable widely, beyond just memory and runtime optimization.
- [Systems Performance](https://bookshop.org/p/books/systems-performance-brendan-gregg/14715855?ean=9780136820154) - a
  quite detailed book about performance optimization, with a focus on linux.
- [BPF Performance Tools](https://bookshop.org/p/books/bpf-performance-tools-brendan-gregg/9643920?ean=9780136554820) -
  a detailed book about eBPF and its many applications to performance optimization.
