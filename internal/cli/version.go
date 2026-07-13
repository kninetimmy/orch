package cli

// Version is the release version of this binary, stamped by the
// release workflow via
//
//	-ldflags "-X github.com/kninetimmy/orch/internal/cli.Version=v1.2.3"
//
// Builds without the stamp (go build, go install, go test) report
// "dev". status and doctor print it before any repository checks, so
// an uninitialized or broken repository can still identify the binary
// on PATH.
var Version = "dev"
