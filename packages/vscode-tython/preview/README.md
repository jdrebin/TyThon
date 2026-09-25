# tython preview workspace

Open the numbered `.ty` files in order. Try hovering declarations and calls,
completing dictionary keys and attributes, and navigating imported functions.
The root examples should have no type errors. `negative/expected_errors.ty`
intentionally exercises diagnostics.

- `01_shapes.ty`: exact dictionaries, methods, optional presence, collections.
- `02_types.ty`: type utilities, mapping, generics, callbacks and assertions.
- `03_classes.ty`: inferred attributes, bound methods and callable instances.
- `04_imports.ty`: plain Python with a neighboring declaration file.

`Dict(Shape)` composes a shape with dictionary methods. Utilities declared with
`type` use parentheses; generic functions/classes/interfaces use angle brackets.
There is no `dict<K, V>` API. Open maps use a shape such as `{ (str): int }`.

Known limitation: the current strict key/method contracts can reject assigning
an exact-key dictionary to an open-map type. The preview does not silently
widen dictionaries or bypass type checking to accept that assignment.

To report a problem, include the extension version, OS/WSL environment, the
smallest `.ty` / `.d.ty` example, expected behavior, actual diagnostics or hover,
and relevant output from **tython Language Server**. Redact
private paths/data. Never include credentials or entire heap profiles publicly.
