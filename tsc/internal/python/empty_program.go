package python

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/module"
	"github.com/microsoft/TypeScript/tsc/internal/packagejson"
	"github.com/microsoft/TypeScript/tsc/internal/symlinks"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// emptyProgram is the checker host. Tython never parses TypeScript, so the
// host has no source files and does not resolve TypeScript modules.
type emptyProgram struct {
	options core.CompilerOptions
}

func newEmptyProgram() *emptyProgram {
	return &emptyProgram{}
}

func (p *emptyProgram) Options() *core.CompilerOptions { return &p.options }
func (p *emptyProgram) SourceFiles() []*ast.SourceFile { return nil }
func (p *emptyProgram) BindSourceFiles()               {}
func (p *emptyProgram) FileExists(string) bool         { return false }
func (p *emptyProgram) GetSourceFile(string) *ast.SourceFile {
	return nil
}
func (p *emptyProgram) GetSourceFileForResolvedModule(string) *ast.SourceFile {
	return nil
}
func (p *emptyProgram) GetEmitModuleFormatOfFile(ast.HasFileName) core.ModuleKind {
	return core.ModuleKindNone
}
func (p *emptyProgram) GetEmitSyntaxForUsageLocation(ast.HasFileName, *ast.StringLiteralLike) core.ResolutionMode {
	return core.ResolutionModeNone
}
func (p *emptyProgram) GetImpliedNodeFormatForEmit(ast.HasFileName) core.ModuleKind {
	return core.ModuleKindNone
}
func (p *emptyProgram) GetResolvedModule(ast.HasFileName, string, core.ResolutionMode) *module.ResolvedModule {
	return nil
}
func (p *emptyProgram) GetResolvedModules() map[tspath.Path]module.ModeAwareCache[*module.ResolvedModule] {
	return nil
}
func (p *emptyProgram) GetPackagesMap() map[string]bool { return nil }
func (p *emptyProgram) GetSourceFileMetaData(tspath.Path) ast.SourceFileMetaData {
	return ast.SourceFileMetaData{}
}
func (p *emptyProgram) GetJSXRuntimeImportSpecifier(tspath.Path) (string, *ast.Node) {
	return "", nil
}
func (p *emptyProgram) GetImportHelpersImportSpecifier(tspath.Path) *ast.Node { return nil }
func (p *emptyProgram) SourceFileMayBeEmitted(*ast.SourceFile, bool) bool     { return false }
func (p *emptyProgram) IsSourceFileDefaultLibrary(tspath.Path) bool           { return false }
func (p *emptyProgram) GetProjectReferenceFromOutputDts(tspath.Path) core.OutputProjectReference {
	return nil
}
func (p *emptyProgram) GetRedirectForResolution(ast.HasFileName) core.ResolutionRedirect {
	return nil
}
func (p *emptyProgram) CommonSourceDirectory() string              { return "" }
func (p *emptyProgram) GetSymlinkCache() *symlinks.KnownSymlinks   { return nil }
func (p *emptyProgram) ContentMapperExtensions() []string          { return nil }
func (p *emptyProgram) GetGlobalTypingsCacheLocation() string      { return "" }
func (p *emptyProgram) UseCaseSensitiveFileNames() bool            { return true }
func (p *emptyProgram) GetCurrentDirectory() string                { return "" }
func (p *emptyProgram) GetProjectReferenceFromSource(tspath.Path) core.OutputProjectReference {
	return nil
}
func (p *emptyProgram) GetRedirectTargets(tspath.Path) []string { return nil }
func (p *emptyProgram) GetSourceOfProjectReferenceIfOutputIncluded(ast.HasFileName) string {
	return ""
}
func (p *emptyProgram) GetNearestAncestorDirectoryWithPackageJson(string) string { return "" }
func (p *emptyProgram) GetPackageJsonInfo(string) *packagejson.InfoCacheEntry    { return nil }
func (p *emptyProgram) GetDefaultResolutionModeForFile(ast.HasFileName) core.ResolutionMode {
	return core.ResolutionModeNone
}
func (p *emptyProgram) GetResolvedModuleFromModuleSpecifier(ast.HasFileName, *ast.StringLiteralLike) *module.ResolvedModule {
	return nil
}
func (p *emptyProgram) GetModeForUsageLocation(ast.HasFileName, *ast.StringLiteralLike) core.ResolutionMode {
	return core.ResolutionModeNone
}
