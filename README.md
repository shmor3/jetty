# Jetty

Jetty is a fast, lightweight, line-oriented build runner and task automation tool. It acts as a powerful alternative to complex Makefile setups, letting you orchestrate shell commands, nested builds, asynchronous execution, and Docker containers with a clean, Dockerfile-like syntax.

Built with extreme multi-platform reliability in mind, Jetty is designed to be a mission-critical component in development workflows.

---

## Why Jetty?

- **Simple & Familiar:** The `Jettyfile` syntax is incredibly similar to Dockerfiles, making it immediately intuitive.
- **Async by Design:** Prepend `*` to any async-capable instruction (`*RUN`, `*CPY`, `*SUB`, `*USE`, or `*JET`) and Jetty immediately forks it to the background, waiting for it to finish gracefully before evaluating the final `CMD`.
- **First-Class Docker Support:** `USE` commands transparently route execution into lightweight Docker containers while automatically mounting your local workspace.
- **Cross-Platform:** Out of the box, Jetty handles path conversion (`\` vs `/`), carriage returns (`\r\n`), native Windows environments, and proper Unix process grouping.
- **Mission Critical:** Features execution timing telemetry, native graceful shutdown signals (`SIGTERM` / `os.Interrupt`) across process groups, and a CPU-bound concurrency limit on background leaf instructions to bound parallel resource usage. Sub-builds are exempt from the limit so nested async builds cannot starve their own children.
- **Build Isolation:** Working directories and environment variables are scoped per-build so sibling builds don't clobber each other's context. Note that directive paths (e.g. `CPY`, `WDR`, `^FMT`) are not sandboxed to the workspace — Jetty runs your instructions with your own permissions.

## Install

```bash
git clone https://github.com/shmor3/jetty.git
cd jetty
go build -o jetty .
# Move 'jetty' to your system PATH
```

## Quick Start

Initialize a new project:
```bash
jetty init
```

Run the build:
```bash
jetty build
```

Check the status of your current and historical builds:
```bash
jetty ps -a
```

## Anatomy of a Jettyfile

Jetty reads instructions line by line. Line continuations (`\`) are supported exactly like shell scripting or Dockerfiles.

Here is a quick tour of what Jetty can do:

### 1. Variables and Environment

```jetty
ARG NAME=World
ENV GREETING=Hello

RUN echo "$GREETING $NAME!"
```

### 2. Filesystem Ops

```jetty
# Create a directory safely
DIR build_output

# Change Jetty's build-local working directory (does not affect the parent shell)
WDR build_output

# Copy files locally. Append `*` to run the copy in the background!
*CPY ../src ./src_backup
```

### 3. Docker Container Execution

You don't need complex `docker run` scripts. Jetty abstracts it natively via `USE` and `BOX`.

```jetty
# Define an alias to a specific docker image
BOX node node:18-alpine

# Set a default fallback image
FRM golang:1.20-alpine

# Execute inside the Node container! Jetty mounts your host directory to /workspace inside the container.
USE node npm install

# Run a Go command using the fallback image
USE go build -o my_app .
```

### 4. Advanced Formatting

Jetty includes a built-in formatting engine (`FMT`) to generate dynamic configs without invoking `sed` or `awk`.

```jetty
# Write a formatted string directly to a file
^FMT config.json "{ \"app\": \"%s\" }" $NAME

# Store a formatted string directly into an environment variable!
$FMT LOG_PREFIX "[%s] LOG:" $NAME
```

### 5. Multi-line Commands

```jetty
RUN echo "This command \
          spans multiple lines \
          and works perfectly."
```

## Core Directives

| Directive | Description |
| --- | --- |
| `ARG KEY=value` | Defines a build argument. Jetty expands `$KEY` dynamically during execution. |
| `ENV KEY=value` | Defines a persistent environment variable scoped to the current build execution. |
| `RUN command` | Executes a shell command on the host. |
| `*RUN command` | Executes a shell command *asynchronously*. |
| `DEP path...` | Declares input files for the next cacheable step (`RUN`/`CPY`/`USE`). Their contents form the cache key. |
| `OUT path...` | Declares the output files a cacheable step produces. When the `DEP` inputs and the existing outputs are both unchanged, the step is skipped and reported as `CACHED`. |
| `CMD command` | Runs once after all other instructions (and background tasks) are finished. Only one allowed per file. |
| `DIR path` | Creates a directory recursively (`mkdir -p`) within the build workspace. |
| `WDR path` | Changes the current working directory for subsequent instructions. |
| `CPY src dest` | Copies a file or directory from `src` to `dest`. |
| `*CPY src dest` | Copies a file or directory *asynchronously*. |
| `SUB target` | Delegates execution to another Jettyfile locally or via GitHub import syntax (`github.com/owner/repo[@ref][/path]`). |
| `*SUB target` | Delegates execution to another Jettyfile *asynchronously*. |
| `FMT format args...` | Formats a string and emits it as a `FMT:` build-log line. |
| `^FMT file format args...` | Appends a formatted string to a target file. |
| `$FMT NAME format args...` | Formats a string and assigns it to an environment variable (`$NAME`). |
| `&FMT NAME format args...` | Formats a string and assigns it to a build argument (`$NAME`). |
| `FRM image[:tag]` | Sets the default Docker image for subsequent `USE` directives. |
| `BOX name image[:tag]` | Aliases a Docker image to a simpler name. |
| `USE [box] command` | Executes a command inside a Docker container (mounting the host workspace). |
| `*USE [box] command` | Executes a command inside a Docker container *asynchronously*. |
| `JET plugin [args...]` | Executes a Jetty plugin from the local `plugins/` directory or an absolute path. |
| `*JET plugin [args...]` | Executes a Jetty plugin *asynchronously*. |

## Status and Configuration

Run `jetty` or `jetty status` to view a tabular history of completed and active builds across your machine.
- `jetty build [-f file] [--env-file file] [file]`: Runs a Jettyfile build. Optionally specify an explicit .env file.
- `jetty validate [file]`: Validates the syntax of a Jettyfile without executing it.
- `jetty ps -a`: Lists all builds with truncated IDs and execution metadata.
- `jetty ps`: Lists only actively running asynchronous builds.
- `jetty clean`: Automatically garbage-collects all status history and clears the local state directory.
- `jetty help <command>`: View detailed CLI help.

## Secrets and 12-Factor Variables
By default Jetty loads any `.env` file located in the same directory as the executing `Jettyfile`. These variables are injected straight into the build context and seamlessly made available to `*RUN`, `*USE`, and `*JET` environments! Passing `--env-file` **replaces** this automatic load: only the file you specify is read, and the adjacent `.env` is not.

> **Remote sub-builds:** `SUB github.com/owner/repo[@ref][/path]` fetches and executes a remote Jettyfile with your local build context — including parent args (but not an implicit `.env`, which is skipped for remote fetches) — on your host. Only import repositories you trust, and pin a commit or tag with `@ref`; the default `main` is a mutable branch that can change between runs. The `@ref` may not contain a `/`, so slashed branch names (e.g. `feature/x`) are not supported — use a tag or commit SHA instead.

**Environment Variables:**
- `JETTY_STATE_DIR`: Overrides the default `.jetty` state storage location.
- `JETTY_TIMEOUT`: Overrides the global 10-minute timeout limit (e.g. `export JETTY_TIMEOUT=30m`).

## Development

Jetty is written in pure Go and designed to run on Windows, Linux, and macOS. Automated CI currently builds and tests on Linux; release binaries target `linux/amd64`.

```bash
go test ./...
go vet ./...
go build -o jetty .
```

## License
MIT
