package pyprogram

import (
	"os"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestTytestPublicAPI(t *testing.T) {
	helper, err := os.ReadFile("../../../tests/tytest.ty")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, expression string
		valid            bool
	}{
		{"string hover", `tytest("value = 1").at("value").hover("(variable) value: int")`, true},
		{"builder hover", `tytest("value = 1").hover("(variable) value: int", at="value").diagnostics("")`, true},
		{"builder line", `tytest("value = 1").hover("(variable) value: int", at=1)`, true},
		{"invalid builder target", `tytest("value = 1").hover("text", at={"line": 1})`, false},
		{"fluent selectors", `tytest("value = 1").line(1).at("value").semantic("variable")`, true},
		{"configuration", `tytest("value = 1").configure(hover_verbosity=1).at("value").hover("text")`, true},
		{"suggestions", `tytest("val").point(1, 4).suggest("value", match="includes", ordered=False)`, true},
		{"object hover rejected", `tytest("value = 1").at("value").hover({"prefix": "str"})`, false},
		{"invalid matching mode", `tytest("val").point(1, 4).suggest("value", match="include")`, false},
		{"invalid semantic kind", `tytest("value = 1").at("value").semantic("madeup")`, false},
		{"invalid coordinates", `tytest("value = 1").point("one", 2)`, false},
		{"unknown setting", `tytest("value = 1").configure(unknown_setting=True)`, false},
		{"unknown method", `tytest("value = 1").hovre("text")`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := Open(t.Context(), []python.SourceInput{
				{FileName: "/tests/tytest.ty", Text: string(helper)},
				{FileName: "/tests/api.ty", Text: "from tytest import tytest\n" + test.expression + "\n"},
			})
			defer session.Close()
			diagnostics := session.Diagnostics()
			if test.valid && len(diagnostics) != 0 {
				t.Fatalf("valid API use rejected: %+v", diagnostics)
			}
			if !test.valid && len(diagnostics) == 0 {
				t.Fatal("invalid API use accepted")
			}
		})
	}
}
