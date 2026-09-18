"""Exercise typed layout rules, native equivalence, and stock Black compatibility."""

import json
import os
import subprocess
import time
import unittest

from adapter import BUILD, analyze, black, format_source, validate_output

# These test actual source syntax, not Python-encoded approximations.
CASES = {
    "annotation_only_classes": 'class User:\n id:str\n\nclass Me(User):\n id:"Shloimy"\n',
    "short_annotation_only_suite": "class A:\n x:T\n",
    "nested_annotation_only_suite": "def f():\n if True:\n  value:str\n",
    "generic_lambda": "identity=lambda<T extends str> value:T:value\n",
    "callable_lambda_parameter": "invoke=lambda callback:()->str:callback()\n",
    "nested_typed_lambdas": "outer=lambda x:T:lambda y:U:y\n",
    "multiple_lambda_parameters": "pair=lambda first:T,second:str:first as str\n",
    "generic_callable_parameter": "invoke=lambda<T extends str> callback:(value:T)->T,value:T:callback(value)\n",
    "lambda_defaults": 'f=lambda first:str="value",*,second:int=1:first\n',
    "lambda_dict_body": 'f=lambda value:str:{"name":value}\n',
    "lambda_comprehension": "names=[(lambda value:str:value.upper())(item) for item in users]\n",
    "lambda_unicode": 'label="😀"\nf=lambda value:str:value\n',
    "nested_generics": "type Nested=Box<Box<str>>\n",
    "long_generic": "type X=Box<SomeReallyLongTypeName,AnotherReallyLongTypeName,ThirdLongTypeName>\n",
    "attribute_identity": 'type Key=*<"id">|"id"\n',
    "type_utility": "type Many(T)=[]T\n",
    "tuple_and_list_prefixes": "type Pair=(int,*()str)\ntype Seq=[int,*[]str]\ntype Tuple=()(str|None)\n",
    "attribute_patterns": 'type Attrs={(*<f"get_{str}">):()->str}\n',
    "mapped_filter": "type Omit(Obj,K)={(P):Obj[P] for P in keyof Obj if (False if P extends K else True) extends True}\n",
    "mapped_required": "type Required(T)={-optional (K):T[K] for K in keyof T}\n",
    "shapes": 'type User={id:int,"display_name":str,optional "email_address":str,readonly (bytes):int}\n',
    "computed_vs_attribute": 'type Shape={id:int,"id":str,(str):str,(*<"name">):str}&Base\n',
    "shape_method": "type User={def greet(message:str)->str}\n",
    "interface_inheritance": "interface User<T>(Base):\n data:T\n def greet(message:str)->str: ...\n",
    "generic_function": "def identity<T extends str>(value:T)->T:\n return value\n",
    "generic_defaults": "def f<T extends str = str>(value:T)->T:\n return value\n",
    "constrained_infer": "type Unbox(T)=U if T extends Box<infer U extends str> else never\n",
    "conditional_callable": "type F=(x:int)->str if T extends int else bytes\n",
    "satisfies": 'user={"id":1} satisfies {"id":int}\n',
    "parameter_kinds": 'def f(a:str="ok",/,*args:()int,flag:bool=False,**kwargs:{"name":str})->str:\n return a\n',
    "comments": 'type User={\n# id attribute\nid:int,\n"name":str, # indexed name\n}\n',
    "long_lambda_header": "pair=lambda<T extends str> first:T,second:str:first as str\n",
    "runtime_generic_call": 'result=identity<str>("value")\n',
    "generic_callable_type": "type Identity=<T>(value:T)->T\n",
    "presence_assertion": 'value=user["name"]!\n',
    "ambient_function": "declare def greet(name:str)->str\n",
    "nested_runtime_generic_call": 'result=mod.identity<Box<Box<str>>, int>("value").name!\n',
    "long_runtime_generic_call": 'result=identity<SomeReallyLongTypeName,AnotherReallyLongTypeName>("a long argument value",other_value)\n',
    "presence_chain": 'value=user["name"]!.upper()\n',
    "presence_operators": "value=user.name!+other.name!\n",
    "long_generic_callable": "type Identity=<T extends SomeVeryLongTypeName,U extends AnotherVeryLongTypeName>(first:T,second:U)->(T,U)\n",
    "generic_callable_lambda": "invoke=lambda callback:<T>(value:T)->T:callback(1)\n",
    "long_ambient_function": "declare def greet<T extends SomeVeryLongTypeName>(first_name:T,last_name:str,*,greeting:str=...)->str\n",
    "ambient_ellipsis": "declare def greet(name:str)->str: ...\n",
    "long_lambda_typeparams": "pair=lambda<T extends str,U extends int> first:T,second:U:first\n",
    "long_lambda_body": "pair=lambda<T extends str> first:T:some_function(first,another_really_long_argument_name)\n",
}

