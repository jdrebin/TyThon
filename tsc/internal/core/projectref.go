package core

// ResolutionRedirect is a project-reference config the checker consults
// while resolving modules. Tython's host returns nil.
type ResolutionRedirect interface {
	CommonSourceDirectory() string
	CompilerOptions() *CompilerOptions
}

// OutputProjectReference is a source file's declaration output in another project.
type OutputProjectReference interface {
	OutputDtsPath() string
	ResolvedCompilerOptions() *CompilerOptions
}
