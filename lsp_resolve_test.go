package main

import (
	"reflect"
	"testing"
)

func TestResolveInitOptions(t *testing.T) {
	def := lspServerDef{
		Name: "gopls",
		InitOptions: map[string]any{
			"analyses": map[string]any{
				"unusedparams": true,
			},
		},
	}

	// 1. No user options
	got := resolveInitOptions(def, map[string]any{})
	want := map[string]any{"analyses": map[string]any{"unusedparams": true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("No user options: got %v, want %v", got, want)
	}

	// 2. Valid user options
	raw := map[string]any{
		"lsp.gopls": map[string]any{
			"directoryFilters": []any{"-vendor"},
		},
	}
	got = resolveInitOptions(def, raw)
	want = map[string]any{
		"analyses":         map[string]any{"unusedparams": true},
		"directoryFilters": []any{"-vendor"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Valid user options: got %v, want %v", got, want)
	}

	// 3. Unknown server
	rawUnknown := map[string]any{
		"lsp.rust-analyzer": map[string]any{
			"foo": "bar",
		},
	}
	got = resolveInitOptions(def, rawUnknown)
	want = map[string]any{"analyses": map[string]any{"unusedparams": true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unknown server options: got %v, want %v", got, want)
	}

	// 4. Invalid configuration format (not a map)
	rawInvalid := map[string]any{
		"lsp.gopls": "not a map",
	}
	got = resolveInitOptions(def, rawInvalid)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Invalid format options: got %v, want %v", got, want)
	}
}
