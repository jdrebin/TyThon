<!-- Modified for tython: Python type-system adaptation and independent project integration. -->

# tython

### TypeScript's type system. Python's way of speaking.

Write Python with expressive, structural types. Infer the shape of a dictionary,
derive one type from another, and let a function's inputs determine its output
type—all using the adapted TypeScript checker, not a new inference engine.

**Python at runtime. Types at development time.** Typed `.ty` files erase to
ordinary `.py` files. No replacement runtime, injected validation, or dispatch
machinery.

[Try it](#try-it) · [Language guide](#language-guide) · [Library possibilities](#what-this-could-unlock-for-libraries) · [Current limitations](#current-limitations) · [Help shape tython](#help-shape-tython)

> **Development preview.** The checker and editor integration are working, but
> Python/library coverage and tooling are still being completed. The verified
> packaging target is Linux x64, including VS Code running in WSL. This is not
> yet a claim of production readiness or complete Python compatibility.

## A little input. A precise result.

<!-- ty-example: introduction -->
```python
def wrap<T>(data: T):
    return {"data": data, "ok": True}

response = wrap({"id": 12, "name": "Ada", "active": True})

name = response["data"]["name"]      # str
active = response["data"]["active"]  # bool
missing = response["data"]["email"]  # error: "email" is not a key of the payload
```

You don't write a response type for every payload. The same function preserves
the relationship between **the data you pass** and **the data you get back**.
The dictionary's shape and the function's return type are inferred; annotating
every literal is unnecessary.

`T` represents the input type. Call `wrap` with a different shape and the result
changes with it. The type parameter and annotation disappear when the code is
emitted as Python.

Examples in this guide use `.ty` syntax. Markdown uses Python highlighting as a
fallback; it won't color all of tython's type syntax correctly. See
[the highlighting note](#a-note-on-syntax-highlighting).

## Try it

### From source

Use the Node/npm versions specified in [package.json](package.json), Go 1.26,
and VS Code. The supported preview environment is Linux x64/WSL with working
systemd/cgroup support; the language server uses memory containment.

```sh
git clone https://github.com/jdrebin/TyThon.git
cd TyThon
npm ci
npm run -w tython demo:prepare

code --new-window \
  --extensionDevelopmentPath="$PWD/packages/vscode-python-typescript" \
  "$PWD/packages/vscode-python-typescript/preview"
```

In the new Extension Development Host window:

1. Trust the example workspace so the extension can start.
2. Open `01_shapes.ty` and hover over variables and types.
3. Try completing a dictionary key inside quotes, or change a value to the wrong type.
4. Continue through `02_types.ty`, `03_classes.ty`, and `04_imports.ty`.

The examples include intentional errors in a separate `negative/` folder.
You can experiment with the introductory example above in a new `.ty` file;
its final line is deliberately invalid.

### From a VSIX

If you have a packaged tython `.vsix`, use **Extensions: Install from VSIX…**,
then **tython: Open Preview Examples**. The compiler and declarations are bundled;
native typing features do not require Go, Node, or this checkout.

See the [extension guide](packages/vscode-python-typescript/README.md) for optional
Jedi documentation/navigation, Ruff linting/formatting, interpreter selection,
memory limits, and installation details. Those integrations complement the
tython checker; they do not replace it.

### Check and emit Python

From a source checkout, after building, create a `demo.ty` containing valid code:

```sh
./built/local/tsc --python demo.ty
./built/local/tsc --python --emit demo.ty
python3 demo.py
```

`--emit` writes the sibling `demo.py` after checking. **It can overwrite that
file**; don't use an existing, separately maintained `.py` file as an output target.
The `tsc` binary name is currently an internal compatibility detail.

## Language guide

If you know TypeScript, the type machinery should feel familiar. If you know
Python, the expressions and runtime behavior should feel familiar.

This guide documents tython's spelling and intentional differences. For the
underlying concepts, use the [TypeScript handbook](https://www.typescriptlang.org/docs/handbook/intro.html):
[generics](https://www.typescriptlang.org/docs/handbook/2/generics.html),
[narrowing](https://www.typescriptlang.org/docs/handbook/2/narrowing.html),
[conditional types](https://www.typescriptlang.org/docs/handbook/2/conditional-types.html),
and [mapped types](https://www.typescriptlang.org/docs/handbook/2/mapped-types.html).
TypeScript's JavaScript runtime, module configuration, and library declarations
are not tython's Python runtime or configuration.

Each section compares the spellings directly. **No exact equivalent** means a
real Python/JavaScript difference, not missing punctuation. Numeric TS examples
use `number` where convenient; that does not make it equivalent to Python `int`.

**Jump to:** [Shapes](#2-shapes-distinguish-attributes-from-keys) ·
[Keys](#3-one-keyof-two-kinds-of-keys) ·
[Generics](#4-type-functions-and-generics) ·
[Transformations](#5-type-transformations-read-like-python) ·
[Collections](#6-lists-and-tuples-keep-their-python-identities) ·
[Functions](#7-functions-remain-python-functions) ·
[Lambdas](#lambdas) ·
[Optional members](#8-missing-is-not-none) ·
[Classes and values](#9-python-values-structural-classes) ·
[Inheritance](#inheritance) ·
[Assertions and imports](#10-assertions-readonly-and-imports) ·
[Python protocols](#11-python-specific-protocols)

### 1. Types erase; Python stays Python

| tython | TypeScript | Purpose |
| --- | --- | --- |
| `module.ty` | `module.ts` | Implementation with static types |
| `module.d.ty` | `module.d.ts` | Declarations describing a module |
| `module.py` | `module.js` | Runtime source / emitted output |

Type aliases, interfaces, generics and assertions are static. They don't create
classes, validate JSON, convert values, or choose runtime overloads. `.d.ty`
files can describe existing `.py` implementations without editing the library.

Typed annotations in `.ty` are erased too. Don't assume an annotation-driven
framework will see them in Python's `__annotations__`; its runtime schema or
metadata still needs a real runtime representation.

**Python-specific:** erasure preserves Python, not JavaScript. Runtime imports,
decorators, indentation and operators keep Python's behavior. Unlike TS features
that generate runtime code, tython's added type syntax is erasure-only.

### 2. Shapes distinguish attributes from keys

In Python, `value.id` and `value["id"]` are different operations. The type syntax
makes that distinction explicit:

| tython | TypeScript | Difference |
| --- | --- | --- |
| `{ id: int }` | `{ id: number }` | Attribute contract in tython; ordinary property in TS |
| `{ "id": str }` | `{ "id": string }` | Indexed-item contract in tython; the same property namespace in TS |
| `T.id` in a type | `T["id"]` | tython can look up a known attribute with a dot |
| `T["id"]` | `T["id"]` | Indexed access; tython excludes the separate `id` attribute |
| `{ (str): int }` | `{ [key: string]: number }` | Open item index signature |
| `{ (bytes): str }` | No direct equivalent | Python item-key types can go beyond JS property-key types |

<!-- ty-example: shapes -->
```python
type Example = {
    id: int,      # attribute: value.id
    "id": str,    # indexed item: value["id"]
}

type AttributeValue = Example.id        # int
type IndexedValue = Example["id"]       # str
type AttributeAgain = Example[*<"id">]  # int
```

An unquoted identifier declares an attribute. A literal declares an item key.
Other key types go in parentheses: `{ (str): int }` is an open string-keyed shape.
Index signatures must agree with overlapping explicitly declared members.

A shape is structural: `{ "id": int }` describes indexed access, **not necessarily
a dictionary**. A dictionary literal is both its inferred shape and a dictionary
protocol. Use `Dict(Shape)` when you also need dictionary methods:

| tython | TypeScript |
| --- | --- |
| `{ "id": int }` describes an item shape | `{ id: number }` describes an object property shape |
| `Dict({ "id": int })` adds dictionary methods | No direct equivalent: JS plain objects and `Map` have different APIs |
| `Dict({ (str): int })` describes an open dictionary | `Record<string, number>` is the closest object-map shape, not a Python dict |

<!-- ty-example: dictionaries -->
```python
type User = {"id": int, "name": str}

def name_of(user: User):
    return user["name"]

def maybe_name(user: Dict(User)):
    return user.get("name")

user = {"id": 12, "name": "Ada"}
name = name_of(user)         # str
possible = maybe_name(user)  # str | None
```

`Dict(Shape)` is a library type utility, not a magic `dict<Key, Value>` primitive.
An open dictionary is written `Dict({ (str): int })`. Plain Python dictionary
literals still use Python's normal rules: `{"id": 12}`, not `{id: 12}` unless
`id` is an actual variable whose value you want to use as the key.

### 3. One `keyof`, two kinds of keys

`*<"id">` represents the attribute name `id`; `"id"` represents the string item
key. They are distinct types. Bare `*` means `*<str>`.

| tython | TypeScript |
| --- | --- |
| `keyof { label: str, "id": int }` → `*<"label"> \| "id"` | `keyof { label: string; id: number }` → `"label" \| "id"` |
| `Extract(keyof T, *)` selects attribute keys | No equivalent namespace to select |
| `Exclude(keyof T, *)` selects item keys | Ordinary `keyof` already covers JS properties |
| `{ *: str }` | `{ [name: string]: string }` is the closest spelling, but does not separate attributes/items |
| `{ (*<f"get_{str}">): int }` | ``{ [name: `get_${string}`]: number }`` is the patterned-property analogue |

<!-- ty-example: keys -->
```python
type Mixed = { label: str, "id": int }

type EveryKey = keyof Mixed               # *<"label"> | "id"
type Attributes = Extract(keyof Mixed, *)  # *<"label">
type Items = Exclude(keyof Mixed, *)       # "id"
```

`keyof` includes explicitly described attributes and item keys. Implicit base
`object` members do not flood it with dunders. `*` is a real generic interface
with a private intrinsic identity, available through Go to Definition; its
private marker is not a public key, and `keyof *` is `never`.

Open attribute contracts and patterned attribute names use that same key family:

<!-- ty-example: attribute-patterns -->
```python
type StringAttributes = { *: str }
type Getters = { (*<f"get_{str}">): () -> str }
```

These describe allowed access; they do not create attributes or change Python's
`__getattr__` behavior.

### 4. Type functions and generics

**Type functions use `()`. Generics use `<>`.**

<table>
<tr><th width="50%">tython</th><th width="50%">TypeScript</th></tr>
<tr><td valign="top">

<!-- ty-example: type-functions -->
```python
type Many(T) = []T
type Names = Many(str)

interface Box<T>:
    value: T

type StringBox = Box<str>

def identity<T>(value: T) -> T:
    return value

inferred = identity("hello")
explicit = identity<str>("hello")
```

</td><td valign="top">

```typescript
type Many<T> = T[];
type Names = Many<string>;

interface Box<T> {
    value: T;
}

type StringBox = Box<string>;

function identity<T>(value: T): T {
    return value;
}

const inferred = identity("hello");
const explicit = identity<string>("hello");
```

</td></tr>
</table>

`type Many(T) = ...` defines a compile-time utility; `Many(str)` evaluates it.
`Box<str>` specializes a generic interface. Generic functions, classes and
lambdas likewise use angle brackets. Constraints use `extends`, and generic
defaults and `const` type parameters use the existing checker machinery.

| tython | TypeScript |
| --- | --- |
| `type Many(T) = []T` / `Many(str)` | `type Many<T> = T[]` / `Many<string>` |
| `interface Box<T>:` / `Box<str>` | `interface Box<T> { ... }` / `Box<string>` |
| `<T extends str = str>` | `<T extends string = string>` |
| `def keep<const T>(value: T)` | `function keep<const T>(value: T)` |

### 5. Type transformations read like Python

Conditional types use `A if T extends U else B`. Mapped types use comprehensions;
template literal types use f-strings.

| tython | TypeScript |
| --- | --- |
| `A if T extends U else B` | `T extends U ? A : B` |
| `{ (K): T[K] for K in keyof T }` | `{ [K in keyof T]: T[K] }` |
| `{ (K): T[K] for K in keyof T if K extends str }` | `{ [K in keyof T as K extends string ? K : never]: T[K] }` |
| `f"public_{K}"` | `` `public_${K}` `` |
| `U if T extends Box<infer U> else never` | `T extends Box<infer U> ? U : never` |

For example, derive event names **and** their callback types from a source shape:

<table>
<tr><th width="50%">tython</th><th width="50%">TypeScript</th></tr>
<tr><td valign="top">

<!-- ty-example: transformations -->
```python
type Present(T) = never if T extends None else T
type Result = Present(str | None)  # str

type Select(T, Keys) = {
    (K): T[K]
    for K in keyof T
    if K extends Keys
}

type PublicUser = Select({"id": int, "name": str, "secret": str}, "id" | "name")

type ChangeHandlers(T) = {
    (f"{K}_changed"): (value: T[K]) -> None
    for K in Extract(keyof T, str)
}

type Events = ChangeHandlers({"name": str, "active": bool})
type NameHandler = Events["name_changed"]  # (value: str) -> None
```

</td><td valign="top">

```typescript
type Present<T> = T extends null ? never : T;
type Result = Present<string | null>;

type Select<T, Keys> = {
    [K in keyof T as K extends Keys ? K : never]: T[K]
};

type PublicUser = Select<
    { id: number; name: string; secret: string },
    "id" | "name"
>;

type ChangeHandlers<T> = {
    [K in Extract<keyof T, string> as `${K}_changed`]:
        (value: T[K]) => void
};

type Events = ChangeHandlers<{ name: string; active: boolean }>;
type NameHandler = Events["name_changed"];
```

</td></tr>
</table>

The callback return uses TS `void` as the idiomatic analogue. Python `None` is an
actual value type; it is not TS's special discard-the-return-value `void` rule.

The callback's input type follows the original field's type. Rename a field or
change its type, and its derived handler changes with it.

Use **`(K)`**, not `K`, for the computed key: `{ K: ... }` declares the literal
attribute `K`. A trailing filter is equivalent to remapping rejected keys to
`never`; a property whose **value** is `never` is not removed.

`infer`, constrained `infer`, conditional distribution, recursion, unions (`|`),
intersections (`&`) and indexed access keep their TypeScript mechanics. For
example, extracting a generic argument still uses the ordinary infer operation:

<!-- ty-example: infer -->
```python
interface Box<T>:
    value: T

type Unbox(T) = U if T extends Box<infer U> else never
type Text = Unbox(Box<str>)  # str
```

### 6. Lists and tuples keep their Python identities

These are **type expressions**, not new runtime collection syntax:

| tython | Closest TypeScript type | Python identity |
| --- | --- | --- |
| `[]str` | `string[]` | Variable-length list |
| `[str, int]` | `[string, number]` | Fixed-position list |
| `()str` | `readonly string[]` | Variable-length tuple |
| `(str, int)` | `readonly [string, number]` | Fixed-position tuple |
| `(str,)` | `readonly [string]` | One-element tuple; `(str)` is just grouping |
| `()` / `[]` | `readonly []` / `[]` | Empty tuple / empty list |
| `(str, *()int)` | `readonly [string, ...number[]]` | Tuple with a rest element |
| `[str, *[]int]` | `[string, ...number[]]` | List with a rest element |
| `()(str \| None)` | `readonly (string \| null)[]` | Tuple of strings or `None` |
| `set<str>` | `Set<string>` | A set, not a new literal shorthand |

The outer delimiter determines the collection: lists are mutable, tuples are
readonly. Rest elements can use either collection spelling:
`(str, *[]int)` and `(str, *()int)` describe the same tuple shape.
Fixed-position lists use TS-style tuple checking; they are not runtime-frozen.
Conversely, a Python tuple is a different runtime object from a list. TS's
`readonly` array types do not create a different JavaScript runtime container.

### 7. Functions remain Python functions

Return types are inferred unless you choose to supply one. Defaults are inferred
and checked. Positional-only `/`, keyword-only `*`, named arguments, `*args` and
`**kwargs` retain Python call rules. Parameter names matter for named calls.

| tython | TypeScript analogue |
| --- | --- |
| `def greet(name: str) -> str:` | `function greet(name: string): string { ... }` |
| `(value: str) -> int` | `(value: string) => number` |
| `def greet(name: str = "Ada"):` | `function greet(name: string = "Ada") { ... }` |
| `def f(value: str, /):` | No positional-only marker; JS arguments are positional |
| `def f(*, name: str):` / `f(name="Ada")` | Usually an options object, such as `f({ name: "Ada" })`; not the same calling convention |
| `(*args: []int) -> int` | `(...args: number[]) => number` |
| `(**kwargs: Dict({ (str): str })) -> None` | No keyword pack; an object parameter is only an analogue |
| `@overload` or `declare def` signatures | TS overload signatures or `declare function` |

<!-- ty-example: functions -->
```python
def greet(name: str, /, *, uppercase = False):
    return name.upper() if uppercase else name

message = greet("Ada", uppercase=True)  # str

type Formatter = (value: str, /, *, uppercase: bool = ...) -> str
```

A callable type looks like a `def` signature without `def` or a name. `= ...`
in a declaration describes an omittable argument, not a new missing-value type.
Rest annotations describe the packs, for example `(*args: []int) -> int` or
`(**kwargs: Dict({ (str): str })) -> None`.

`@overload` and `declare def` can describe alternative call signatures; there is
still one runtime implementation. They do not generate multiple dispatch.

#### Lambdas

Typed lambdas remain Python expressions. Type parameters go after `lambda`,
parameter types go after the parameter names, and the final colon introduces
the expression body.

<table>
<tr><th width="50%">tython</th><th width="50%">TypeScript</th></tr>
<tr><td valign="top">

<!-- ty-example: lambdas -->
```python
upper = lambda value: str: value.upper()

identity = lambda<T extends str> value: T: value

widen = lambda<T extends str> value: T: value as str

invoke = lambda callback: () -> str: callback()

upper_name = upper("Ada")       # str
same = identity("ready")        # "ready"
widened = widen("ready")        # str
```

</td><td valign="top">

```typescript
const upper = (value: string) => value.toUpperCase();

const identity = <T extends string>(value: T) => value;

const widen = <T extends string>(value: T) => value as string;

const invoke = (callback: () => string) => callback();

const upperName = upper("Ada");
const same = identity("ready");
const widened = widen("ready");
```

</td></tr>
</table>

There is **no separate inline lambda return annotation**. In `invoke`, `() -> str`
is the parameter's type, not the lambda's return annotation. Return types are
inferred; asserting the body can change its static result type. Python lambdas
remain single-expression functions, unlike a TS arrow's optional statement body.

### 8. Missing is not `None`

The `optional` modifier marks a member that may be absent. It doesn't add an `undefined`
value to Python, and it doesn't change the member's value type when present.

| tython | TypeScript | Difference |
| --- | --- | --- |
| `{ optional "name": str }` or `{ optional name: str }` | `{ name?: string }` | Presence modifier; separate item/attribute spelling |
| `Patch["name"]` → `str` | `Patch["name"]` → `string \| undefined` | Missing is not part of the tython value type |
| `value["name"]!` | `value.name!` | tython asserts presence; TS removes `null`/`undefined` from the expression type |
| `{ optional (K): T[K] for K in keyof T }` | `{ [K in keyof T]?: T[K] }` | Add optionality |
| `{ -optional (K): T[K] for K in keyof T }` | `{ [K in keyof T]-?: T[K] }` | Remove optionality |
| `str \| None` | `string \| null` | Explicit nullable value, independent of whether the member exists |

<!-- ty-example: optional -->
```python
type Patch = { optional "name": str }
type Name = Patch["name"]  # str, not str | None

def display(patch: Dict(Patch)):
    if "name" in patch:
        return patch["name"]
    return "Anonymous"

def unsafe(patch: Patch):
    return patch["name"]  # error: the key might be absent
```

Use `optional name: str` for an optional attribute, and `hasattr(value, "name")` to test
its presence. Assignments establish local presence; deleting an optional member
invalidates that proof. These facts narrow access without rewriting the declared
object contract or a function's returned object shape.

`value["name"]!` asserts presence without a runtime check. It doesn't turn
`str | None` into `str`. Mapped `optional (K)` adds optionality; `-optional (K)` removes it.
Use `str | None` separately when `None` is an allowed, present value.

### 9. Python values, structural classes

| tython | TypeScript analogue | Important distinction |
| --- | --- | --- |
| `str` / `bool` | `string` / `boolean` | Literals use Python's `True` and `False` |
| `int` / `float` | No exact pair | TS `number` doesn't distinguish integer and floating-point values; JS `bigint` has different runtime rules |
| `bytes` / `complex` | No corresponding scalar types | Python-facing primitives; `b"id"` is a bytes literal type |
| `None` | `null` | There is no separate `undefined` type |
| `object` | No exact equivalent | Python root includes every value and exposes its common object surface; TS `object` excludes primitives |
| `Some` | `{}` / `NonNullable<unknown>` under strict null checking | Non-`None`, including `0`, `False`, `""`; the TS analogue excludes both nullish values |
| `Object` | TS lowercase `object` is the closest category | Structured values, not bare scalars; not TS uppercase `Object` |
| `{}` | No exact equivalent to TS `{}` | tython's empty contract accepts `None`; TS `{}` excludes nullish values under strict null checking |
| `unknown`, `any`, `never` | `unknown`, `any`, `never` | Unknown input, checking escape hatch, and impossible type |

`bool` is deliberately not implicitly assignable to `int`, despite Python's
runtime inheritance. Likewise, `int` and `float` remain distinct; choose an
explicit union or conversion where needed. Assertions such as `True as int`
change checking, not the runtime value. Literal types use Python spelling:
`True`, `False`, `None`, and `b"id"`.

Classes and interfaces are structural. Interfaces use Python-style base lists,
with generics before the parentheses:

| tython | TypeScript |
| --- | --- |
| `interface NamedBox<T>(Named):` | `interface NamedBox<T> extends Named { ... }` |
| `class User:` / `User("Ada")` | `class User { ... }` / `new User("Ada")` |
| `def __init__(self, name: str):` | `constructor(name: string) { ... }` |
| `self.name` | `this.name` |
| `User` / `typeof User` | `User` / `typeof User` for instance/class-value types |
| `kind = "user"` in the class body | No single equivalent: TS separates instance fields and `static` members |

<!-- ty-example: classes -->
```python
interface Named:
    name: str

interface NamedBox<T>(Named):
    value: T

class User:
    kind = "user"  # inferred; no explicit annotation required

    def __init__(self, name: str):
        self.name = name

def welcome(user: Named):
    return user.name

message = welcome(User("Ada"))
type Constructor = typeof User
```

`User` in a type position means an instance; `typeof User` means the class value.
Bound methods account for the receiver. Method declarations can also appear
inside structural types; callable-valued properties use a colon:

| tython | TypeScript |
| --- | --- |
| `def greet(message: str) -> str` | `greet(message: string): string` |
| `callback: (message: str) -> str` | `callback: (message: string) => string` |
| `"handler": (message: str) -> str` | `"handler": (message: string) => string`; no separate item namespace |

<!-- ty-example: methods -->
```python
type Greeter = {
    def greet(message: str) -> str,
    callback: (message: str) -> str,
    "handler": (message: str) -> str,
}
```

#### Inheritance

Class inheritance uses Python's base-list syntax too. The generic parameters,
when present, precede that list: `class Child<T>(Base<T>):`.

<table>
<tr><th width="50%">tython</th><th width="50%">TypeScript</th></tr>
<tr><td valign="top">

<!-- ty-example: inheritance -->
```python
class User:
    def label(self) -> str:
        return "user"

class Admin(User):
    def admin_label(self) -> str:
        return self.label().upper()

label = Admin().admin_label()  # str
```

</td><td valign="top">

```typescript
class User {
    label(): string {
        return "user";
    }
}

class Admin extends User {
    adminLabel(): string {
        return this.label().toUpperCase();
    }
}

const label = new Admin().adminLabel();
```

</td></tr>
</table>

| tython | TypeScript | Runtime distinction |
| --- | --- | --- |
| `interface C(A, B):` | `interface C extends A, B { ... }` | Static contracts in both |
| `class C(A, B):` | No direct equivalent | Python supports multiple runtime bases; a TS class has one superclass |
| `class C<T>(Base<T>):` | `class C<T> extends Base<T> { ... }` | Python versus JS class construction still applies |
| `class A(B, C as ProjectedC):` | No direct equivalent | Project the static base contract without changing the actual Python base |

Incompatible inherited contracts are rejected rather than resolved by pretending
Python's MRO makes every base contract true. A base can be explicitly projected
with `class A(B, C as ProjectedC): ...`; the assertion erases, so runtime inheritance
still uses `C`. Library authors must make the declared contract match reality.

Python-specific operations—iteration, operators, async, generators and context
managers—use protocol adaptation around the shared checker. Recognized built-in
decorators such as `@property` have dedicated handling. Arbitrary third-party
decorators do **not** automatically transform a declaration's static type; describe
their resulting contract explicitly. Metaclass/descriptor modeling is not complete.

### 10. Assertions, readonly and imports

| tython | TypeScript |
| --- | --- |
| `value as Type` | `value as Type` |
| `value satisfies Type` | `value satisfies Type` |
| `value as const` | `value as const` |
| `{ readonly name: str }` | `{ readonly name: string }` |
| Ordinary `name = value`; no `const` binding syntax | `let name = value` or `const name = value` |
| `from models import type User` | `import type { User } from "./models"` |
| `import type models` | `import type * as models from "./models"` |
| `from models import User` | `import { User } from "./models"`; different module/runtime rules |

<!-- ty-example: assertions -->
```python
fixed = {"mode": "ready", "retries": 3} as const
checked = {"id": 12} satisfies {"id": int}
mode = fixed["mode"]  # "ready"
```

- `as Type` is a checked type assertion, not a Python conversion.
- `satisfies Type` checks compatibility while retaining the expression's inferred
  type; its target can provide contextual typing.
- `as const` preserves literal types and readonly members. It doesn't freeze
  Python objects or turn lists into tuples.
- `readonly` describes member mutability. `const name = ...` and readonly variable
  declarations are not introduced.
- Runtime imports use Python syntax. `import type module` and
  `from module import type Name` are erased type-only imports. Ordinary imports
  remain real Python imports; declarations don't provide runtime implementations.

The actual library source is [builtins.d.ty](tsc/internal/python/lib/builtins.d.ty).
Utilities such as `Exclude`, `Extract`, `ItemKeys` and `Dict` are declarations
using ordinary type machinery. A familiar TS utility is not necessarily a bundled
name yet; consult the library rather than assuming the entire TS lib is imported.

### 11. Python-specific protocols

These aren't just alternative spellings for TS syntax. They are the Python
runtime behaviors the frontend connects to the shared type checker.

| Python / tython | JavaScript / TypeScript analogue | What tython checks |
| --- | --- | --- |
| `obj[key]`, `obj.attr` | Both access JS properties on ordinary objects | Separate indexed and attribute contracts |
| `for value in source` with `__iter__` / `__next__` | `for ... of` with `[Symbol.iterator]()` / `next()` | Iteration element types |
| `left + right` with operator dunders | No general user-defined JS operator overloads | Declared Python operator result types |
| `with manager as value` with `__enter__` / `__exit__` | No exact equivalent; `try/finally` or disposable APIs serve related purposes | The entered value's type, not just the manager's type |
| `async def fetch() -> str` | `async function fetch(): Promise<string>` | The Python annotation describes the awaited result; the call returns an awaitable |
| `async for`, `async with` | `for await ... of`; no exact `async with` equivalent | Async iteration and context-manager protocols |
| `yield from source` | `yield* source` | Generator delegation, subject to Python's runtime rules |
| `Generator<Yield, Send, Return>` | `Generator<Yield, Return, Next>` | **Different parameter order** for send/next and final return |
| `@property`, `@staticmethod`, `@classmethod` | Getters / `static` are related; class methods have no exact counterpart | Recognized built-in descriptor/binding behavior |

For example, entering a context manager changes which value you work with:

<!-- ty-example: context-manager -->
```python
class Greeting:
    def __enter__(self) -> str:
        return "Hello"

    def __exit__(self, error_type: any, error: any, traceback: any) -> bool:
        return False

with Greeting() as message:
    greeting = message.upper()  # str, not Greeting
```

Async calls and generator exchanges retain their distinct types:

<table>
<tr><th width="50%">tython</th><th width="50%">TypeScript</th></tr>
<tr><td valign="top">

<!-- ty-example: async-generator -->
```python
async def fetch_name() -> str:
    return "Ada"

pending: Awaitable<str> = fetch_name()

def exchange() -> Generator<int, str, bool>:
    received = yield 1
    return received == "done"

stream = exchange()
```

</td><td valign="top">

```typescript
async function fetchName(): Promise<string> {
    return "Ada";
}

const pending: Promise<string> = fetchName();

function* exchange(): Generator<number, boolean, string> {
    const received = yield 1;
    return received === "done";
}

const stream = exchange();
```

</td></tr>
</table>

An awaitable is a protocol, not an alias for JavaScript's `Promise`. Neither
example changes the underlying language's scheduling or generator behavior.
These adaptations do not imply complete coverage of every dunder, descriptor
or standard-library class; the current boundaries are listed below.

## What this could unlock for libraries

The biggest payoff isn't annotating more variables. It's letting a library
describe how **the shape of its input determines the shape of its output**.

Consider pandas. Its real API already supports selecting columns and converting
the result into record dictionaries:

```python
import pandas as pd

people = pd.DataFrame([
    {"name": "Ada", "city": "London", "active": True}
])

records = people[["name", "active"]].to_dict("records")
# Runtime result: [{"name": "Ada", "active": True}]
```

With suitable shape-aware declarations, the desired static result would be:

```text
records                → []Dict({"name": str, "active": bool})
records[0]["name"]     → str
records[0]["active"]   → bool
records[0]["city"]     → type error: this column was not selected
```

> **Illustrative integration, not bundled pandas support.** This shows a contract
> a library author could aim to express, not a claim that today's extension infers
> these pandas results. Real declarations must account for dtype coercion, missing
> data, dynamic columns and mutations; the example deliberately uses known data
> and a simple selection. It does not infer an unknown CSV's schema by reading it.

See pandas' [column-selection guide](https://pandas.pydata.org/docs/getting_started/intro_tutorials/03_subset_data.html)
and [`to_dict` documentation](https://pandas.pydata.org/docs/reference/api/pandas.DataFrame.to_dict.html)
for the runtime operations. The earlier `Select` and `ChangeHandlers` examples
demonstrate type transformations already checked by tython itself.

## Current limitations

| Area | Current boundary |
| --- | --- |
| Python and libraries | Built-in signatures and stdlib coverage are incomplete. `.d.ty` contracts remain necessary for precise external-library typing. |
| Stub import | Existing `.pyi`/Python annotation import is a supported subset, not automatic conversion of all typeshed or arbitrary implementations. Unsupported conversions report errors. |
| Dictionaries | Some exact-shape to open-map assignments are rejected because their method/index contracts differ. This relationship needs further work. |
| Generic indexing | Some constrained generic indexed-access function bodies and type-utility calls in function constraints still need frontend fixes. See the audit for reproductions. |
| Inheritance | Base-constructor signatures and instance attributes inferred inside a base `__init__` do not yet propagate reliably to subclasses. The inheritance example above exercises declared methods. |
| Editor | Hover still has custom rendering paths. Navigation is partial; complete reference/rename/refactor parity is not promised. |
| Formatting | Ruff handles supported untyped regions. Whole-file rewriting of typed syntax is not yet safe or enabled. |
| Runtime | Erasure is not validation. No inferred arbitrary decorator transformations, general monkey-patch tracking, or runtime enforcement of readonly/presence assertions. |
| Distribution | Verified preview packaging is Linux x64/WSL. Memory containment is required by default; it is not silently disabled on startup failure. |

Shared TypeScript semantics are the design rule, **not a claim that every editor
feature or Python adaptation is finished**. The [design plan](TYTHON_PLAN.md)
contains the detailed decisions and deferred work. The
[repository audit](docs/REPOSITORY_AUDIT.md) tracks cleanup and adaptation gaps.

### A note on syntax highlighting

The README and the `.ty` editor use different highlighters. The README labels
tython examples as `python` so ordinary Markdown viewers can color the Python
parts; their Python grammars don't know about `keyof`, `extends`, `*<...>`, or
generic lambdas. The TS comparisons use `typescript` fences.

GitHub [selects grammars from code-fence language labels](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/creating-and-highlighting-code-blocks);
it doesn't load our VS Code grammar. IDE Markdown previews can use yet another
renderer—VS Code, for example, has a separate
[Markdown extension API](https://code.visualstudio.com/api/extension-guides/markdown-extension).
Tables use compact inline code, which generally has no syntax coloring.

For tython-aware highlighting, open a `.ty` file with the extension enabled.
If those files also have missing colors, that's a separate grammar/theme issue
we'd love an example of. The README's fallback is not a test of editor highlighting.

## Help shape tython

Try something you'd actually want to build. Tell us what feels natural, what
feels awkward, or which library you'd love to see typed this way. You don't need
to know compiler internals—or have a fix—to help.

[Share an idea or report a problem](https://github.com/jdrebin/TyThon/issues).
A small example is especially helpful, but questions, documentation improvements
and experiments are welcome too. If you'd like to work on the code, start with
[the contributor guide](CONTRIBUTING.md).

<details>
<summary>Working on the README examples?</summary>

To check the examples in this README using the real parser and checker:

```sh
cd tsc
go test ./internal/python -run '^TestReadmeExamples$' -count=1
```

The test reads the marked tython examples directly, verifies intentional errors and selected
inferred types. The pandas illustration is deliberately outside that claim.

</details>

## Built on TypeScript

tython adapts Microsoft's TypeScript implementation and retains its shared
checker and regression infrastructure. This is an independent project, not an
official Microsoft product or an implementation built from scratch.

The compiler lives in `tsc/`; the Python frontend and canonical declarations are
in `tsc/internal/python/`; the editor is in `packages/vscode-python-typescript/`.
Some internal names remain unchanged for compatibility.

Upstream licenses, copyright and third-party notices are retained in
[LICENSE.txt](LICENSE.txt), [NOTICE.txt](NOTICE.txt), and component directories.
See [licensing and provenance](docs/LICENSING.md) for reuse and distribution details.
