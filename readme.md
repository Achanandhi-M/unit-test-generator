# Unit Test Generator

A tool that automatically generates unit tests for C++ code using Ollama's AI models and Google Test framework.

## Features

- Generates Google Test unit tests for C++ code
- Validates generated tests by compiling and running them
- Measures code coverage and enforces minimum thresholds
- Supports both header (.h) and implementation (.cpp) files
- Works with Ollama's local AI models

## Prerequisites

- [Go](https://golang.org/dl/) (1.20+ recommended)
- [Ollama](https://ollama.ai/) installed and running
- C++ compiler (g++ or clang++)
- Google Test installed
- gcov for coverage analysis

## Installation

1. Clone this repository:
   ```bash
   git clone https://github.com/yourusername/unit-test-generator.git
   cd unit-test-generator
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Pull the required Ollama model:
   ```bash
   ollama pull qwen2.5-coder:7b
   ```

## Usage

1. Place your C++ code in the `codebase` directory (both .h and .cpp files)

2. Run the generator:
   ```bash
   go run main.go
   ```

3. Generated tests will be saved in the `tests` directory

## Configuration

### Environment Variables

- `OLLAMA_HOST`: URL of Ollama server (default: `http://localhost:11434`)

### Directory Structure

```
unit-test-generator/
├── codebase/       # Your C++ source files go here
├── tests/          # Generated unit tests will be saved here
├── go.mod
├── go.sum
└── main.go         # Main application code
```

## Made with ❤️ and too much debugging