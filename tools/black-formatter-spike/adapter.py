"""Tython grammar/layout adaptation of pinned, pure-Python Black.

The pinned pure-Python dependency is left unmodified on disk. This process-only
patch runs in a dedicated formatter process, never in the language server or
user's Python interpreter. It feeds actual .ty text to Black's parser.
"""

import io
import json
import os
from functools import lru_cache
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
FROZEN = getattr(sys, "frozen", False)
BUILD = Path(sys._MEIPASS) if FROZEN else HERE.parents[1] / "built/local/black-spike"
if not FROZEN:
    sys.path.insert(0, str(BUILD / "vendor"))
    os.environ.setdefault("BLACK_CACHE_DIR", str(BUILD / "cache"))

from blib2to3 import pygram
from blib2to3.pgen2 import parse, pgen, token, tokenize

# Parser labels, not substituted source characters. The tree still contains
# '<' and ':' leaves with their real source values and locations.
token.TY_LANGLE = 180
token.TY_ANNOTATION_COLON = 181
token.tok_name[180] = "TY_LANGLE"
token.tok_name[181] = "TY_ANNOTATION_COLON"

REPLACEMENTS = {
    "type_stmt:": "type_stmt: \"type\" NAME ['(' [ty_typeparams] ')'] '=' ty_test",
    "typeparams:": "typeparams: '[' typeparam (',' typeparam)* [','] ']' | TY_LANGLE ty_typeparams '>'",
    "funcdef:": "funcdef: 'def' NAME [typeparams] parameters ['->' ty_test] ':' suite",
    "tname:": "tname: NAME [':' ty_test]",
    "tname_star:": "tname_star: NAME [':' ty_test]",
    "annassign:": "annassign: ':' ty_test ['=' (yield_expr|testlist_star_expr)]",
    "lambdef:": "lambdef: 'lambda' [typeparams] [varargslist] ':' test",
    "vname:": "vname: NAME [TY_ANNOTATION_COLON ty_test]",
    "classdef:": "classdef: ('class' | \"interface\") (NAME | '*') [typeparams] ['(' [arglist] ')'] ':' suite",
    "test:": "test: ty_assertion ['if' or_test 'else' test] | lambdef",
    "trailer:": "trailer: '(' [arglist] ')' | '[' subscriptlist ']' | '.' NAME | '!' | ty_call_typeargs '(' [arglist] ')'",
    "small_stmt:": "small_stmt: (ty_declare | type_stmt | expr_stmt | del_stmt | pass_stmt | flow_stmt |",
}

RULES = r"""
ty_typeparams: ty_typeparam (',' ty_typeparam)* [',']
ty_typeparam: NAME ["extends" ty_test] ['=' ty_test]
ty_assertion: or_test (('as' | "satisfies") ty_test)*
ty_test: ty_union ['if' ty_union "extends" ty_union 'else' ty_test]
ty_union: ty_intersection ('|' ty_intersection)*
ty_intersection: ty_prefix ('&' ty_prefix)*
ty_prefix: ("keyof" | "typeof") ty_prefix | "infer" NAME ["extends" ty_union] | ty_generic_callable | ty_primary
ty_primary: ty_atom ty_trailer*
ty_trailer: TY_LANGLE ty_arglist '>' | '(' [ty_arglist] ')' | '[' ty_test ']' | '.' NAME
ty_atom: (NAME | NUMBER | STRING+ | fstring | '.' '.' '.' | '*' [ty_primary] |
          '(' [ty_arglist] ')' ['->' ty_test | ty_primary] |
          '[' [ty_arglist] ']' [ty_primary] |
          '{' [ty_members] '}')
ty_arglist: ty_argument (',' ty_argument)* [',']
ty_argument: ty_test [':' ty_test] ['=' test]
ty_members: ty_member (ty_comp_for | (',' ty_member)* [','])
ty_member: ["readonly"] ["optional" | '-' "optional"] ty_test ':' ty_test | 'def' NAME [typeparams] parameters '->' ty_test
ty_comp_for: 'for' NAME 'in' ty_union ['if' ty_union "extends" ty_union]
ty_generic_callable: ty_callable_typeparams parameters '->' ty_test
ty_callable_typeparams: TY_LANGLE ty_typeparams '>'
ty_call_typeargs: TY_LANGLE ty_arglist '>'
ty_declare: "declare" ['async'] 'def' NAME [typeparams] parameters ['->' ty_test] [':' '.' '.' '.']
"""

_initialize = pygram.initialize


