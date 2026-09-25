// Syntax/erasure helper bundled with the Black formatter. The historical
// blackspike name is internal; this is not a language server or second checker.
// Reuse tython's parser rather than duplicate lambda/generic disambiguation.
package main

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

type request struct {
	Source string
}

// The first '>' after a type-parameter list is often the '>' inside '->'.
// That arrow is not the closer.
func markTypeParamClose(source string, from int, angles map[int]string) {
	for index := from; index < len(source); index++ {
		if source[index] == '>' && (index == 0 || source[index-1] != '-') {
			angles[index] = "close"
			return
		}
	}
}

func main() {
	var input request
	// JSON escaping can make a 1 MB source substantially larger on the wire.
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 8_000_000)).Decode(&input); err != nil {
		panic(err)
	}
	if len(input.Source) > 1_000_000 {
		panic("formatter source exceeds 1 MB")
	}
	decl, declErrors := python.ParseTypedSourceDeclarations("spike.ty", input.Source)
	runtime, runtimeErrors := python.ParseRuntimeFile("spike.ty", input.Source)
	erased, erasureErrors := python.EraseTypedPython(input.Source)
	errors := []string{}
	for _, e := range declErrors {
		errors = append(errors, e.Message)
	}
	for _, e := range runtimeErrors {
		errors = append(errors, e.Message)
	}
	for _, e := range erasureErrors {
		errors = append(errors, e.Message)
	}
	angles := map[int]string{}
	colons := map[int]bool{}
	unsupported := []string{}
	mark := func(start, end int, value byte, kind string) {
		if start < 0 || end > len(input.Source) || start > end {
			return
		}
		if i := strings.IndexByte(input.Source[start:end], value); i >= 0 {
			angles[start+i] = kind
		}
	}
	var inspect func(reflect.Value)
	inspect = func(value reflect.Value) {
		if !value.IsValid() || !value.CanInterface() {
			return
		}
		if value.Kind() == reflect.Interface {
			if !value.IsNil() {
				inspect(value.Elem())
			}
			return
		}
		if value.Kind() == reflect.Pointer && value.IsNil() {
			return
		}
		switch node := value.Interface().(type) {
		case *python.RuntimeLambdaExpr:
			if node.Signature != nil {
				for _, p := range node.Signature.Parameters {
					if p.Annotated && p.Type != nil {
						start, end := p.NameLoc.End, p.Type.Range().Start
						if start >= 0 && end >= start && end <= len(input.Source) {
							if i := strings.IndexByte(input.Source[start:end], ':'); i >= 0 {
								colons[start+i] = true
							}
						}
					}
				}
			}
		case *python.GenericSpecializationTypeExpr:
			mark(node.Target.Range().End, node.Range().End, '<', "open")
			mark(node.Range().End-1, node.Range().End, '>', "close")
		case *python.RuntimeCallExpr:
			if len(node.TypeArguments) > 0 {
				mark(node.Target.Range().End, node.TypeArguments[0].Range().Start, '<', "open")
				mark(node.TypeArguments[len(node.TypeArguments)-1].Range().End, node.Range().End, '>', "close")
			}
		case []python.TypeParameterExpr:
			if len(node) > 0 {
				start := node[0].NameLoc.Start
				if start >= 0 && start <= len(input.Source) {
					if i := strings.LastIndexByte(input.Source[:start], '<'); i >= 0 {
						angles[i] = "open"
					}
				}
				last := node[len(node)-1]
				end := last.NameLoc.End
				if last.Constraint != nil {
					end = last.Constraint.Range().End
				}
				if last.Default != nil {
					end = last.Default.Range().End
				}
				markTypeParamClose(input.Source, end, angles)
			}
		}
		switch value.Kind() {
		case reflect.Pointer:
			inspect(value.Elem())
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				inspect(value.Field(i))
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				inspect(value.Index(i))
			}
		}
	}
	inspect(reflect.ValueOf(decl))
	inspect(reflect.ValueOf(runtime))
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"declarations": normalize(reflect.ValueOf(decl)), "runtime": normalize(reflect.ValueOf(runtime)),
		"erased": erased, "errors": errors, "unsupported": unsupported, "angles": angles, "colons": colons,
	}); err != nil {
		panic(err)
	}
}

// Preserve node kinds and every semantic field; omit only source locations and
// file identity. This is a test oracle, not a new parser/type-checking algorithm.
func normalize(value reflect.Value) any {
	if !value.IsValid() || !value.CanInterface() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return normalize(value.Elem())
	case reflect.Struct:
		result := map[string]any{"$kind": value.Type().Name()}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if !field.IsExported() || field.Type == reflect.TypeFor[python.TextRange]() || field.Name == "FileName" {
				continue
			}
			result[field.Name] = normalize(value.Field(i))
		}
		return result
	case reflect.Slice:
		result := []any{}
		for i := 0; i < value.Len(); i++ {
			result = append(result, normalize(value.Index(i)))
		}
		return result
	default:
		return value.Interface()
	}
}
