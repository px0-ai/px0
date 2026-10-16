module px0

go 1.25

require (
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/px0-ai/harness v0.0.1
	github.com/yuin/goldmark v1.8.6
)

require github.com/dlclark/regexp2/v2 v2.2.1 // indirect

// px0 carries its own copy of the harness module so it can ship the fx preset
// before it is released upstream. Drop this line once px0-ai/harness has fx.
replace github.com/px0-ai/harness => ./internal/harness
