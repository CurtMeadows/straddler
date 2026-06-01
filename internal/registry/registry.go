// Package registry defines the interface for interacting with OCI-compliant
// container registries and provides a go-containerregistry-backed implementation.
//
// The Client interface is the only thing the rest of the application depends on,
// which keeps the registry logic isolated and the interface easy to fake in tests.
package registry

import "context"

// Client is the primary abstraction over container registry operations.
// Implementations must be safe for concurrent use by multiple goroutines.
type Client interface {
	// ListTags returns all tags for the given image repository.
	// repo must be a fully-qualified repository without a tag, e.g.:
	//   "docker.io/library/nginx"
	//   "ghcr.io/myorg/myimage"
	//   "123456.dkr.ecr.us-east-1.amazonaws.com/myimage"
	//   "quay.io/myorg/myimage"
	//   "harbor.example.com/project/myimage"
	ListTags(ctx context.Context, repo string) ([]string, error)

	// Copy streams an image from src to dst without materialising it on disk.
	// Both must be fully-qualified references including a tag or digest, e.g.:
	//   src: "docker.io/library/nginx:1.25"
	//   dst: "ghcr.io/myorg/nginx:1.25"
	//
	// Multi-architecture manifest lists (ImageIndex) are handled transparently —
	// all platform variants are copied as a single atomic operation.
	Copy(ctx context.Context, src, dst string) error

	// AlreadyExists reports whether dst already contains the same manifest as src.
	// Returns (true, nil) if and only if both references resolve and their
	// manifest digests are identical — meaning the copy can be safely skipped.
	// Returns (false, nil) when the destination tag does not exist yet.
	// Returns (false, err) only on unexpected errors (auth failure, network, etc.).
	AlreadyExists(ctx context.Context, src, dst string) (bool, error)
}
