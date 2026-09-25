"""Annotation syntax adapter, using CPython's AST (never importing the target).

The generated declarations are checked by the existing Typed-Python/TS pipeline.
This does not infer types or reproduce Pyright's type semantics. Unsupported
constructs fail the entire preview instead of silently introducing any.
"""

import ast
import sys


class Unsupported(ValueError):
    pass


class Declarations:
    def __init__(self, source):
        self.source = source
        self.tree = ast.parse(source, type_comments=True)
        self.imports = {}
        self.names = {
            node.name
            for node in self.tree.body
            if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef))
        }
        for node in self.tree.body:
            if isinstance(node, ast.Assign):
                self.names.update(t.id for t in node.targets if isinstance(t, ast.Name))
            elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
                self.names.add(node.target.id)
            elif isinstance(node, ast.TypeAlias):
                self.names.add(node.name.id)
            elif isinstance(node, ast.ImportFrom):
                for alias in node.names:
                    self.imports[alias.asname or alias.name] = (
                        f"{node.module}.{alias.name}"
                    )
            elif isinstance(node, ast.Import):
                for alias in node.names:
                    self.imports[alias.asname or alias.name] = alias.name

    def fail(self, node, message):
        raise Unsupported(f"Line {getattr(node, 'lineno', 1)}: {message}")

    def qualified(self, node):
        if isinstance(node, ast.Name):
            return self.imports.get(node.id, node.id)
        if isinstance(node, ast.Attribute):
            return f"{self.qualified(node.value)}.{node.attr}"
        return ""

    def annotation(self, node, parameters=()):
        if node is None:
            self.fail(
                self.tree,
                "Missing annotation; inference from Python implementations is not imported yet",
            )
        if isinstance(node, ast.Constant):
            if node.value is None:
                return "None"
            if isinstance(node.value, str):
                return self.annotation(
                    ast.parse(node.value, mode="eval").body, parameters
                )
        if isinstance(node, ast.BinOp) and isinstance(node.op, ast.BitOr):
            return f"({self.annotation(node.left, parameters)} | {self.annotation(node.right, parameters)})"
        name = self.qualified(node)
        simple = {"int", "float", "bool", "str", "bytes", "object", "None"}
        specials = {
            "typing.Any": "any",
            "typing.Never": "never",
            "typing.NoReturn": "never",
        }
        if name in specials:
            return specials[name]
        if (
            name in simple
            or isinstance(node, ast.Name)
            and node.id in (*self.names, *parameters)
        ):
            return node.id
        if isinstance(node, ast.Subscript):
            base = self.qualified(node.value)
            args = (
                node.slice.elts if isinstance(node.slice, ast.Tuple) else [node.slice]
            )
            if base == "typing.Literal":
                values = []
                for arg in args:
                    try:
                        value = ast.literal_eval(arg)
                    except (ValueError, TypeError):
                        self.fail(arg, "Only scalar Literal arguments are supported")
                    if type(value) not in (str, bytes, int, bool, type(None)):
                        self.fail(arg, "Unsupported Literal argument")
                    values.append(repr(value))
                return "(" + " | ".join(values) + ")"
            if base == "typing.Optional" and len(args) == 1:
                return f"({self.annotation(args[0], parameters)} | None)"
            if base == "typing.Union":
                return (
                    "(" + " | ".join(self.annotation(a, parameters) for a in args) + ")"
                )
            if base in {"tuple", "typing.Tuple"}:
                if (
                    len(args) == 2
                    and isinstance(args[1], ast.Constant)
                    and args[1].value is Ellipsis
                ):
                    return "()(" + self.annotation(args[0], parameters) + ")"
                values = [self.annotation(a, parameters) for a in args]
                return "(" + ", ".join(values) + ("," if len(values) == 1 else "") + ")"
            collections = {
                "list": ("list", 1),
                "typing.List": ("list", 1),
                "dict": ("dict", 2),
                "typing.Dict": ("dict", 2),
                "set": ("set", 1),
                "typing.Set": ("set", 1),
            }
            if base in collections:
                target, count = collections[base]
                if len(args) != count:
                    self.fail(node, f"Expected {count} arguments for {base}")
                if target == "dict":
                    key, value = (self.annotation(a, parameters) for a in args)
                    return f"Dict({{ ({key}): {value} }})"
                return (
                    target
                    + "<"
                    + ", ".join(self.annotation(a, parameters) for a in args)
                    + ">"
                )
        self.fail(node, f"Unsupported annotation: {ast.unparse(node)}")

    def function(self, node, owner=False):
        if node.type_comment or node.type_params:
            self.fail(
                node, "Type comments and generic declarations are not imported yet"
            )
        decorators = []
        for decorator in node.decorator_list:
            name = self.qualified(decorator)
            if name not in {
                "typing.overload",
                "property",
                "staticmethod",
                "classmethod",
            }:
                self.fail(
                    decorator,
                    "Decorator needs an explicit declaration; transformations are not imported",
                )
            decorators.append("@" + name.split(".")[-1])
        args = node.args
        positional = args.posonlyargs + args.args
        defaults = [None] * (len(positional) - len(args.defaults)) + args.defaults
        params = []
        for i, (arg, default) in enumerate(zip(positional, defaults)):
            if (
                owner
                and i == 0
                and arg.annotation is None
                and arg.arg in {"self", "cls"}
                and "@staticmethod" not in decorators
            ):
                value = arg.arg
            else:
                value = f"{arg.arg}: {self.annotation(arg.annotation)}"
            if default is not None:
                if (
                    not isinstance(default, ast.Constant)
                    or default.value is not Ellipsis
                ):
                    self.fail(
                        default,
                        "Non-ellipsis defaults need explicit validation; use a .pyi stub with = ...",
                    )
                value += " = ..."
            params.append(value)
            if i + 1 == len(args.posonlyargs):
                params.append("/")
        if args.vararg:
            params.append(
                f"*{args.vararg.arg}: ()({self.annotation(args.vararg.annotation)})"
            )
        elif args.kwonlyargs:
            params.append("*")
        for arg, default in zip(args.kwonlyargs, args.kw_defaults):
            value = f"{arg.arg}: {self.annotation(arg.annotation)}"
            if default is not None:
                if (
                    not isinstance(default, ast.Constant)
                    or default.value is not Ellipsis
                ):
                    self.fail(
                        default,
                        "Non-ellipsis defaults need explicit validation; use a .pyi stub with = ...",
                    )
                value += " = ..."
            params.append(value)
        if args.kwarg:
            params.append(
                f"**{args.kwarg.arg}: Dict({{ (str): {self.annotation(args.kwarg.annotation)} }})"
            )
        prefix = "" if owner else "declare "
        if isinstance(node, ast.AsyncFunctionDef):
            prefix += "async "
        result = self.annotation(node.returns)
        return decorators + [
            f"{prefix}def {node.name}({', '.join(params)}) -> {result}: ..."
        ]

    def statements(self, nodes, owner=False):
        lines = []
        for node in nodes:
            if isinstance(node, (ast.Pass, ast.Expr)) and (
                isinstance(node, ast.Pass)
                or isinstance(node.value, ast.Constant)
                and (isinstance(node.value.value, str) or node.value.value is Ellipsis)
            ):
                continue
            if isinstance(node, (ast.Import, ast.ImportFrom)):
                if owner:
                    self.fail(node, "Class-local imports are not supported")
                modules = (
                    [node.module]
                    if isinstance(node, ast.ImportFrom)
                    else [a.name for a in node.names]
                )
                if any(m not in {"typing", "__future__"} for m in modules):
                    self.fail(
                        node,
                        "External type dependencies must be declared explicitly; recursive stub import is not implemented",
                    )
                if any(a.name == "*" for a in node.names):
                    self.fail(node, "Wildcard imports are not supported")
                continue
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                lines.extend(self.function(node, owner))
            elif isinstance(node, ast.ClassDef):
                if (
                    owner
                    or node.bases
                    or node.keywords
                    or node.decorator_list
                    or node.type_params
                ):
                    self.fail(
                        node,
                        "Class bases, generics, decorators and nested classes need explicit declarations",
                    )
                body = self.statements(node.body, True)
                lines.append(f"declare class {node.name}:")
                lines.extend("    " + line for line in (body or ["pass"]))
            elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
                if self.qualified(node.annotation) == "typing.TypeAlias" and not owner:
                    lines.append(
                        f"type {node.target.id} = {self.annotation(node.value)}"
                    )
                else:
                    if node.value is not None and not (
                        isinstance(node.value, ast.Constant)
                        and node.value.value is Ellipsis
                    ):
                        self.fail(
                            node,
                            "Annotated runtime values need explicit validation; use an annotation-only stub",
                        )
                    lines.append(
                        f"{node.target.id}: {self.annotation(node.annotation)}"
                    )
            elif isinstance(node, ast.TypeAlias) and not owner and not node.type_params:
                lines.append(f"type {node.name.id} = {self.annotation(node.value)}")
            else:
                self.fail(node, f"Unsupported declaration: {type(node).__name__}")
        return lines

    def render(self):
        return "\n".join(self.statements(self.tree.body)) + "\n"


def convert(source):
    if sys.version_info < (3, 12):
        return {
            "text": "",
            "errors": [
                "Declaration import requires Python 3.12 or newer in the tools environment"
            ],
        }
    try:
        return {"text": Declarations(source).render(), "errors": []}
    except (Unsupported, SyntaxError) as error:
        return {"text": "", "errors": [str(error)]}