def initialize(cache_dir=None):
    _initialize(cache_dir)
    original = Path(pygram.__file__).with_name("Grammar.txt").read_text()
    lines = []
    for line in original.splitlines():
        lines.append(
            next(
                (
                    value
                    for prefix, value in REPLACEMENTS.items()
                    if line.startswith(prefix)
                ),
                line,
            )
        )
    grammar = pgen.ParserGenerator(
        "tython-spike", io.StringIO("\n".join(lines) + RULES)
    ).make_grammar()
    grammar.async_keywords = True
    grammar.version = (3, 10)
    pygram.python_grammar = grammar
    pygram.python_grammar_async_keywords = grammar
    pygram.python_grammar_soft_keywords = grammar
    pygram.python_symbols = pygram._python_symbols(grammar)


pygram.initialize = initialize
_classify = parse.Parser.classify


def classify(self, kind, value, context):
    # Reuse Black's soft-keyword machinery, permitting our contextual type
    # keywords inside expressions as well as at statement boundaries.
    if kind == token.NAME and value in {
        "keyof",
        "typeof",
        "infer",
        "extends",
        "satisfies",
        "optional",
        "readonly",
    }:
        return [self.grammar.tokens[token.NAME], self.grammar.soft_keywords[value]]
    return _classify(self, kind, value, context)


parse.Parser.classify = classify

import black
import black.brackets
import black.lines
import black.linegen
import black.parsing
from black.nodes import syms, preceding_leaf
from blib2to3.pytree import Node

assert (
    black.__version__ == "26.5.1"
), "The spike must run against its pinned Black source"

_parse = black.parsing.lib2to3_parse
_tokenize = tokenize.tokenize
_lexical_context = None


@lru_cache(maxsize=128)
def analyze(source):
    if len(source.encode("utf8")) > 1_000_000:
        raise ValueError("Spike input exceeds 1 MB")
    result = subprocess.run(
        [str(BUILD / "oracle")],
        input=json.dumps({"source": source}),
        text=True,
        capture_output=True,
        check=True,
        timeout=10,
    )
    result = json.loads(result.stdout)
    if result["errors"]:
        raise ValueError("Native parser rejected input: " + "; ".join(result["errors"]))
    if result["unsupported"]:
        raise ValueError("Spike limitation: " + "; ".join(result["unsupported"]))
    return result


def typed_tokens(source, *args, **kwargs):
    context = _lexical_context
    offsets = [0]
    for line in source.splitlines(keepends=True):
        offsets.append(offsets[-1] + len(line.encode("utf8")))
    angle_depth = 0
    for kind, value, start, end, line in _tokenize(source, *args, **kwargs):
        if context is not None:
            position = offsets[start[0] - 1] + len(line[: start[1]].encode("utf8"))
            if angle_depth and kind in {token.INDENT, token.DEDENT}:
                continue
            if angle_depth and kind == token.NEWLINE:
                kind = tokenize.NL
            if value == ":" and str(position) in context["colons"]:
                kind = token.TY_ANNOTATION_COLON
            elif value == "<" and context["angles"].get(str(position)) == "open":
                kind = token.TY_LANGLE
                angle_depth += 1
            elif value == ">" and context["angles"].get(str(position)) == "close":
                angle_depth -= 1
            elif value == ">>" and all(
                context["angles"].get(str(position + i)) == "close" for i in range(2)
            ):
                yield token.OP, ">", start, (start[0], start[1] + 1), line
                yield token.OP, ">", (start[0], start[1] + 1), end, line
                angle_depth -= 2
                continue
        yield kind, value, start, end, line


tokenize.tokenize = typed_tokens


def parse_source(source, target_versions=()):
    global _lexical_context
    _lexical_context = analyze(source)
    try:
        tree = _parse(source, target_versions)
    finally:
        _lexical_context = None
    # Tell the existing bracket layout machinery that generic delimiters form
    # a pair. Only CST token categories change; the printed values remain <>.
    for node in list(tree.pre_order()):
        if node.type == token.TY_ANNOTATION_COLON:
            node.type = token.COLON
        if isinstance(node, Node) and node.type == syms.varargslist:
            if any(
                isinstance(child, Node) and child.type == syms.vname
                for child in node.children
            ):
                node.type = syms.typedargslist
                for child in node.children:
                    if isinstance(child, Node) and child.type == syms.vname:
                        child.type = syms.tname
        if (
            isinstance(node, Node)
            and node.type == syms.trailer
            and node.children[0].type == syms.ty_call_typeargs
        ):
            # Keep the runtime call's parentheses in Black's ordinary trailer
            # shape so its existing argument-wrapping rules remain available.
            typeargs = node.children[0]
            typeargs.remove()
            node.parent.insert_child(node.parent.children.index(node), typeargs)
        if isinstance(node, Node) and node.type in {
            syms.ty_trailer,
            syms.typeparams,
            syms.ty_callable_typeparams,
            syms.ty_call_typeargs,
            syms.trailer,
        }:
            if getattr(node.children[0], "value", None) == "<":
                node.children[0].type = token.LSQB
                for child in node.children[1:]:
                    if not isinstance(child, Node) and child.value == ">":
                        child.type = token.RSQB
                        break
            if node.type == syms.ty_trailer:
                node.type = syms.trailer
    return tree