CONTROLS = {
    "untyped_lambda": "identity=lambda x:str\n",
    "nested_untyped_lambdas": "outer=lambda x:lambda y:y\n",
    "untyped_lambda_dictionary": 'f=lambda value:{"name":value}\n',
    "comparisons_and_shifts": "flag=a<b>c\nshifted=value>>2\n",
    "contextual_identifiers": "optional=1\nkeyof=2\nreadonly=3\n",
    "comprehensions": "values=[n*n for n in range(10) if n%2==0]\nlookup={str(n):n for n in values}\n",
    "async": "async def collect(stream):\n async with resource() as handle:\n  return [x async for x in stream]\n",
    "with": "with resource() as handle:\n consume(handle)\n",
    "dunder_method": "class C:\n def __getitem__(self,key):\n  return self.items[key]\n",
    "comments_and_fmt_off": '# fmt: off\nx={"id":1}\n# fmt: on\ny=2 # trailing\n',
}

EXPECTED = {
    "long_lambda_header": "pair = (\n    lambda<T extends str> first: T, second: str: first as str\n)\n",
    "runtime_generic_call": 'result = identity<str>("value")\n',
    "generic_callable_type": "type Identity = <T>(value: T) -> T\n",
    "presence_assertion": 'value = user["name"]!\n',
    "ambient_function": "declare def greet(name: str) -> str\n",
    "presence_chain": 'value = user["name"]!.upper()\n',
    "presence_operators": "value = user.name! + other.name!\n",
    "ambient_ellipsis": "declare def greet(name: str) -> str: ...\n",
}

RESULTS = []


class Feasibility(unittest.TestCase):
    def test_typed_syntax(self):
        for name, source in CASES.items():
            with self.subTest(name=name):
                width = (
                    50
                    if name.startswith("long_")
                    or name in {"shapes", "mapped_filter", "generic_callable_lambda"}
                    else 88
                )
                output = format_source(source, width)
                self.assertEqual(format_source(output, width), output)
                self.assertNotEqual(source, output, "Must actually exercise formatting")
                if name == "shapes":
                    self.assertIn('\n    optional "email_address": str,', output)
                if name == "computed_vs_attribute":
                    self.assertIn(
                        "(str): str", output, "Computed-key parentheses are semantic"
                    )
                if name == "generic_lambda":
                    self.assertIn("lambda<T extends str> value: T: value", output)
                if name in EXPECTED:
                    self.assertEqual(output, EXPECTED[name])
                RESULTS.append(
                    {
                        "name": name,
                        "status": "formatted",
                        "width": width,
                        "input": source,
                        "output": output,
                    }
                )

    def test_ordinary_python_matches_unmodified_black(self):
        env = {
            **os.environ,
            "PYTHONPATH": str(BUILD / "vendor"),
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        for name, source in CONTROLS.items():
            with self.subTest(name=name):
                # Fresh process: no patches from adapter.py can affect this oracle.
                stock = subprocess.run(
                    [
                        "python3",
                        "-B",
                        "-c",
                        'import black,sys; print(black.format_str(sys.stdin.read(),mode=black.Mode(string_normalization=False)),end="")',
                    ],
                    input=source,
                    text=True,
                    capture_output=True,
                    check=True,
                    env=env,
                    timeout=10,
                ).stdout
                output = format_source(source)
                black.assert_equivalent(source, output)
                self.assertEqual(output, stock)
                RESULTS.append(
                    {
                        "name": name,
                        "status": "matches-stock-black",
                        "input": source,
                        "output": output,
                    }
                )

    def test_safety_rejects_type_and_runtime_changes(self):
        for before, after in [
            ("type X={id:int}\n", 'type X={"id":int}\n'),
            ('type X={"id":int}\n', 'type X={"id":str}\n'),
            ("type X={(str):int}\n", "type X={str:int}\n"),
            ("x=lambda value:str:value\n", "x=(lambda value:str:value,)\n"),
            ("x=1\n", "x=2\n"),
        ]:
            with self.subTest(before=before):
                with self.assertRaises(ValueError):
                    validate_output(before, after)

    def test_invalid_source_is_not_formatted(self):
        for source in [
            "value=name!\n",
            "type Identity=<T>(value:T)->\n",
            "result=f<str>(\n",
        ]:
            with self.subTest(source=source):
                with self.assertRaises(ValueError):
                    format_source(source)


if __name__ == "__main__":
    start = time.monotonic()
    run = unittest.TextTestRunner(verbosity=2).run(
        unittest.defaultTestLoader.loadTestsFromTestCase(Feasibility)
    )
    output = BUILD / "results"
    output.mkdir(parents=True, exist_ok=True)
    for result in RESULTS:
        if "output" in result:
            (output / (result["name"] + ".ty")).write_text(result["output"])
    report = {
        "black": black.__version__,
        "success": run.wasSuccessful(),
        "elapsed_seconds": round(time.monotonic() - start, 3),
        "cases": RESULTS,
    }
    (BUILD / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"Report: {BUILD / 'report.json'}")
    print(f"Formatted examples: {output}")
    raise SystemExit(0 if run.wasSuccessful() else 1)
