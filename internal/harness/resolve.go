package harness

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Command is a resolved, runnable harness invocation.
type Command struct {
	Name  string   // preset name, or the binary's base name for a custom template
	Args  []string // argv with Args[0] an absolute path; still contains Placeholder
	Model string
}

// Resolve is shorthand for the package-level Resolve(h.Name, h.Model). It
// fails if the harness is not installed.
func (h Harness) Resolve() (Command, error) {
	return Resolve(h.Name, h.Model)
}

// Resolve turns a preset name or a command template into a Command, and
// verifies the binary exists now rather than at first use.
//
// model may be empty, in which case no model flag is passed and the harness
// uses its own configured model. A template is a whitespace-separated argv that
// must contain Placeholder; it may contain ModelPlaceholder, but then model
// must not be empty.
func Resolve(spec, model string) (Command, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Command{}, errors.New("empty harness")
	}

	var c Command
	if p, ok := LookupPreset(spec); ok {
		c = Command{Name: p.Name, Model: model, Args: presetArgs(p, model)}
	} else {
		args := strings.Fields(spec)
		if !strings.Contains(spec, Placeholder) {
			return Command{}, fmt.Errorf("a command template must contain %s (known harnesses: %s)",
				Placeholder, strings.Join(PresetNames(), ", "))
		}
		if strings.Contains(spec, ModelPlaceholder) && model == "" {
			return Command{}, fmt.Errorf("the template uses %s but no model was given", ModelPlaceholder)
		}
		for i, a := range args {
			args[i] = strings.ReplaceAll(a, ModelPlaceholder, model)
		}
		c = Command{Name: filepath.Base(args[0]), Model: model, Args: args}
	}

	bin, ok := LookPath(c.Args[0])
	if !ok {
		return Command{}, fmt.Errorf("%s is not installed", c.Args[0])
	}
	c.Args[0] = bin
	return c, nil
}

// presetArgs inserts the model flag (when there is a model) just before the
// prompt, ahead of the prompt's own flag (the "-p" in "-p {prompt}") when there is one.
func presetArgs(p Preset, model string) []string {
	prompt := slices.Index(p.Args, Placeholder)
	insert := prompt
	if prompt > 0 && strings.HasPrefix(p.Args[prompt-1], "-") {
		insert = prompt - 1
	}
	args := make([]string, 0, len(p.Args)+2)
	for i, a := range p.Args {
		if i == insert && p.ModelFlag != "" && model != "" {
			args = append(args, p.ModelFlag, model)
		}
		args = append(args, a)
	}
	return args
}
