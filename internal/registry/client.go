package registry

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// RemoteClient implements Client using go-containerregistry.
//
// Images are never written to local disk — blobs are streamed on demand from
// the source registry directly to the destination registry using the OCI
// Distribution Spec's blob/manifest HTTP API.
type RemoteClient struct {
	srcKeychain authn.Keychain
	dstKeychain authn.Keychain
	transport   http.RoundTripper
}

// NewRemoteClient creates a RemoteClient.
// srcKeychain and dstKeychain are used for the source and destination
// registries respectively — they can be different to support cross-registry
// copies with independent credentials.
// Pass nil for transport to use http.DefaultTransport.
func NewRemoteClient(srcKeychain, dstKeychain authn.Keychain, transport http.RoundTripper) *RemoteClient {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &RemoteClient{
		srcKeychain: srcKeychain,
		dstKeychain: dstKeychain,
		transport:   transport,
	}
}

// AlreadyExists reports whether dst already has the same manifest digest as src.
// A (false, nil) result means the destination tag does not exist — safe to copy.
// A (true, nil) result means the content is already there — copy can be skipped.
func (c *RemoteClient) AlreadyExists(ctx context.Context, src, dst string) (bool, error) {
	srcRef, err := name.ParseReference(src)
	if err != nil {
		return false, fmt.Errorf("parse source reference %q: %w", src, err)
	}

	dstRef, err := name.ParseReference(dst)
	if err != nil {
		return false, fmt.Errorf("parse destination reference %q: %w", dst, err)
	}

	srcOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(c.srcKeychain),
		remote.WithTransport(c.transport),
	}
	dstOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(c.dstKeychain),
		remote.WithTransport(c.transport),
	}

	srcDesc, err := remote.Get(srcRef, srcOpts...)
	if err != nil {
		return false, fmt.Errorf("get source manifest for %q: %w", src, err)
	}

	dstDesc, err := remote.Get(dstRef, dstOpts...)
	if err != nil {
		// A 404 (not found) or 401/403 from a tag that doesn't exist yet is the
		// normal "not there yet" case — treat it as (false, nil) so the copy proceeds.
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get destination manifest for %q: %w", dst, err)
	}

	return srcDesc.Digest == dstDesc.Digest, nil
}

// isNotFound returns true when err represents a missing tag at the destination.
// This covers HTTP 404s and manifest-unknown errors from go-containerregistry.
// We treat HTTP 401 as "not found" too: some registries (e.g. Quay) return 401
// for tags that don't exist rather than 404, to avoid leaking repository contents.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	terr, ok := err.(*transport.Error)
	if !ok {
		return false
	}
	for _, e := range terr.Errors {
		if e.Code == transport.ManifestUnknownErrorCode {
			return true
		}
	}
	return terr.StatusCode == http.StatusNotFound ||
		terr.StatusCode == http.StatusUnauthorized
}

// ListTags returns all tags for the given repository.
// repo must be a fully-qualified repository without a tag.
func (c *RemoteClient) ListTags(ctx context.Context, repo string) ([]string, error) {
	ref, err := name.NewRepository(repo)
	if err != nil {
		return nil, fmt.Errorf("parse repository %q: %w", repo, err)
	}

	tags, err := remote.List(ref,
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(c.srcKeychain),
		remote.WithTransport(c.transport),
	)
	if err != nil {
		return nil, fmt.Errorf("list tags for %q: %w", repo, err)
	}

	return tags, nil
}

// Copy streams an image from src to dst.
//
// It first fetches the manifest descriptor (a lightweight HEAD/GET call) to
// determine whether src is a plain image or a multi-architecture manifest list.
// The correct copy path is then chosen:
//
//   - ImageIndex (manifest list): remote.WriteIndex copies all platform variants
//     atomically so the destination ends up with a proper multi-arch image.
//   - Single image: remote.Write streams each layer from source to destination
//     on demand — nothing is buffered locally.
func (c *RemoteClient) Copy(ctx context.Context, src, dst string) error {
	srcRef, err := name.ParseReference(src)
	if err != nil {
		return fmt.Errorf("parse source reference %q: %w", src, err)
	}

	dstRef, err := name.ParseReference(dst)
	if err != nil {
		return fmt.Errorf("parse destination reference %q: %w", dst, err)
	}

	srcOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(c.srcKeychain),
		remote.WithTransport(c.transport),
	}
	dstOpts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(c.dstKeychain),
		remote.WithTransport(c.transport),
	}

	// Fetch the descriptor to determine the manifest media type.
	// This is a single HTTP request — no blob data is transferred yet.
	desc, err := remote.Get(srcRef, srcOpts...)
	if err != nil {
		return fmt.Errorf("fetch manifest for %q: %w", src, err)
	}

	if desc.MediaType.IsIndex() {
		// Multi-arch manifest list — copy all platform variants as one unit.
		idx, err := desc.ImageIndex()
		if err != nil {
			return fmt.Errorf("load image index for %q: %w", src, err)
		}
		if err := remote.WriteIndex(dstRef, idx, dstOpts...); err != nil {
			return fmt.Errorf("push image index %q → %q: %w", src, dst, err)
		}
		return nil
	}

	// Single-platform image — stream layers on demand.
	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("load image for %q: %w", src, err)
	}
	if err := remote.Write(dstRef, img, dstOpts...); err != nil {
		return fmt.Errorf("push image %q → %q: %w", src, dst, err)
	}

	return nil
}
