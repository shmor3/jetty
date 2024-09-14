# Jetty Build Script

This is a Jetty build script. Jetty is a custom build tool with its own set of instructions

Jettyfile

```jetty
ARG TEST_ARG='arg works'
ENV TEST_ENV='env works'
RUN echo 'run works'
RUN echo $TEST_ENV
RUN echo $TEST_ARG \
    echo 'multiline works' \
    echo 1 \
    echo 2 \
    echo 3
DIR ./test
WDR ./test
DIR ./itworks
CMD echo 'it works'
```

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

Demonstrates how Jetty handles multi-line instructions

```jetty
RUN echo $TEST_ARG \
 echo 'multiline works' \
 echo 1 \
 echo 2 \
 echo 3
```

## DIR: Changes the working directory for subsequent operations

```jetty
DIR ./test
```

## WDR: Another directory-related instruction (purpose may need clarification)

```jetty
WDR ./test
```

## Another DIR instruction

```jetty
DIR ./itworks
```

## CMD: Specifies the command to run when the build completes

```jetty
CMD echo 'it works'
```

## Building Jetty

Clone this project

```bash
go build .
```

## Usage

```bash
./jetty -h
```

Create Jettyfile in project directory

Run jetty in project directory

```bash
./jetty build
```
