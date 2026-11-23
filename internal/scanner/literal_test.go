package scanner

import (
	"encoding/json"
	"testing"
	"time"
)

func parseJSONish(t *testing.T, src string, l lang) any {
	t.Helper()
	v, ok := toJSONValue(newLitParser(src, 0, l).value())
	if !ok {
		return "<unknown>"
	}
	return v
}

func jsonString(v any) string {
	if s, ok := v.(string); ok && s == "<unknown>" {
		return `"<unknown>"`
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLiteralParser(t *testing.T) {
	cases := []struct {
		name string
		lang lang
		src  string
		want string
	}{
		{"python dict", langPython, `{"type": "object", 'required': ["city"], "x": True, "y": None, "n": -1.5e2,}`,
			`{"n":-150,"required":["city"],"type":"object","x":true,"y":null}`},
		{"python implicit concat and triple quotes", langPython, `["a" 'b', """multi
line""", r"raw\d"]`, `["ab","multi\nline","raw\\d"]`},
		{"python comments", langPython, "{ # comment\n 'a': 1, # more\n}", `{"a":1}`},
		{"js bare keys, template, comments", langJS, "{ type: 'object', /* c */ desc: `tmpl`, // x\n n: 2 }",
			`{"desc":"tmpl","n":2,"type":"object"}`},
		{"js concat and as const", langJS, `{ a: "x" + 'y', b: ["p", "q"] as const }`, `{"a":"xy","b":["p","q"]}`},
		{"js escapes", langJS, `"a\"b\né"`, `"a\"b\né"`},
		{"rust json macro", langRust, `json!({"type": "object", "properties": {"url": {"type": "string"}}})`,
			`{"properties":{"url":{"type":"string"}},"type":"object"}`},
		{"rust into", langRust, `"hello".into()`, `"hello"`},
		{"rust String::from", langRust, `String::from("hi")`, `"hi"`},
		{"variable is unknown", langJS, `{ a: someVar }`, `"<unknown>"`},
		{"spread is unknown", langJS, `{ ...base, a: 1 }`, `"<unknown>"`},
		{"template substitution is unknown", langJS, "`x ${y} z`", `"<unknown>"`},
		{"ternary is unknown", langJS, `{ a: cond ? "x" : "y" }`, `"<unknown>"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonString(parseJSONish(t, tc.src, tc.lang)); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestLiteralParserKeepsKnownFieldsBesideUnknownOnes(t *testing.T) {
	src := `{ name: "get_weather", handler: async ({ city }) => { return { ok: true }; }, schema: buildSchema(), description: "Weather." }`
	m, ok := newLitParser(src, 0, langJS).value().(map[string]any)
	if !ok {
		t.Fatal("not an object")
	}
	if m["name"] != "get_weather" || m["description"] != "Weather." {
		t.Errorf("lost known fields: %#v", m)
	}
	if _, ok := m["schema"].(*chainValue); !ok {
		t.Errorf("schema = %#v, want call chain", m["schema"])
	}
}

func TestLiteralParserChains(t *testing.T) {
	v := newLitParser(`z.string().url().describe("The URL")`, 0, langJS).value()
	ch, ok := v.(*chainValue)
	if !ok || ch.chainName() != "z.string.url.describe" || ch.segments[3].args[0] != "The URL" {
		t.Fatalf("chain = %#v", v)
	}
	v = newLitParser(`Field(description="City name", default="x")`, 0, langPython).value()
	ch, ok = v.(*chainValue)
	if !ok || ch.segments[0].kwargs["description"] != "City name" {
		t.Fatalf("kwargs chain = %#v", v)
	}
	v = newLitParser(`z.string().regex(/^https:\/\/[a-z]+$/i)`, 0, langJS).value()
	if ch, ok := v.(*chainValue); !ok || ch.segments[2].args[0] != (regexValue{pattern: `^https:\/\/[a-z]+$`}) {
		t.Fatalf("regex chain = %#v", v)
	}
}

func TestEnclosingOpen(t *testing.T) {
	src := `x = [{"a": "}", "inputSchema": {}}, f("inputSchema")]`
	targets := []int{17, 41}
	got := enclosingOpen(src, langPython, targets)
	if got[0] != 5 || got[1] != -1 {
		t.Errorf("got %v", got)
	}
}

// TestLiteralParserTerminates guards against loops that stop making
// progress on ';' or stray closers.
func TestLiteralParserTerminates(t *testing.T) {
	inputs := []struct {
		src  string
		lang lang
	}{
		{"[0; 4]", langRust},
		{"{ let a = 1; a }", langRust},
		{"f({ x; y })", langRust},
		{"{ a: string; b: number }", langJS},
		{"type T = { a: string; b?: number[] }; const x = {", langJS},
		{"([)]", langJS},
		{"{ 'a': [1, 2; ", langPython},
	}
	for _, in := range inputs {
		done := make(chan struct{})
		go func() {
			defer close(done)
			newLitParser(in.src, 0, in.lang).value()
			enclosingOpen(in.src, in.lang, []int{len(in.src) / 2})
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("parser did not terminate on %q", in.src)
		}
	}
}
