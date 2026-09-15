package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
)

func isPythonInput(fileName string) bool {
	return pythonfrontend.GetFileKind(fileName) != pythonfrontend.FileKindUnknown
}

func runPython(args []string) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, "usage: tsgo --python [--emit] [--type-at=BYTE_OFFSET] [--stdin-file=PATH] <module.py|module.ty|module.d.ty> [...]")
		return 2
	}
	emit := false
	typeAt := -1
	stdinFile := ""
	files := make([]string, 0, len(args))
	for _, argument := range args {
		if argument == "--emit" {
			emit = true
			continue
		}
		if strings.HasPrefix(argument, "--type-at=") {
			value, err := strconv.Atoi(strings.TrimPrefix(argument, "--type-at="))
			if err != nil || value < 0 {
				fmt.Fprintln(os.Stderr, "--type-at requires a non-negative byte offset")
				return 2
			}
			typeAt = value
			continue
		}
		if strings.HasPrefix(argument, "--stdin-file=") {
			stdinFile = strings.TrimPrefix(argument, "--stdin-file=")
			continue
		}
		files = append(files, argument)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no Python input files")
		return 2
	}
	inputs, err := collectPythonInputs(files)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if stdinFile != "" {
		absolute, err := filepath.Abs(stdinFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		found := false
		for index := range inputs {
			if inputs[index].FileName == absolute {
				inputs[index].Text = string(contents)
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "stdin overlay file is not an input: %s\n", stdinFile)
			return 2
		}
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, inputs)
	texts := make(map[string]string, len(inputs))
	for _, input := range inputs {
		texts[input.FileName] = input.Text
	}
	for _, diagnostic := range program.Diagnostics {
		line, column := sourceLineAndColumn(texts[diagnostic.FileName], diagnostic.Range.Start)
		fmt.Fprintf(os.Stderr, "%s:%d:%d: error: %s\n", diagnostic.FileName, line, column, pythonfrontend.FormatDiagnosticMessage(diagnostic.Message))
	}
	if typeAt >= 0 {
		target, err := filepath.Abs(files[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if hover, ok := program.HoverAt(target, typeAt); ok {
			fmt.Fprintln(os.Stdout, hover)
			return 0
		}
		return 1
	}
	if len(program.Diagnostics) != 0 {
		return 1
	}
	if emit {
		for _, module := range program.Modules {
			if module.Files.TypedImplementation == "" {
				continue
			}
			output, diagnostics := pythonfrontend.EraseTypedPython(module.TypedSource)
			for _, diagnostic := range diagnostics {
				line, column := sourceLineAndColumn(module.TypedSource, diagnostic.Range.Start)
				fmt.Fprintf(os.Stderr, "%s:%d:%d: error: %s\n", module.Files.TypedImplementation, line, column, diagnostic.Message)
			}
			if len(diagnostics) != 0 {
				return 1
			}
			outputFile, _ := pythonfrontend.OutputFileName(module.Files.TypedImplementation)
			if err := os.WriteFile(outputFile, []byte(output), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "write %s: %v\n", outputFile, err)
				return 1
			}
			fmt.Fprintf(os.Stdout, "Emitted %s\n", outputFile)
		}
	}
	fmt.Fprintf(os.Stdout, "Checked %d Python module(s) with no errors.\n", len(program.Modules))
	return 0
}

func collectPythonInputs(args []string) ([]pythonfrontend.SourceInput, error) {
	entries := []string{}
	roots := []string{}
	for _, argument := range args {
		absolute, err := filepath.Abs(argument)
		if err != nil {
			return nil, err
		}
		if pythonfrontend.GetFileKind(absolute) == pythonfrontend.FileKindUnknown {
			return nil, fmt.Errorf("unsupported Python input extension: %s", argument)
		}
		if _, err := os.Stat(absolute); err != nil {
			return nil, err
		}
		entries = append(entries, absolute)
		roots = append(roots, filepath.Dir(absolute))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	var readError error
	files := pythonfrontend.CollectProjectSources(context.Background(), entries, roots, nil, func(name string) (string, bool) {
		contents, err := os.ReadFile(name)
		if err != nil && !os.IsNotExist(err) && readError == nil {
			readError = fmt.Errorf("read %s: %w", name, err)
		}
		return string(contents), err == nil
	})
	if readError != nil {
		return nil, readError
	}
	fileNames := make([]string, 0, len(files))
	for fileName := range files {
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)
	inputs := make([]pythonfrontend.SourceInput, 0, len(fileNames))
	for _, fileName := range fileNames {
		inputs = append(inputs, pythonfrontend.SourceInput{FileName: fileName, Text: files[fileName]})
	}
	return inputs, nil
}

func sourceLineAndColumn(source string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line := 1 + strings.Count(source[:offset], "\n")
	lastNewline := strings.LastIndex(source[:offset], "\n")
	return line, offset - lastNewline
}