black.lib2to3_parse = parse_source
_whitespace = black.lines.whitespace


def typed_whitespace(leaf, *, complex_subscript, mode):
    parent, previous = leaf.parent, leaf.prev_sibling
    if parent is not None:
        if parent.type == syms.trailer and leaf.value == "!":
            return ""
        if parent.type == syms.ty_declare and leaf.value == ".":
            return "" if previous is not None and previous.value == "." else " "
        if parent.type == syms.ty_call_typeargs and leaf.value == "<":
            return ""
        if parent.type == syms.tname and previous is None:
            prior = preceding_leaf(parent)
            if prior and prior.value in {"lambda", ">"}:
                return " "
        if (
            parent.type == syms.ty_member
            and leaf.value == "optional"
            and previous
            and previous.type == token.MINUS
        ):
            return ""
        if parent.type == syms.type_stmt and leaf.type == token.LPAR:
            return ""
        if parent.type == syms.ty_atom:
            previous = previous or preceding_leaf(parent)
        if parent.type == syms.ty_atom and previous is not None:
            if (
                previous.type in {token.RPAR, token.RSQB, token.STAR}
                and leaf.type != token.RARROW
            ):
                return ""
    return _whitespace(leaf, complex_subscript=complex_subscript, mode=mode)


black.lines.whitespace = typed_whitespace
_decrement_lambda = black.brackets.BracketTracker.maybe_decrement_after_lambda_arguments


def decrement_lambda(self, leaf):
    # Black suppresses comma splitting until the lambda's body colon. An
    # annotation colon must not end that existing protection early.
    if (
        leaf.type == token.COLON
        and leaf.parent
        and leaf.parent.type in {syms.vname, syms.tname}
    ):
        return False
    return _decrement_lambda(self, leaf)


black.brackets.BracketTracker.maybe_decrement_after_lambda_arguments = decrement_lambda

_right_split = black.linegen._first_right_hand_split


def right_split(line, omit=()):
    # Like Black's ordinary lambda parameter list, keep the generic header
    # intact. Prefer splitting the body/enclosing expression, and allow a long
    # indivisible header to exceed line length. Erased type brackets must not
    # be the only thing providing Python's implicit line continuation.
    protected = set(omit)
    for leaf in line.leaves:
        node = leaf.parent
        while node is not None and node.type != syms.lambdef:
            if node.type == syms.ty_call_typeargs or (
                node.type
                in {syms.typeparams, syms.typedargslist, syms.tname, syms.vname}
                and any(parent.type == syms.lambdef for parent in ancestors(node))
            ):
                protected.add(id(leaf))
                break
            node = node.parent
    return _right_split(line, omit=protected)


black.linegen._first_right_hand_split = right_split


def ancestors(node):
    while node.parent is not None:
        node = node.parent
        yield node


def validate_output(source, output):
    before, after = analyze(source), analyze(output)
    for key in ("declarations", "runtime"):
        if before[key] != after[key]:
            raise ValueError(f"Formatting changed the native {key} tree")
    # Black's real semantic safety check on the existing compiler's erasure,
    # PLUS the type-aware check above: erasure alone cannot validate type safety.
    black.assert_equivalent(before["erased"], after["erased"])


def format_source(source, width=88, *, magic_trailing_comma=True):
    mode = black.Mode(line_length=width, string_normalization=False,
                      magic_trailing_comma=magic_trailing_comma)
    output = black.format_str(source, mode=mode)
    validate_output(source, output)
    black.assert_stable(source, output, mode)
    return output


if __name__ == "__main__":
    print(format_source(sys.stdin.read()), end="")

class User:
    def __init__(self) -> None:
        pass
