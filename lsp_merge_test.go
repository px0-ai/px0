package main

import (
	"reflect"
	"testing"
)

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name string
		base map[string]any
		user map[string]any
		want map[string]any
	}{
		{
			name: "deep merge objects",
			base: map[string]any{
				"analyses": map[string]any{
					"unusedparams": true,
					"unusedwrite":  true,
				},
			},
			user: map[string]any{
				"analyses": map[string]any{
					"unusedparams": false,
				},
			},
			want: map[string]any{
				"analyses": map[string]any{
					"unusedparams": false,
					"unusedwrite":  true,
				},
			},
		},
		{
			name: "array replacement",
			base: map[string]any{
				"directoryFilters": []string{"-vendor", "-generated"},
			},
			user: map[string]any{
				"directoryFilters": []string{"-vendor"},
			},
			want: map[string]any{
				"directoryFilters": []string{"-vendor"},
			},
		},
		{
			name: "primitive override",
			base: map[string]any{
				"staticcheck": false,
			},
			user: map[string]any{
				"staticcheck": true,
			},
			want: map[string]any{
				"staticcheck": true,
			},
		},
		{
			name: "different types",
			base: map[string]any{
				"foo": map[string]any{"a": 1},
			},
			user: map[string]any{
				"foo": "bar",
			},
			want: map[string]any{
				"foo": "bar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepMerge(tt.base, tt.user)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("deepMerge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeepMergeAliasing(t *testing.T) {
	base := map[string]any{
		"nested": map[string]any{
			"a": 1,
		},
	}
	user := map[string]any{}

	got := deepMerge(base, user)

	// Mutate the nested map in the result
	nestedGot := got["nested"].(map[string]any)
	nestedGot["a"] = 2
	nestedGot["b"] = 3

	// Verify base is unmodified
	nestedBase := base["nested"].(map[string]any)
	if nestedBase["a"] != 1 {
		t.Errorf("base map was mutated: a=%v, want 1", nestedBase["a"])
	}
	if _, ok := nestedBase["b"]; ok {
		t.Errorf("base map was mutated: b exists")
	}
}

func TestDeepMergeSliceAliasing(t *testing.T) {
	base := map[string]any{
		"items": []any{
			map[string]any{"foo": "bar"},
		},
	}

	got := deepMerge(base, nil)

	// Mutate the nested map in the result
	itemsGot := got["items"].([]any)
	item0 := itemsGot[0].(map[string]any)
	item0["foo"] = "changed"

	// Verify base is unmodified
	itemsBase := base["items"].([]any)
	itemBase0 := itemsBase[0].(map[string]any)
	if itemBase0["foo"] != "bar" {
		t.Errorf("base map in slice was mutated: foo=%v, want bar", itemBase0["foo"])
	}
}
