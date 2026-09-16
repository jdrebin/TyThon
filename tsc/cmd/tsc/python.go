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

	"github.com/microsoft/TypeScript/tsc/internal/outputpaths"
	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

func isPythonInput(fileName string) bool {
	return pythonfrontend.GetFileKind(fileName) != pythonfrontend.FileKindUnknown
}

func runPython(args []string) int {
	if len(args) == 0 || len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, "usage: tsgo --python [--emit] [--out-dir=dist] [--root-dir=.] [--type-at=BYTE_OFFSET] [--stdin-file=PATH] <module.py|module.ty|module.d.ty> [...]")
		return 2
	}
	emit := false
	typeAt := -1
	stdinFile := ""
	rootDir, outDir := ".", "dist"
	files := make([]string, 0, len(args))
	for _, argument := range args {
		if argument == "--emit" {
			emit = true
			continue
		}
		if strings.HasPrefix(argument, "--out-dir=") {
			outDir = strings.TrimPrefix(argument, "--out-dir=")
			continue
		}
		if strings.HasPrefix(argument, "--root-dir=") {
			rootDir = strings.TrimPrefix(argument, "--root-dir=")
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
	if rootDir == "" || outDir == "" {
		fmt.Fprintln(os.Stderr, "--root-dir and --out-dir must not be empty")
		return 2
	}
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	inputs, err := collectPythonInputsAtRoot(files, rootDir)
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
		if err := emitPythonProgram(program, inputs, rootDir, outDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Fprintf(os.Stdout, "Checked %d Python module(s) with no errors.\n", len(program.Modules))
	return 0
}

func collectPythonInputs(args []string) ([]pythonfrontend.SourceInput, error) {
	return collectPythonInputsAtRoot(args, "")
}

func collectPythonInputsAtRoot(args []string, rootDir string) ([]pythonfrontend.SourceInput, error) {
	entries := []string{}
	roots := []string{}
	if rootDir != "" {
		roots = append(roots, rootDir)
	}
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

// Keep Python erasure in the frontend and path relocation in the existing TS
// output mapper. Only filesystem validation and copying ordinary .py modules
// belong here; neither import expressions nor runtime behavior are rewritten.
func emitPythonProgram(program *pythonfrontend.PythonProgram, inputs []pythonfrontend.SourceInput, rootDir, outDir string) error {
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if root == out {
		return fmt.Errorf("output directory must be separate from the source root")
	}
	inside := func(dir, file string) bool {
		rel, err := filepath.Rel(dir, file)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	inputFiles := make([]os.FileInfo, 0, len(inputs))
	for _, input := range inputs {
		if !inside(root, input.FileName) {
			return fmt.Errorf("source %s is outside --root-dir %s", input.FileName, root)
		}
		if inside(out, input.FileName) {
			return fmt.Errorf("source %s is inside the output directory", input.FileName)
		}
		info, err := os.Stat(input.FileName)
		if err != nil {
			return err
		}
		inputFiles = append(inputFiles, info)
	}
	type outputFile struct{ name, text string }
	outputs := []outputFile{}
	for _, module := range program.Modules {
		source, text := module.Files.Implementation, module.Implementation
		if module.Files.TypedImplementation != "" {
			source = module.Files.TypedImplementation
			erased, diagnostics := pythonfrontend.EraseTypedPython(module.TypedSource)
			if len(diagnostics) != 0 {
				return fmt.Errorf("cannot emit %s: %s", source, diagnostics[0].Message)
			}
			text = erased
		}
		if source == "" { // Declaration-only modules have no runtime output.
			continue
		}
		name := filepath.FromSlash(outputpaths.GetSourceFilePathInNewDir(filepath.ToSlash(source), filepath.ToSlash(out), filepath.ToSlash(root), tspath.EnsureTrailingDirectorySeparator(filepath.ToSlash(root)), true))
		if module.Files.TypedImplementation != "" {
			name, _ = pythonfrontend.OutputFileName(name)
		}
		if !inside(out, name) {
			return fmt.Errorf("output %s escapes the output directory", name)
		}
		// Do not follow output symlinks into source/user files, or overwrite a
		// source through a hard link. Validate every target before writing any.
		for part := name; ; part = filepath.Dir(part) {
			info, err := os.Lstat(part)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("output path contains a symbolic link: %s", part)
			}
			if part == filepath.Dir(part) {
				break
			}
		}
		if info, err := os.Stat(name); err == nil {
			for _, input := range inputFiles {
				if os.SameFile(info, input) {
					return fmt.Errorf("output would overwrite a source file: %s", name)
				}
			}
		}
		outputs = append(outputs, outputFile{name, text})
	}
	for _, output := range outputs {
		if err := os.MkdirAll(filepath.Dir(output.name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output.name, []byte(output.text), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Emitted %s\n", output.name)
	}
	return nil
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
