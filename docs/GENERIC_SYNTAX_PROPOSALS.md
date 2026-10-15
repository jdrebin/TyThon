# Deferred generic syntax and upper bounds

## Decision

**Deferred on October 15, 2026.** Keep the current language syntax and behavior.
This includes deferring colon upper bounds, bracket declarations and
specialization, named type arguments, and new disambiguation syntax. These
proposals are discussion material, not implementation requirements or accepted
conformance cases.

The current contract remains in [the design plan](../TYTHON_PLAN.md#type-declarations-and-generic-syntax):

- Generic declarations and specialization use angle brackets.
- Type functions use parentheses for declaration and application.
- Upper bounds and constrained inference use `extends`.
- Conditional assignability tests continue to use `extends`.

The user wants optional named type arguments eventually, but their design and
implementation are deferred with this discussion. The user explicitly declined
`infer A: int`; do not fold that spelling into a future colon-bound change.

## Motivation and runtime goal

Angle brackets distinguish specialization from indexing, but their appearance
and distance from Python syntax motivated exploring square brackets. Colon
upper bounds are an independent readability and Python-alignment proposal.

Future designs must account for replacing TyThon constructs with Python code,
as well as erasing them. For example, `typeof 0` could be represented as
`typing.Literal[0]`. That is a proposed lowering, not a claim that arbitrary
`typeof` expressions are currently supported or that every TyThon type has a
Python representation. See [types as Python annotations](../TYTHON_PLAN.md#tython-types-as-python-annotations).

## Comma based specialization proposal

The initial proposal used brackets for declarations and a comma at the outer
level of an application to distinguish specialization from indexed access:

```ty
class Example[T: int = int, S: str = str]:
    (int): T
    (str): S

type Indexed = Example[int]           # intended: int
type Specialized = Example[int,]      # T = int, S defaults to str
type Both = Example[int, str]         # T = int, S = str

def identity[T: int = int](value: T): ...

identity[0]   # initial proposal: indexing error, no item protocol
identity[0,]  # initial proposal: specialize T with literal type 0
```

In type expressions, the proposed distinction was:

| Form | Proposed meaning |
| --- | --- |
| `G[K]` | Indexed access |
| `G[T,]` | One generic argument |
| `G[T, U]` | Two generic arguments |
| `G[(T, U)]` | Index by one tuple type |
| `G[(T, U),]` | One generic argument that is a tuple type |
| `G[T,][K]` | Specialization followed by indexed access |

The same rule would apply to inference patterns:

```ty
type Extracted = A if Example[0,] extends Example[infer A,] else never
# Intended result: 0.

type Invalid = A if Example[0,] extends Example[infer A] else never
# Indexed-access interpretation; no implicit specialization fallback.
```

Type functions would retain their separate operation:

```ty
type Func(T: int = int) = { id: T }
Func(0)   # intended: { id: 0 }
Func[0,]  # no generic specialization operation on the type function
Func[0]   # no item key 0 in the resulting default shape
```

Python already uses `obj[a, b]` and `obj[a,]` for tuple-key subscription. A
uniform comma rule would take over those forms. Explicit tuple parentheses
could preserve ordinary tuple keys, but multidimensional slicing such as
`obj[:, 0]` would need a policy too. A later variant restricted the comma rule
to type expressions and used different rules in executable expressions. That
avoids taking over runtime tuple indexing, at the cost of context-dependent
interpretation. [Python 3.15 subscriptions](https://docs.python.org/3.15/reference/expressions.html#subscriptions)

Additional unresolved details include empty application (`G[]`), omitted
arguments and defaults, partial inference, parameter packs, and specialization
through aliases. Formatters must preserve the semantic trailing comma and the
parentheses around tuple keys. Removing a second generic argument must not
accidentally turn the remaining argument into an index.

## Runtime type representations and subscription

The runtime alternative distinguished a value from a type expression:

```ty
target[0]          # value subscription
target[typeof 0]   # proposed type-argument form
```

Constructor or function calls can also infer type arguments from values when
their signatures support it. However, replacing `typeof 0` with `Literal[0]`
only changes the subscription argument. It does not automatically select a
different Python operation.

An instance with a generic `__call__` and an ordinary `__getitem__` does not
demonstrate a subscription clash: `instance[key]` invokes item access, while
`instance(value)` invokes the call method. A generic call method does not make
subscription a specialization operation on the instance.

For classes, instance indexing and class subscription are also separate. A
class's instance `__getitem__` does not intercept `Class[Type]`. A real overlap
can occur when the metaclass supplies `__getitem__` and the generic class has
`__class_getitem__`. Python gives the metaclass indexer precedence; it does not
check the key's annotation and fall back to generics when the key fails.
[Python 3.15 subscription dispatch](https://docs.python.org/3.15/reference/datamodel.html#class-getitem-versus-getitem)

This example uses the generic declaration syntax supported by Python 3.15:

```python
from typing import Literal as L

class Meta(type):
    def __getitem__(cls, key):
        return {(L[0], L[1]): 12}[key]

class Box[A, B](metaclass=Meta):
    pass

Box[L[0], L[1]]  # 12
```

The concrete key contains the `Literal` objects themselves, not the integers
`0` and `1`. This captures the intended class-entry shorthand
`(L[0], L[1]) = 12` from the discussion. That shorthand is not a Python entry
declaration: written literally in Python, it is unpacking assignment.

The caller can select either operation explicitly:

```python
args = (L[0], L[1])
type(Box).__getitem__(Box, args)  # indexing: 12
Box.__class_getitem__(args)       # generic alias for Box[Literal[0], Literal[1]]
```

In this example, `Box[L[0], L[1]]`, `Box[(L[0], L[1])]`, and
`Box[L[0], L[1],]` pass the same tuple through the same subscription dispatch.
A trailing comma cannot force Python to choose the generic hook. For a normal
generic class with no metaclass indexer, the generic hook interprets a tuple
argument as the sequence of type arguments.

## Erasure and replacement have different contracts

Once an expression is known to request static specialization, an erasing
compiler can emit just the target, such as `Box`. It need not call the generic
hook. It must still distinguish that request from an actual lookup returning
`12` before erasure.

Preserving a Python specialization creates observable runtime information:

```python
from typing import get_args

specialized = Box.__class_getitem__((L[0], L[1]))
get_args(specialized)  # (Literal[0], Literal[1])
get_args(Box)          # ()
specialized is Box     # False
```

Runtime consumers can inspect those arguments. Custom subscription hooks can
also execute code and return different objects. Erasure is therefore a valid
language contract, but not a universally equivalent replacement for Python
specialization. [Python type inspection](https://docs.python.org/3.15/library/typing.html#typing.get_args)

A future emitter needs an explicit policy for static specialization versus
runtime type representations, including imported Python classes with custom
hooks. Ordinary Python functions also do not gain a subscription operation
merely by being generic; retained function specialization needs its own
lowering policy.

Type-level indexed access has a related issue: emitting `SomeType[Key]`
unchanged can ask Python to specialize a class rather than project its indexed
value type. The compiler must compute the projection or use the documented
fallback for types without a runtime representation.

## Named type arguments proposal

Named application provides an explicit specialization form:

```ty
class Box[T: int = int]:
    value: T

Box[T=0]
type Unbox(X) = U if X extends Box[T=infer U] else never
```

For a receiver supporting the appropriate item operation, the intended split
would be `Box[key]` or `Box[a, b]` for indexing and `Box[T=Type]` or
`Box[A=0, B=1]` for specialization. The named argument establishes type context
on its right side. Python 3.15 has no keyword subscription form, so the latter
would need transpilation. [Python subscription grammar](https://docs.python.org/3.15/reference/expressions.html#subscriptions)

Two policies were considered; neither was selected:

1. Require names for every explicit specialization. This makes the operation
   syntactically explicit, independent of the receiver.
2. Permit positional specialization when the receiver has no indexing
   operation; give indexing precedence when both operations exist, with names
   as an explicit specialization escape hatch. Choose by the receiver's
   operations, not by whether a particular key passes checking. Adding indexing
   to an existing API could then change previous positional applications.

Optional named type arguments are desirable independently of bracket syntax.
They support descriptive applications and selection of defaulted parameters.
Using them as the required disambiguator has a verbosity cost, especially for
generic containers and inference patterns:

```ty
Cache[Key=str, Value=User]
Cache[Key=infer K, Value=infer V]

# Positional comparison with existing delimiters:
Cache<str, User>
Cache<infer K, infer V>
```

Names become part of the application API. A future design must address
renaming, overloads with different parameter names, aliases and structurally
equivalent generic callables, argument ordering, and how omitted parameters
use defaults or inference. Optional names inside angle brackets were also
discussed (`Cache<K=str, V=User>`); they are not current syntax.

## Colon upper bounds and other independent options

Replacing declaration bounds with `:` can be considered independently:

```ty
class Box<T: int = int>:
    value: T

def identity<T: int>(value: T) -> T: ...
type Func(T: int = int) = { id: T }
```

This change is deferred too. If revisited, it must not change conditional
`T extends U` tests or constrained `infer A extends int`. The proposed
`infer A: int` spelling was explicitly declined.

TyThon's tuple type bound `(int, str)` would need to project to Python's
`tuple[int, str]` bound. In Python, `T: (int, str)` instead declares a choice
of constraints. Colon spelling must not silently change the existing bound
semantics. [Python 3.15 type parameters](https://docs.python.org/3.15/reference/compound_stmts.html#type-params)

Other options were raised without adoption:

- Square brackets only in declarations, retaining positional angle-bracket
  applications. This avoids application ambiguity but mixes delimiter styles.
- A compact explicit specialization marker, illustrated by `Box.[int]` or
  `Box@[int]`. No marker or syntax was selected.
- Keep angle brackets and add optional named type arguments later.

Any complete delimiter migration must cover classes, interfaces, functions,
methods, lambdas, anonymous generic callable types, `const` parameters,
inheritance, and inference patterns. Anonymous callable headers beginning
with `[T]` also share a prefix with existing list types.

## Conditions for revisiting

If reopened, evaluate bracket proposals for concise positional application
and a coherent account of type-level indexing, runtime subscription, and
emitted type representations. Resolve omitted arguments, defaults, inference,
and name stability before using named arguments as a disambiguation rule.
Include formatter, printer, editor, and migration behavior in that proposal.
Colon upper bounds and optional named arguments can be revisited independently;
none of these changes are currently scheduled for implementation.
