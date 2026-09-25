# VS Code extension demo

Open the repository root in VS Code. In **Run and Debug**, select
**Launch tython demo** and press **F5**. The pre-launch task builds
the compiler and extension without `hereby`, then opens
`python-typescript-demo.code-workspace` in an Extension Development Host.
The launch reuses the compiler and formatter when current, otherwise rebuilds
them, then builds the small extension bundle. The formatter includes its own
Python runtime; the first preparation installs pinned build dependencies.

To test formatting, open `formatting.ty` and run **Format Document**
(`Shift+Alt+F`). It is deliberately unformatted. If another formatter is selected,
choose **Format Document With… → tython**. Undo the edit to try again.

To perform the same steps manually, build the compiler and extension from the
repository root:

```bash
npm run -w tython demo:prepare
```

Then launch an Extension Development Host with this directory already open:

```bash
code --extensionDevelopmentPath="$PWD/packages/vscode-tython" "$PWD/vscode-extension-demo/python-typescript-demo.code-workspace"
```

In the new window:

1. Open `syntax_highlighting.ty` to inspect ordinary Python coloring alongside
   type functions, interfaces, declaration modifiers, collection shorthand,
   generic parameters, and generic specializations.
2. Open `completion.ty`, place the cursor after `user.na` in `user.name`, and
   press **Ctrl+Space**. The checker should offer `name: str`. It must not offer
   the indexed-only `"item-only"` key as a dot attribute. This also works after
   unsaved edits.
3. Open `completion.ty` and hover over `User`, `format_name`, its parameters,
   `user.name`, and the keys in the final dictionary. Quick Info should include
   declaration kinds, names, inferred signatures, and checked types.
4. Open `type_error.ty`. The `identity<int>("Grace")` call should have a red
   diagnostic because its argument is not an `int`.
5. Change `int` to `str`. The diagnostic should disappear through the standard
   language-client diagnostic refresh.

## Advanced feature lab

The `advanced_*.ty` files are intended to type-check without errors. Together
they exercise the current implementation beyond the small original demos:

- `advanced_types.ty`: type functions, generics, defaults and constraints,
  conditional types, nominal `*<Name>` attribute keys, mapped types,
  `Extract`, `Exclude`, `keyof`, indexed access, and generic call inference.
- `advanced_calls.ty`: overload declarations, generic callables,
  positional-only and keyword-only parameters, defaults, `*args`, `**kwargs`,
  lambdas, named arguments, completion, and overload selection.
- `advanced_objects.ty`: deliberately different attribute and item surfaces,
  pure quoted-key type shapes versus runtime dictionary identity, arbitrary
  index signatures, methods, dictionary-literal inference, mapping methods,
  and attribute/item completion.
- `advanced_control_flow.ty`: `None` narrowing, class and mapping patterns,
  assignment expressions, loops with `else`, and return-flow analysis.
- `advanced_collections.ty`: fixed and variadic list/tuple syntax,
  destructuring, starred values, comprehensions, generator expressions, sets,
  and dictionary spreading.
- `advanced_async_and_generators.ty`: coroutines, awaitables, async generators,
  async comprehensions, sync/async context managers, and generator
  yield/send/return channels.
- `advanced_classes.ty`: inheritance, projected bases, constructors, bound
  methods, properties and setters, class methods, static methods, inferred
  class attributes, and the implicit universal `object` surface.
- `advanced_protocols.ty`: operator, iterator, membership, and mutable item
  protocols.
- `advanced_exceptions.ty`: `try`/`except`/`else`/`finally`, typed exception
  bindings, `never`, and chained raises.
- `advanced_modules.ty` and `sample_package/models.{py,d.ty}`: a plain Python
  implementation paired with a declaration file, absolute and aliased
  imports, module objects, constructors, properties, and bound methods.

Hover declaration names, inferred bindings, parameters, properties, and item
keys throughout these files. For completion, delete part of an attribute,
quoted item key, type name, or keyword argument and press **Ctrl+Space**. For
signature help, place the cursor within any call.

`diagnostic_gallery.ty` contains intentional type errors covering generic
arguments, mismatched attributes and items, unknown members, Python parameter
categories, function returns, and generator yields. Every expected error is
marked in the source.

The `frontier_*.ty` files isolate implementation boundaries that have needed
focused coverage. Recursive structural aliases, deferred generic indexed
access, and contextual collection arguments now run through checker-owned
instantiation and contextual typing. The remaining files continue to provide a
live implementation backlog rather than clean examples.

Native type diagnostics, Quick Info, typed completion and signature help use
the repository's existing LSP transport. The Python providers below supplement
that service rather than replacing its checker.

## Python ecosystem tools

Jedi and Ruff now supplement the native `.ty` checker. In the repository root:

```sh
npm run tools:prepare --workspace packages/vscode-tython
npm run demo:prepare --workspace packages/vscode-tython
```

Restart the extension development host, then open `python_tools.ty` for library
docs, import completions, fallback definitions, Ruff warnings/quick fixes, and
Format Document. Open `python_tools_typed.ty` for linting beside type syntax and
safe Format Selection on a complete top-level, untyped Python statement.
Full-document formatting of typed syntax is not supported yet.

The Python extension's selected interpreter controls library discovery when it
is available. Override with `pythonTypeScript.tools.interpreterPath`; use
`pythonTypeScript.tools.pythonPath` for a different interpreter containing Jedi
and Ruff. Tool failures appear in the **tython** output channel.
Tools require workspace trust and never replace native `.ty` type checking.
