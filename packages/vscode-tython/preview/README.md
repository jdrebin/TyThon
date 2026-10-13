# TyThon playground

A collection of small, independent programs to read, run, and change. Each
numbered file has a purpose, examples to hover, and suggestions for experiments.
Start with `01_shapes.ty`, or jump to a topic that interests you. The numbered
examples should all type-check and run; `negative/` contains intentional errors.

## Get a copy

With Python 3.10+ and `tython-lang` installed:

```sh
tython demo my-playground
cd my-playground
python tour.py
```

Omit the directory to create `tython-demo`. The destination must not already
exist, even if it is empty. Generation works offline and only copies files;
it does not install dependencies, build code, or run it.

In VS Code, **tython: Open Preview Examples** creates the same playground.
Install the CLI separately to run the commands below. The extension can check
and explore these files without the CLI. Open this folder and trust the workspace
to enable the extension.

## Run and experiment

```sh
python tour.py --check             # Check all positive lessons
python tour.py                     # Build them into dist/ and run each one
tython check 07_functions.ty       # Work on one file
tython build 07_functions.ty
python dist/07_functions.py
tython check negative/expected_errors.ty  # Deliberately fails with four errors
```

`tour.py` uses the CLI installed in that Python environment. It works from any
working directory and avoids shell-specific wildcards. Builds overwrite generated
files under `dist/`. Edit the `.ty` sources, then rebuild. No third-party libraries
are needed for the tour; the async lesson uses Python's standard `asyncio`.

TyThon emits `.ty` implementations and their typed imports. It does not copy
ordinary Python dependencies. The runner explicitly copies `catalog.py` and
`guards.py` into `dist/` for lessons 04 and 17. When building either lesson by
hand, copy those runtime dependencies too. Their neighboring `.d.ty` files provide
types and never create implementations. `asyncio.d.ty` describes only the one
standard-library function this tour uses.

## Feature map

| File | What to explore |
| --- | --- |
| `01_shapes.ty` | Inferred dictionaries, exact item shapes, `Dict`, methods, optional keys, open maps |
| `02_types.ty` | Type functions, filtering, generic inference, callbacks, `as const`, `satisfies` |
| `03_classes.ty` | Inferred attributes, bound methods, callable instances and callable interfaces |
| `04_imports.ty` | Python plus `.d.ty`, typed packages, relative and aliased imports, erased type imports |
| `05_type_transformations.ty` | `keyof`, indexed access, mapped keys and filters, optional modifiers, f-string types, conditional distribution, `infer`, recursion |
| `06_attributes_and_items.ty` | Attribute/item separation, nominal `*<"name">` keys, dot type access, open and patterned contracts, arbitrary key types |
| `07_functions.ty` | Generics, constraints/defaults, `const` parameters, overloads, positional-only/keyword-only calls, defaults, `*args`/`**kwargs`, typed/generic lambdas |
| `08_collections.ty` | Fixed, homogeneous, and variadic lists/tuples, unpacking, comprehensions, sets, generators, dictionary spreads |
| `09_presence_and_narrowing.ty` | Optional members versus `None`, membership/`hasattr` guards, assignment/deletion, presence assertions (`!`) |
| `10_classes_and_descriptors.ty` | Structural interfaces, inheritance, required initialization, initializer assertions, `super`, properties, class/static methods, descriptors |
| `11_decorators.ty` | Generic preservation, wrapping, factories, stacking, replacing a function with a value |
| `12_protocols.ty` | Arithmetic/reflected/in-place operators, iterators, membership, mutable item protocols |
| `13_async_and_generators.ty` | Awaitables, `await`, async iteration/comprehensions/context managers, yield/send/return channels, `yield from` |
| `14_control_flow.ty` | Loop `else`, mapping patterns, `never`, narrowing, context managers, exception bindings/chaining, `try`/`else`/`finally`, walrus expressions |
| `15_values_and_assertions.ty` | Primitive families, `Some`/`Object`/`object`, intersections, primitive subclasses, readonly and literal assertions |
| `16_editor_playground.ty` | Literal/key/attribute completion, hover, signature help, navigation, rename, formatting, live diagnostics |
| `17_validation_contracts.ty` | Declared type predicates/assertions backed by real validators, `assert` narrowing, closures and `nonlocal` |
| `negative/expected_errors.ty` | Four intentional errors: incompatible shape, unknown key, bad default, incompatible assignment |

`type Utility(T)` uses parentheses; generic functions, classes, and interfaces
use angle brackets. `{name: str}` means an attribute, while `{"name": str}` means
an indexed item. `Dict(Shape)` adds dictionary operations; there is no `dict<K, V>`
type API. Types erase rather than validating, converting, or freezing values.

## Editor experiments

1. Hover inferred bindings and derived types. Follow imported definitions in
   lesson 04 and inspect the package files.
2. In lesson 16, delete the text inside `mode="write"` and invoke completion.
   Try a dictionary key or an attribute after a dot too.
3. Invoke signature help inside a call in lesson 07; try an invalid keyword or
   argument type and undo it after reading the diagnostic.
4. Run **Format Document** on lesson 16. It is deliberately cramped. Undo to
   repeat the experiment. Use **Rename Symbol** on a local binding.
5. Visit `negative/expected_errors.ty` and repair its four marked mistakes.

Optional Jedi/Ruff integration requires those Python tools and a selected
interpreter. It supplies Python documentation/navigation and lint fixes alongside
native typing. **tython: Import Python Declarations** can generate a contract from
an annotated module in that environment; `catalog.py` is a small local example.
Formatting uses the extension's bundled formatter and your Python interpreter.

## Preview boundaries

This tour covers the public feature families, not every Python library or checker
edge case. Exact-key dictionaries can still fail assignment to wider dictionary
contracts; a dunder implementation alone does not declare arbitrary item keys.
Lessons 06 and 12 explicitly describe the item contracts their runtime objects
implement. Annotations and declaration assertions are promises, not validation.
Plain `.py` files are not checked by TyThon.

For syntax and current limitations, see the
[project guide](https://github.com/jdrebin/TyThon#language-guide).
