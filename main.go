package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

func readCodebase(dir string) (map[string]string, error) {
	filesContent := make(map[string]string)
	log.Printf("Reading codebase directory: %s", dir)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error accessing path %s: %v", path, err)
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(info.Name(), ".cpp") || strings.HasSuffix(info.Name(), ".h")) {
			log.Printf("Found file: %s", path)
			content, err := os.ReadFile(path)
			if err != nil {
				log.Printf("Error reading file %s: %v", path, err)
				return err
			}
			filesContent[path] = string(content)
			log.Printf("Successfully read file %s (%d bytes)", path, len(content))
		}
		return nil
	})
	if err != nil {
		log.Printf("Failed to walk codebase directory %s: %v", dir, err)
	} else {
		log.Printf("Found %d files in codebase", len(filesContent))
	}
	return filesContent, err
}

func generateUnitTests(client *api.Client, model, code string) (string, error) {
	log.Printf("Generating unit tests with model %s (code length: %d bytes)", model, len(code))
	resp, err := client.List(context.Background())
	if err != nil {
		log.Printf("Failed to list models: %v", err)
		return "", err
	}
	availableModels := []string{model}
	for _, m := range resp.Models {
		if m.Name != model {
			availableModels = append(availableModels, m.Name)
		}
	}
	log.Printf("Available models: %v", availableModels)

	// Explicitly specify methods to test
	methods := []string{"add", "subtract"}
	methodsList := strings.Join(methods, ", ")

	prompt := strings.Join([]string{
		"You are an expert C++ programmer tasked with generating unit tests using Google Test for the provided C++ code. Follow these requirements strictly:",
		"- Use C++17 standard.",
		"- Include exactly these headers: `#include <gtest/gtest.h>`, `#include <cmath>`, `#include <stdexcept>`, `#include \"example.h\"`.",
		"- Use `TEST` macros with descriptive names (e.g., `TEST(CalculatorTest, Add_PositiveNumbers)).",
		fmt.Sprintf("- Write tests for these methods only: %s.", methodsList),
		"- Write exactly 4 test cases (2 per method): one for positive inputs and one for negative inputs.",
		"- Avoid edge cases involving INT_MIN or INT_MAX to prevent integer overflow issues.",
		"- Ensure each `TEST` macro has complete braces `{}` and valid assertions (`EXPECT_EQ`).",
		"- Output a complete, syntactically correct .cpp file without Markdown code fences, comments outside test code, or extra text.",
		"- Example format:",
		"#include <gtest/gtest.h>",
		"#include <cmath>",
		"#include <stdexcept>",
		"#include \"example.h\"",
		"TEST(CalculatorTest, Add_PositiveNumbers) {",
		"    Calculator calc;",
		"    EXPECT_EQ(calc.add(2, 3), 5);",
		"}",
		"",
		"**Code to test:**",
		code,
		"",
		"Generate the unit test code as a valid .cpp file following the example format exactly.",
	}, "\n")

	log.Printf("Sending API request with prompt (%d bytes)", len(prompt))
	req := api.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		Options: map[string]interface{}{
			"num_ctx":     131072,
			"num_predict": 1024,
		},
	}

	for _, currentModel := range availableModels {
		req.Model = currentModel
		log.Printf("Trying model %s", currentModel)
		var result strings.Builder
		for attempt := 1; attempt <= 3; attempt++ {
			log.Printf("Attempt %d of 3 to generate unit tests with model %s", attempt, currentModel)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			result.Reset()
			err := client.Generate(ctx, &req, func(resp api.GenerateResponse) error {
				result.WriteString(resp.Response)
				return nil
			})
			if err != nil {
				log.Printf("Attempt %d failed with model %s: %v", attempt, currentModel, err)
				time.Sleep(time.Second)
				continue
			}
			output := result.String()
			output = strings.TrimPrefix(output, "```cpp\n")
			output = strings.TrimSuffix(output, "\n```")
			output = strings.TrimSpace(output)

			// Save raw response for debugging
			if err := os.WriteFile(fmt.Sprintf("raw_response_%s_attempt_%d.txt", currentModel, attempt), []byte(output), 0644); err != nil {
				log.Printf("Failed to save raw response: %v", err)
			}

			// Validate output
			if len(output) < 250 {
				log.Printf("Validation failed: Output too short (%d bytes)", len(output))
				continue
			}
			if !strings.Contains(output, "#include <gtest/gtest.h>") {
				log.Printf("Validation failed: Missing #include <gtest/gtest.h>")
				continue
			}
			if !strings.Contains(output, "#include <cmath>") {
				log.Printf("Validation failed: Missing #include <cmath>")
				continue
			}
			if !strings.Contains(output, "#include <stdexcept>") {
				log.Printf("Validation failed: Missing #include <stdexcept>")
				continue
			}
			if !strings.Contains(output, "#include \"example.h\"") {
				log.Printf("Validation failed: Missing #include \"example.h\"")
				continue
			}
			if !strings.Contains(output, "TEST") {
				log.Printf("Validation failed: Missing TEST macro")
				continue
			}
			re := regexp.MustCompile(`TEST\([^)]+\)\s*{[^}]*$`)
			if re.MatchString(output) {
				log.Printf("Validation failed: Incomplete TEST macro detected")
				continue
			}
			// Check for exactly 4 TEST cases

			testCount := len(regexp.MustCompile(`TEST\(CalculatorTest,`).FindAllString(output, -1))
			if testCount != 4 {
				log.Printf("Validation failed: Expected exactly 4 TEST cases, found %d", testCount)
				continue
			}
			missingMethods := []string{}
			for _, method := range methods {
				if !strings.Contains(output, method+"(") {
					missingMethods = append(missingMethods, method)
				}
			}
			if len(missingMethods) > 0 {
				log.Printf("Validation failed: Missing tests for methods: %v", missingMethods)
				continue
			}
			braceCount := 0
			for _, c := range output {
				if c == '{' {
					braceCount++
				} else if c == '}' {
					braceCount--
				}
			}
			if braceCount != 0 {
				log.Printf("Validation failed: Unbalanced braces (count: %d)", braceCount)
				continue
			}

			log.Printf("Successfully generated unit tests (%d bytes) with model %s", len(output), currentModel)
			return output, nil
		}
	}
	log.Printf("Failed to generate unit tests after 3 attempts with all models")
	return "", fmt.Errorf("failed after 3 attempts with all models")
}

