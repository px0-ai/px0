// Package harness detects coding-agent CLIs installed on this machine (claude,
// cursor-agent, agy, opencode, codex) and runs them headless with a prompt.
//
// Typical use: detect what is installed, pick a harness and model, resolve
// them into a command, then run it.
//
//	for _, h := range harness.Detect(nil) {
//		if !h.Installed {
//			continue
//		}
//		cmd, err := harness.Resolve(h.Name, h.Model) // or h.Resolve()
//		if err != nil { ... }
//		res := harness.Run(ctx, cmd, "rename foo to bar", harness.Options{Dir: repo})
//		fmt.Println(res.Stdout, res.Err)
//		break
//	}
//
// Every built-in preset is invoked with the flags that make it non-interactive
// and let it apply edits without asking. Discovery alone never runs anything
// that edits files.
package harness
