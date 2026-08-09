// Package version holds lazysql's build version. It defaults to "dev" for
// `go build`/`go run`; release binaries get the real tag injected via
// -ldflags (see .goreleaser.yaml).
package version

var Version = "dev"
