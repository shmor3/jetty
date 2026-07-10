# Jetty

Jetty is a local Jettyfile runner for small build and automation workflows. It executes shell commands, file operations, nested builds, and selected asynchronous steps while keeping build state isolated from the parent process.

Jettyfiles are trusted input. `RUN`, `CMD`, `USE`, and `JET` can execute commands on the host or in Docker containers, so do not run Jettyfiles from untrusted sources.

## Features

- Line-oriented Jettyfile syntax with `ARG`, `ENV`, `RUN`, `CMD`, `DIR`, `CPY`, `WDR`, `SUB`, `FMT`, `FRM`, `BOX`, `USE`, and `JET`.
- Asynchronous `*RUN`, `*CPY`, and `*SUB` steps. Jetty waits for async work before running the final `CMD`.
- Per-build working directory and environment. `WDR` does not call `chdir` on the Jetty process.
- Nested builds with paths resolved relative to the current Jettyfile working directory.
- Build status snapshots in `.jetty/builds.json`, readable with `jetty ps`.
- Optional Docker-backed execution with `FRM`/`BOX` and `USE`.

## Install

```bash
git clone https://github.com/shmor3/jetty.git
cd jetty
go build -o jetty .
```

## Usage

```bash
jetty init
jetty build
jetty build -f path/to/Jettyfile
jetty ps
jetty ps -a
jetty ps -a -f status=Failed
```

`jetty build` uses `Jettyfile` in the current directory unless a file is passed with `-f` or as a positional argument.

`jetty ps` reads `.jetty/builds.json` from the current directory by default. Set `JETTY_STATE_DIR` to write and read status from another directory.

## Jettyfile Example

```jetty
ARG NAME=jetty
ENV GREETING=hello

DIR build
^FMT build/message.txt "%s %s" $GREETING $NAME

*RUN echo async step
SUB sub.Jettyfile

CMD echo finished
```

## Directives

| Directive | Description |
| --- | --- |
| `ARG KEY=value` | Defines a build argument. Jetty expands `$KEY` from arguments before running directives. |
| `ENV KEY=value` | Defines an environment variable for later `RUN`, `CMD`, `USE`, and `JET` commands. |
| `RUN command` | Runs a shell command in the current build working directory. |
| `*RUN command` | Runs a shell command asynchronously. |
| `CMD command` | Runs once after all other instructions and after async work completes. Only one `CMD` is allowed. |
| `DIR path` | Creates a directory relative to the current build working directory. |
| `WDR path` | Changes Jetty's build-local working directory for later directives. |
| `CPY source destination` | Copies a file or directory. |
| `*CPY source destination` | Copies a file or directory asynchronously. |
| `SUB file` | Runs another Jettyfile synchronously. The sub-build inherits current args and environment. |
| `*SUB file` | Runs another Jettyfile asynchronously. |
| `FMT format args...` | Formats a string and writes it to build output. |
| `^FMT file format args...` | Appends a formatted string to a file. |
| `$FMT NAME format args...` | Stores a formatted string in the build environment. |
| `&FMT NAME format args...` | Stores a formatted string in build args. |
| `FRM image[:tag]` | Sets the default Docker image for later `USE` directives. |
| `BOX name image[:tag]` | Defines a named Docker image. `BOX name repository tag` is also supported. |
| `USE [box] command` | Runs a command inside a Docker box. If no box is supplied, the `FRM` default is used. |
| `JET plugin [args...]` | Executes a plugin from `plugins/plugin` relative to the current working directory, or from an explicit path. |

`RUN` and `CMD` use `sh -c` on Unix. On Windows, Jetty uses `sh -c` when `sh` is available, otherwise it falls back to `cmd /C`.

Argument and environment names must start with a letter or underscore and may contain only letters, numbers, and underscores. `CPY` refuses to copy a directory into itself or one of its descendants.

## Paths

Relative paths are resolved from the directory containing the current Jettyfile. After `WDR`, relative paths resolve from the new build-local working directory. Jetty does not mutate the parent process working directory.

## Status

Builds write status records as JSON:

```json
[
  {
    "id": "1770000000000000000",
    "status": "Completed",
    "start_time": "2026-07-10T12:00:00Z",
    "end_time": "2026-07-10T12:00:01Z",
    "worker_node": "local",
    "file_name": "/path/to/Jettyfile"
  }
]
```

Use `jetty ps` for active builds and `jetty ps -a` for active and completed builds. Supported filters are `id=`, `status=`, `worker=`, and `file=`.

## Development

```bash
go test ./...
go vet ./...
gofmt -w .
```

## License

This project is licensed under the [MIT License](LICENSE).
