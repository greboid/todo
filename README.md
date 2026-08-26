# Todo

A self-hosted, single-binary todo app. A Go HTTP server embeds a Svelte 5 SPA
and stores your data in SQLite (default) or Postgres, so the whole thing ships
as one executable with no external dependencies and no accounts.

## Features

- **Boards** to group related work. One board is shown at a time; switch from
  the header bar. The last board can't be deleted.
- **Nested subtasks**. Any todo can have subtasks, which always live on the
  same board as their parent.
- **Drag and drop** to reorder todos, nest them under a parent, or move them
  to the root of the board.
- **Labels** for lightweight tagging and filtering.
- **Due dates** entered in plain English in a single field: `today`,
  `tomorrow`, `aug 15`, `next monday`, `in 2 weeks`, `in a fortnight`,
  `end of month`, `this weekend`, and more.
- **Recurring tasks** (Todoist-style): `every day`, `every 2 weeks on mon, wed`,
  `every month on the 15th`, `every last friday`, `every month on the last day
  starting sep 1 ending dec 31`. Completing a recurring todo automatically
  creates the next incomplete occurrence.
- **Completion cascades** to descendants: checking a parent marks its whole
  subtree done.
- **Date-only**. Times (`at 3pm`, `every hour`) are not supported; this keeps
  the grammar simple and unambiguous.
- **Default due date** (optional): start the server with `-default-due` to
  stamp a due date or recurrence onto every new todo that doesn't set one.

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

Equivalent Compose files for both storage backends — save either one as
`compose.yaml` and run `docker compose up -d` (or `podman compose up -d`).

> **Warning**
> The app has **no authentication**, so it must never be exposed to a public
> interface. Both files below bind the port to loopback (`127.0.0.1`) only:
> run a reverse proxy on the same host and forward it to
> <http://127.0.0.1:8080>. Whatever the proxy is (nginx, Caddy, Traefik, ...),
> let it own TLS and put some access control in front (basic auth, client
> certs, VPN, IP allowlist). If the reverse proxy itself runs as a container,
> delete the `ports` section and instead attach both containers to a shared
> Compose network, proxying the service name (`http://todo:8080`) with no
> published port at all.

**SQLite (default):**

```yaml
services:
  todo:
    # CI publishes this image from the Containerfile. Pin a release tag
    # instead of tracking latest, e.g. ghcr.io/greboid/todo:0.1.1.
    # (Or build from source: uncomment the next line and run with --build.)
    # build: .
    image: ghcr.io/greboid/todo:latest
    restart: unless-stopped
    # Loopback only — do NOT widen this to "8080:8080". That publishes the
    # port on every interface and, since there is no auth, anyone who can
    # reach the host can read and edit the data.
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - todo-data:/data

volumes:
  todo-data:
```

**Postgres** (change both passwords before use; the app connects to Postgres
exactly once at startup with no retry, hence the healthcheck-gated
`depends_on`):

```yaml
services:
  todo:
    # CI publishes this image from the Containerfile. Pin a release tag
    # instead of tracking latest, e.g. ghcr.io/greboid/todo:0.1.1.
    # (Or build from source: uncomment the next line and run with --build.)
    # build: .
    image: ghcr.io/greboid/todo:latest
    restart: unless-stopped
    # Loopback only — do NOT widen this to "8080:8080". That publishes the
    # port on every interface and, since there is no auth, anyone who can
    # reach the host can read and edit the data.
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      TODO_DB_DRIVER: postgres
      TODO_DB: postgres://todo:pick-a-password@db:5432/todo?sslmode=disable
    depends_on:
      db:
        condition: service_healthy

  db:
    image: postgres:17
    restart: unless-stopped
    environment:
      POSTGRES_USER: todo
      POSTGRES_PASSWORD: pick-a-password
      POSTGRES_DB: todo
    volumes:
      - todo-db:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U todo -d todo"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  todo-data:
  todo-db:
```

## Configuration

The app is configured with command-line flags. Each flag can also be set
through an environment variable of the same name, upper-cased with dashes as
underscores and prefixed with `TODO_` (`-api-key` → `TODO_API_KEY`); a
command-line argument wins when both are given:

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `-addr` | `TODO_ADDR` | `:8080` | Address the HTTP server listens on |
| `-db-driver` | `TODO_DB_DRIVER` | `sqlite` | Database backend: `sqlite` (also `sqlite3`) or `postgres` (also `pg`/`postgresql`) |
| `-db` | `TODO_DB` | `todo.db` | For SQLite, the database file path; for Postgres, a libpq-style connection string |
| `-api-key` | `TODO_API_KEY` | _(empty)_ | Optional API key guarding `/api`; requests must send it as an `X-API-Key` header or a Bearer token, while browsers get a session cookie instead (empty disables authentication) |
| `-default-due` | `TODO_DEFAULT_DUE` | _(empty)_ | Default due/repeating schedule for new todos without one of their own, in the quick-add date grammar (e.g. `"tomorrow"`, `"every monday"`, `"in 3 days"`); empty means no due date |

An invalid `-default-due` value prevents startup. When set, the value is
re-parsed each time a todo is created without its own schedule, so relative
dates resolve against the day of creation: with `-default-due "every monday"`,
a todo added on a Wednesday is due the following Monday. Todos created through
any client (web UI or API) get the default; one that already carries a
`dueDate` or recurrence — e.g. from a trailing schedule in the quick-add text —
is left alone. To deliberately skip the default, end the quick-add line with
the keyword `never` (`buy milk never`) or send `noSchedule: true` on the API
call; the todo is then created with no due date.

### SQLite (default)

No extra configuration is needed — the database is created next to the binary
in the current working directory. Set `TODO_DB` to an absolute path (or run the
container with a mounted `/data`) to control where it lives:

```sh
TODO_DB=/var/lib/todo/todo.db ./todo
```

### Postgres

Point the app at an existing Postgres instance by setting `TODO_DB_DRIVER` and
passing a libpq-style connection string in `TODO_DB`:

```sh
TODO_DB_DRIVER=postgres \
TODO_DB='postgres://user:pass@host:5432/todo?sslmode=disable' \
./todo
```

The app applies its own schema on startup, so just create an empty database
and grant the connecting role permission to create tables.
