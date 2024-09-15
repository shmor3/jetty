# jettyctl Build System

jettyctl is a concurrent build system that processes build instructions from a file and executes them in a distributed manner using worker nodes.

## Features

-   Concurrent execution of build instructions
-   Worker pool for distributed processing
-   Support for various build directives (ARG, ENV, RUN, CMD, DIR, CPY, WDR, SUB, FRM, BOX, USE, JET)
-   Real-time build status tracking
-   Asynchronous execution with \*RUN flag
-   Sub-build support with SUB directive

Executes the build process for a given file:

1. Creates a worker pool
2. Assigns the build to a worker
3. Parses and executes instructions from the file
4. Handles concurrent execution of instructions
5. Supports asynchronous execution with \*RUN
6. Allows nested builds with SUB directive
7. Executes the final CMD instruction if present

## jettyctlfile

ARG TEST_ARG='arg works'
ENV TEST_ENV='env works'
RUN echo 'run works'
RUN echo $TEST_ENV
RUN echo $TEST_ARG \
 echo 'multiline works' \
 echo 1 \
 echo 2 \
 echo 3
\*RUN sleep 5
DIR ./test
WDR ./test
DIR ./itworks
SUB ./sub-build.jettyctl
CMD echo 'it works'

## ARG: Defines a build-time variable

```jetty
ARG TEST_ARG='arg works'
```

## ENV: Sets an environment variable for the build process

```jetty
ENV TEST_ENV='env works'
```

## RUN: Executes a command during the build

```jetty
RUN echo 'run works'
```

## Another RUN command, echoing the value of an environment variable

```jetty
RUN echo $TEST_ENV
```

## Multi-line RUN command

Demonstrates how jettyctl handles multi-line instructions

```jetty
RUN echo $TEST_ARG \
 echo 'multiline works' \
 echo 1 \
 echo 2 \
 echo 3
```

## \*RUN: Executes a command asynchronously

```jetty
*RUN sleep 5
```

## DIR: Changes the working directory for subsequent operations

```jetty
DIR ./test
```

## WDR: Another directory-related instruction (purpose may need clarification)

````jetty
WDR .```
/test

## Another DIR instruction

```jetty
DIR ./itworks
````

## CMD: Specifies the command to run when the build completes

```jetty
CMD echo 'it works'
```

## Building jettyctl

```bash
Clone this project
```

```bash
go build .
```

## Usage

```jetty
./jettyctl -h
```

Create jettyctlfile in project directory

Run jettyctl in project directory

```jetty
init
```

-   Description: Create a new Jettyfile in the current directory
-   Usage: jettyctl init

```jetty
ps
```

-   Description: View the status of builds
-   Usage: jettyctl ps [-a] [-f filter]
-   Options:
-   -a: Show all builds (active and completed)
-   -f: Filter builds (e.g., "id=buildid")

```jetty
build
```

-   Description: Run a new build
-   Usage: jettyctl build -f filename
-   Options:
-   -f: Specify the build file

```jetty
./jettyctl build
```