func runTestsAndCoverage(testFile, sourceFile string) (bool, float64, error) {
	brewPrefix := "/opt/homebrew/opt/googletest"
	baseName := filepath.Base(sourceFile)

	// Create temp directory for test execution
	tempDir, err := os.MkdirTemp("", "unit-test-generator-")
	if err != nil {
		return false, 0.0, fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Write test file to temp directory
	tempTestFile := filepath.Join(tempDir, "test.cpp")
	if err := os.WriteFile(tempTestFile, []byte(testFile), 0644); err != nil {
		return false, 0.0, fmt.Errorf("failed to write test file: %v", err)
	}

	// Copy source file to temp directory
	tempSourceFile := filepath.Join(tempDir, baseName)
	if err := copyFile(sourceFile, tempSourceFile); err != nil {
		return false, 0.0, fmt.Errorf("failed to copy source file: %v", err)
	}

	// Copy header file if it exists
	if strings.HasSuffix(sourceFile, ".cpp") {
		headerFile := strings.Replace(sourceFile, ".cpp", ".h", 1)
		if _, err := os.Stat(headerFile); err == nil {
			if err := copyFile(headerFile, filepath.Join(tempDir, filepath.Base(headerFile))); err != nil {
				return false, 0.0, fmt.Errorf("failed to copy header file: %v", err)
			}
		}
	}

	// Compile tests
	compileCmd := exec.Command("g++",
		"-std=c++17",
		"-I"+brewPrefix+"/include",
		"-I/usr/local/include",
		"-I"+tempDir, // Include temp directory for headers
		"-L"+brewPrefix+"/lib",
		"-L/usr/local/lib",
		"-lgtest", "-lgtest_main", "-pthread",
		"-fprofile-arcs", "-ftest-coverage",
		filepath.Base(tempTestFile), filepath.Base(tempSourceFile),
		"-o", filepath.Join(tempDir, "run_tests"))
	compileCmd.Dir = tempDir
	compileCmd.Env = append(os.Environ(),
		"GCOV_PREFIX="+tempDir,
		"GCOV_PREFIX_STRIP=0")

	log.Printf("Compiling tests: %s", compileCmd.String())
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		return false, 0.0, fmt.Errorf("compilation failed: %v\nOutput: %s", err, string(compileOutput))
	}
	log.Println("Tests compiled successfully")

	// Run tests
	runCmd := exec.Command(filepath.Join(tempDir, "run_tests"))
	runCmd.Dir = tempDir
	log.Printf("Running tests: %s", runCmd.String())
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		return false, 0.0, fmt.Errorf("tests failed: %v\nOutput: %s", err, string(runOutput))
	}
	log.Println("Tests passed successfully")

	// Run gcov for coverage
	gcovCmd := exec.Command("gcov", "-r", baseName)
	gcovCmd.Dir = tempDir
	log.Printf("Running gcov: %s", gcovCmd.String())
	gcovOutput, err := gcovCmd.CombinedOutput()
	if err != nil {
		return true, 0.0, fmt.Errorf("gcov failed: %v\nOutput: %s", err, string(gcovOutput))
	}

	// Parse gcov output for coverage percentage
	re := regexp.MustCompile(`Lines executed:([\d.]+)% of (\d+)`)
	matches := re.FindStringSubmatch(string(gcovOutput))
	if len(matches) < 2 {
		return true, 0.0, fmt.Errorf("failed to parse gcov output: %s", string(gcovOutput))
	}
	coverage, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return true, 0.0, fmt.Errorf("failed to parse coverage percentage: %v", err)
	}
	log.Printf("Code coverage: %.2f%%", coverage)

	// Enforce minimum coverage threshold (80%)
	if coverage < 80.0 {
		return false, coverage, fmt.Errorf("coverage %.2f%% is below 80%% threshold", coverage)
	}

	return true, coverage, nil
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting unit test generator")

	// Initialize Ollama client
	ollamaURL := os.Getenv("OLLAMA_HOST")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
		log.Println("OLLAMA_HOST not set, using default:", ollamaURL)
	} else {
		log.Println("Using OLLAMA_HOST:", ollamaURL)
	}
	url, err := url.Parse(ollamaURL)
	if err != nil {
		log.Fatalf("Invalid Ollama URL %s: %v", ollamaURL, err)
	}
	client := api.NewClient(url, http.DefaultClient)
	log.Println("Ollama client initialized")

	// Check Ollama server status
	resp, err := client.List(context.Background())
	if err != nil {
		log.Fatalf("Failed to connect to Ollama server: %v", err)
	}
	log.Printf("Ollama server running, available models: %v", resp.Models)

	// Read codebase
	codebaseDir := "./codebase"
	files, err := readCodebase(codebaseDir)
	if err != nil {
		log.Fatalf("Failed to read codebase: %v", err)
	}

	// Create tests directory if it doesn't exist
	if err := os.MkdirAll("./tests", 0755); err != nil {
		log.Fatalf("Failed to create tests directory: %v", err)
	}
	log.Println("Tests directory ready: ./tests")

	// Generate, validate, and save unit tests for each file
	for filePath, content := range files {
		fmt.Printf("Generating unit tests for %s\n", filePath)
		log.Printf("Processing file: %s", filePath)

		// Skip files that don't have both .h and .cpp
		if strings.HasSuffix(filePath, ".h") {
			cppFile := strings.Replace(filePath, ".h", ".cpp", 1)
			if _, err := os.Stat(cppFile); os.IsNotExist(err) {
				log.Printf("Skipping %s: corresponding .cpp file not found", filePath)
				continue
			}
		} else if strings.HasSuffix(filePath, ".cpp") {
			hFile := strings.Replace(filePath, ".cpp", ".h", 1)
			if _, err := os.Stat(hFile); os.IsNotExist(err) {
				log.Printf("Skipping %s: corresponding .h file not found", filePath)
				continue
			}
		}

		tests, err := generateUnitTests(client, "qwen2.5-coder:7b", content)
		if err != nil {
			log.Printf("Failed to generate tests for %s: %v", filePath, err)
			continue
		}

		// Determine source file for testing
		sourceFile := filePath
		if strings.HasSuffix(filePath, ".h") {
			sourceFile = strings.Replace(filePath, ".h", ".cpp", 1)
		}

		// Run tests and check coverage
		passed, coverage, err := runTestsAndCoverage(tests, sourceFile)
		if err != nil {
			log.Printf("Test validation failed for %s: %v", filePath, err)
			continue
		}
		if !passed {
			log.Printf("Tests did not pass for %s, skipping file write", filePath)
			continue
		}

		// Save unit tests to a file if tests pass and coverage is sufficient
		baseName := filepath.Base(filePath)
		testFile := filepath.Join("./tests", strings.Replace(baseName, ".cpp", "_test.cpp", 1))
		if strings.HasSuffix(baseName, ".h") {
			testFile = filepath.Join("./tests", strings.Replace(baseName, ".h", "_test.cpp", 1))
		}
		log.Printf("Writing unit tests to %s (coverage: %.2f%%)", testFile, coverage)
		err = os.WriteFile(testFile, []byte(tests), 0644)
		if err != nil {
			log.Printf("Failed to write tests to %s: %v", testFile, err)
		} else {
			fmt.Printf("Unit tests saved to %s (coverage: %.2f%%)\n", testFile, coverage)
			log.Printf("Successfully saved unit tests to %s", testFile)
		}
	}
	log.Println("Unit test generation completed")
}
