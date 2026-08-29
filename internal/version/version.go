// Package version carries the build identity, injected at link time.
//
// The values are set with -ldflags -X rather than derived from the VCS,
// because the .git directory is not in the Docker build context and
// -buildvcs would fail the build rather than quietly omit the stamp.
package version

var (
	// Version is the release tag, or "dev" for an untagged build.
	Version = "dev"

	// Commit is the full commit SHA the binary was built from.
	Commit = ""

	// Date is the RFC 3339 build timestamp.
	Date = ""
)
