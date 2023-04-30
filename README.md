# Rendering a template in go: a performance optimization tale

Points:

## Introduction

I have been building web functionality of [sqleton](https://github.com/go-go-golems/sqleton)
this week. Sqleton is an application that allows you to define CLI commands in a [YAML file](https://github.com/go-go-golems/sqleton/blob/main/cmd/sqleton/queries/wp/ls-posts.yaml)
to render a SQL template and fire it off against a database.

It is now possible to [serve these commands](https://github.com/go-go-golems/sqleton/blob/main/cmd/sqleton/cmds/serve.go) as little webpages:
the CLI flags are rendered as a HTML form, and the resulting structured data (which is served 
by the [glazed](https://github.com/go-go-golems/glazed) library) is rendered as a HTML table.

- create a db with users
- render template naively
- measuring the allocations
- measuring the top heap size
- stream user structures to the template
- have the DB do the string concatenation
- measuring the database performance
- measuring the allocations