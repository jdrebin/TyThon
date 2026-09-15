package python

// SemanticIdentifiers exposes the exact, named spans already recorded during
// binding/checking. It does not rescan Python or infer a second set of types.
func (p *PythonProgram) SemanticIdentifiers(fileName, source string) []SemanticHover {
	byRange := make(map[TextRange]SemanticHover)
	add := func(info SemanticHover) {
		if info.Kind == QuickInfoUnknown || info.Kind == QuickInfoItem || info.Name == "" || info.Type == nil {
			return
		}
		if info.Range.Start < 0 || info.Range.End > len(source) || info.Range.End <= info.Range.Start {
			return
		}
		if source[info.Range.Start:info.Range.End] != info.Name {
			return
		}
		if declared, ok := byRange[info.Range]; ok {
			info.Readonly = info.Readonly || declared.Readonly
			info.Async = info.Async || declared.Async
		}
		byRange[info.Range] = info
	}
	for _, module := range p.Modules {
		if declarationFileName(module.Files) == fileName {
			for _, info := range module.Types.hovers {
				add(info)
			}
		}
		if module.Runtime != nil && module.Runtime.File.FileName == fileName {
			for _, expression := range module.Runtime.Expressions {
				add(SemanticHover{Range: expression.Range, Kind: expression.Kind, Name: expression.Name, Type: expression.Type})
			}
		}
	}
	result := make([]SemanticHover, 0, len(byRange))
	for _, info := range byRange {
		result = append(result, info)
	}
	return result
}
