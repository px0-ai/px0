package harness_test

import (
	"context"
	"fmt"

	"github.com/px0-ai/harness"
)

// List the known harnesses, which are installed, and the model each would use.
func ExampleDetect() {
	for _, h := range harness.Detect(nil) {
		fmt.Println(h.Name, h.Model, h.Installed)
	}
}

// Detect, then pass a harness name and model to Resolve, then Run.
func ExampleRun() {
	for _, h := range harness.Detect(nil) {
		if !h.Installed {
			continue
		}
		cmd, err := harness.Resolve(h.Name, h.Model) // or h.Resolve()
		if err != nil {
			continue
		}
		res := harness.Run(context.Background(), cmd, "rename foo to bar in main.go", harness.Options{Dir: "."})
		fmt.Println("error:", res.Err)
		fmt.Println("output:", res.Stdout)
		return
	}
}

// A custom harness is any command with a {prompt} placeholder.
func ExampleResolve_template() {
	cmd, err := harness.Resolve("mytool --model {model} --task {prompt}", "fast")
	fmt.Println(cmd.Name, err)
}
