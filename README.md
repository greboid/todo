# Todo

A self-hosted, single-binary todo app. A Go HTTP server embeds a Svelte 5 SPA
and stores everything in a local SQLite database, so the whole thing ships as
one executable with no external dependencies and no accounts.

## Features

- **Boards** to group related work. One board is shown at a time; switch from
  the header bar. The last board can't be deleted.
- **Nested subtasks**. Any todo can have subtasks, which always live on the
  same board as their parent.
- **Drag and drop** to reorder todos, nest them under a parent, or move them
  to the root of the board.
- **Labels** for lightweight tagging and filtering.
- **Due dates** entered in plain English in a single field: `today`,
  `tomorrow`, `aug 15`, `next monday`, `in 2 weeks`, `end of month`,
  `this weekend`, and more.
- **Recurring tasks** (Todoist-style): `every day`, `every 2 weeks on mon, wed`,
  `every month on the 15th`, `every last friday`, `every month on the last day
  starting sep 1 ending dec 31`. Completing a recurring todo automatically
  creates the next incomplete occurrence.
- **Completion cascades** to descendants: checking a parent marks its whole
  subtree done.
- **Date-only**. Times (`at 3pm`, `every hour`) are not supported; this keeps
  the grammar simple and unambiguous.

### Due date and recurrence examples

The due date and recurrence rule are entered together in one text field. A
relative date like `tomorrow` resolves from your browser's local today, not the
server's clock.

| You type | Result |
| --- | --- |
| `today` / `tom` / `tod` | Today's date |
| `tomorrow` / `tmr` | Tomorrow |
| `aug 15` / `15 aug` / `15/8` | That calendar date |
| `next monday` | Next week's Monday (Monday-start weeks) |
| `in 2 weeks` | Two weeks from today |
| `end of month` | Last day of the current month |
| `every day` | Daily recurrence |
| `every 3 days` | Every third day |
| `every 2 weeks on mon, wed` | Every other week on those weekdays |
| `every weekday` / `every weekend` | Mon-Fri / Sat-Sun |
| `every month on the 15th` | Monthly on the 15th |
| `every last friday` | Monthly on the last Friday |
| `every year` | Yearly |
| `every! day` | Advance from the completion date, not the due date |
| `every day ending dec 31` | Stop recurring after Dec 31 |
| `every day starting sep 1` | First occurrence Sep 1 |

A blank field clears both the due date and the recurrence.

## Getting started

### Prerequisites

- [Go](https://go.dev/) 1.26 or newer
- [Node.js](https://nodejs.org/) and [pnpm](https://pnpm.io/) (only to build
  the frontend, which is embedded into the binary)

### Build from source

The Go binary embeds a pre-built copy of the frontend, so the SPA must be
built first. `go generate` handles both the `pnpm install` and `pnpm build`
steps:

```sh
go generate ./..
go build -o todo .
```

Then run it:

```sh
./todo
```

Open <http://localhost:8080> in your browser.

### Container

A `Containerfile` is included for building an OCI image. The final stage runs
as a non-root user from `/data`, so mount a volume there to persist the
database:

```sh
podman build -t todo .
podman run -p 8080:8080 -v todo-data:/data todo
```

## Configuration

The app is configured entirely through environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `TODO_ADDR` | `:8080` | Address the HTTP server listens on |
| `TODO_DB` | `todo.db` | Path to the SQLite database file |

By default the database is created next to the binary in the current working
directory. Set `TODO_DB` to an absolute path (or run the container with a
mounted `/data`) to control where it lives.
