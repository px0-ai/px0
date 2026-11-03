package harness

import (
	"slices"
	"strings"
)

// Placeholder is the token in a command template replaced by the prompt.
// In a custom template, ModelPlaceholder is replaced by the model.
const (
	Placeholder      = "{prompt}"
	ModelPlaceholder = "{model}"
)

// Preset is a harness this package knows and the argv that runs it headless.
// Every preset must be able to list its models (see listers in models.go).
// It holds no model names: the models a harness offers change often, so ask the
// installed harness with DiscoverModels. With no model chosen, the model flag is
// left out and the harness uses its own configured model.
// Each of these starts an interactive session by default and would wait forever
// for approval, so every preset carries the flag that turns that off and the
// one that lets it apply edits without asking.
type Preset struct {
	Name      string
	Args      []string // argv template; Args[0] is the binary, one element is Placeholder
	ModelFlag string
}

var presets = []Preset{
	{
		Name:      "claude",
		Args:      []string{"claude", "--permission-mode", "acceptEdits", "-p", Placeholder},
		ModelFlag: "--model",
	},
	{
		Name:      "cursor-agent",
		Args:      []string{"cursor-agent", "--force", "-p", Placeholder},
		ModelFlag: "--model",
	},
	{
		Name:      "agy",
		Args:      []string{"agy", "--dangerously-skip-permissions", "--mode", "accept-edits", "-p", Placeholder},
		ModelFlag: "--model",
	},
	{
		Name:      "opencode",
		Args:      []string{"opencode", "run", Placeholder},
		ModelFlag: "-m",
	},
	{
		// "--" ends flag parsing so a prompt that starts with "-" stays a prompt.
		// --auto lets fx review its own permission prompts, as there is no TTY to ask.
		Name:      "fx",
		Args:      []string{"fx", "ask", "--auto", "--", Placeholder},
		ModelFlag: "--model",
	},
}

// Presets returns a copy of the built-in presets, in picker order.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	for i, p := range presets {
		p.Args = slices.Clone(p.Args)
		out[i] = p
	}
	return out
}

// PresetNames lists the built-in preset names.
func PresetNames() []string {
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}
	return names
}

// LookupPreset finds a preset by name, case-insensitively.
func LookupPreset(name string) (Preset, bool) {
	name = strings.TrimSpace(name)
	for _, p := range Presets() {
		if strings.EqualFold(name, p.Name) {
			return p, true
		}
	}
	return Preset{}, false
}
