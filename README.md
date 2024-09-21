````markdown
# jetty: A Concurrent Build System

jetty is a powerful, concurrent build system that processes build instructions from a file and executes them in a distributed manner using worker nodes. It's designed for efficiency, flexibility, and ease of use in complex build environments.

## Features

-   Concurrent execution of build instructions
-   Distributed processing with a worker pool
-   Support for various build directives (ARG, ENV, RUN, CMD, DIR, CPY, WDR, SUB, FRM, BOX, USE, JET)
-   Real-time build status tracking
-   Asynchronous execution with \*RUN flag
-   Nested builds support with SUB directive
-   Docker integration for containerized builds

## How It Works

jetty executes the build process for a given file by:

1. Creating a worker pool
2. Assigning the build to a worker
3. Parsing and executing instructions from the file
4. Handling concurrent execution of instructions
5. Supporting asynchronous execution with \*RUN
6. Allowing nested builds with SUB directive
7. Executing the final CMD instruction if present

## Jettyfile Syntax

Here's an example of a `Jettyfile` showcasing various directives:

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
*RUN sleep 5
DIR ./test
WDR ./test
DIR ./itworks
SUB ./sub-build.jetty
CMD echo 'it works'
```
````

## Directives and Symbols

-   ARG: No specific symbols
-   ENV: No specific symbols
-   RUN: Can use "\*" symbol
-   CMD: No specific symbols
-   DIR: No specific symbols
-   CPY: Can use "\*" symbol
-   WDR: No specific symbols
-   SUB: Can use "\*" symbol
-   FRM: No specific symbols
-   JET: No specific symbols
-   FMT: Can use "^", "$", "&" symbols
-   BOX: No specific symbols
-   USE: No specific symbols

### Directive-Symbol Compatibility

-   **RUN**, **CPY**: Can be prefixed with \* for asynchronous execution
-   **FMT**: Can use ^, $, & symbols for different formatting options
-   Other directives: Do not have currently have specific symbol modifiers

## Installation

1. Clone the repository:

    ```bash
    git clone https://github.com/yourusername/jetty.git
    ```

2. Build the project:
    ```bash
    cd jetty
    go build .
    ```

## Usage

To see available commands:

```bash
./jetty -h
```

### Available Commands

1. **init**: Create a new Jettyfile in the current directory

    ```bash
    jetty init
    ```

2. **ps**: View the status of builds

    ```bash
    jetty ps [-a] [-f filter]
    ```

    Options:

    - `-a`: Show all builds (active and completed)
    - `-f`: Filter builds (e.g., "id=buildid")

3. **build**: Run a new build
    ```bash
    jetty build [-f filename]
    ```
    Options:
    - `-f`: Specify the build file (default: Jettyfile in current directory)

## Getting Started

1. Create a `Jettyfile` in your project directory
2. Run jetty in your project directory:
    ```bash
    ./jetty build
    ```

## Contributing

We welcome contributions! Please see our [CONTRIBUTING.md](CONTRIBUTING.md) file for details on how to contribute to jetty.

## License

This project is licensed under the [MIT License](LICENSE).

## Support

If you encounter any issues or have questions, please file an issue on the GitHub issue tracker.

---

Happy building with jetty! 🚀

```

This version of the README.md is now consistent with the codebase we've seen and ready to be committed.
```
