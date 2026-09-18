package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptOutlineIgnoresControlFlow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.ts")
	src := "export const good = () => {\n" +
		"  if (condition) {\n" +
		"    for (const item of items) {\n" +
		"      if (item) {\n" +
		"        work(item);\n" +
		"      }\n" +
		"    }\n" +
		"  } else if (fallback) {\n" +
		"    recover();\n" +
		"  }\n" +
		"};\n\n" +
		"export const multiline = async ({\n" +
		"  user_id,\n" +
		"}: Params): Promise<string | null> => {\n" +
		"  return null;\n" +
		"};\n\n" +
		"// This top-level declaration must end the function above.\n" +
		"const typedValues: string[] = [\n" +
		"  \"seller\",\n" +
		"];\n\n" +
		"class Thing {\n" +
		"  method(arg) {\n" +
		"    if (arg) {\n" +
		"      work(arg);\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	syms, err := Outline(path, "sample.ts")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"func:good", "func:multiline", "const:typedValues", "class:Thing", "method:method"}
	if len(syms) != len(want) {
		t.Fatalf("outline = %v, want %v", syms, want)
	}
	for i, sym := range syms {
		got := sym.Kind + ":" + sym.Name
		if got != want[i] {
			t.Errorf("outline[%d] = %q, want %q", i, got, want[i])
		}
	}
}
