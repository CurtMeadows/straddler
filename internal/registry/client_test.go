package registry

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	ggregistry "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startRegistry creates a local in-memory OCI registry for testing and returns
// its base URL (e.g. "127.0.0.1:PORT"). The registry is shut down when the
// test completes.
func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(ggregistry.New())
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// newTestClient returns a RemoteClient that talks to the given registry host
// using anonymous auth and http.DefaultTransport.
func newTestClient() *RemoteClient {
	return NewRemoteClient(authn.DefaultKeychain, authn.DefaultKeychain, nil)
}

// pushRandomImage pushes a random single-platform image to ref and returns
// the digest string. Panics on error to keep test setup concise.
func pushRandomImage(t *testing.T, ref string) string {
	t.Helper()
	img, err := random.Image(1024, 2)
	require.NoError(t, err)

	r, err := name.ParseReference(ref)
	require.NoError(t, err)

	err = remote.Write(r, img, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	require.NoError(t, err)

	digest, err := crane.Digest(ref)
	require.NoError(t, err)
	return digest
}

// ── ListTags ─────────────────────────────────────────────────────────────────

func TestListTags_ReturnsTags(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/myimage"
	ctx := context.Background()

	// Push two tagged images.
	pushRandomImage(t, repo+":v1.0")
	pushRandomImage(t, repo+":v2.0")

	client := newTestClient()
	tags, err := client.ListTags(ctx, repo)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"v1.0", "v2.0"}, tags)
}

func TestListTags_EmptyRepo(t *testing.T) {
	host := startRegistry(t)
	ctx := context.Background()

	client := newTestClient()
	_, err := client.ListTags(ctx, host+"/does-not-exist")
	// An empty or missing repo returns an error from the registry.
	assert.Error(t, err)
}

func TestListTags_InvalidReference(t *testing.T) {
	client := newTestClient()
	_, err := client.ListTags(context.Background(), ":::bad::ref:::")
	assert.Error(t, err)
}

// ── Copy ─────────────────────────────────────────────────────────────────────

func TestCopy_SingleImage(t *testing.T) {
	host := startRegistry(t)
	src := host + "/source:v1.0"
	dst := host + "/dest:v1.0"
	ctx := context.Background()

	pushRandomImage(t, src)

	client := newTestClient()
	err := client.Copy(ctx, src, dst)
	require.NoError(t, err)

	// Verify dst exists and has the same digest as src.
	srcDigest, err := crane.Digest(src)
	require.NoError(t, err)
	dstDigest, err := crane.Digest(dst)
	require.NoError(t, err)

	assert.Equal(t, srcDigest, dstDigest, "source and destination digests should match after copy")
}

func TestCopy_InvalidSourceRef(t *testing.T) {
	client := newTestClient()
	err := client.Copy(context.Background(), ":::bad", "localhost:5000/dst:tag")
	assert.Error(t, err)
}

func TestCopy_InvalidDestRef(t *testing.T) {
	host := startRegistry(t)
	src := host + "/source:v1.0"
	pushRandomImage(t, src)

	client := newTestClient()
	err := client.Copy(context.Background(), src, ":::bad")
	assert.Error(t, err)
}

// ── AlreadyExists ─────────────────────────────────────────────────────────────

func TestAlreadyExists_SameDigest(t *testing.T) {
	host := startRegistry(t)
	src := host + "/img:v1.0"
	dst := host + "/img:v1.0-copy"
	ctx := context.Background()

	pushRandomImage(t, src)

	// Copy to dst first, then check.
	client := newTestClient()
	require.NoError(t, client.Copy(ctx, src, dst))

	exists, err := client.AlreadyExists(ctx, src, dst)
	require.NoError(t, err)
	assert.True(t, exists, "should report already exists when digests match")
}

func TestAlreadyExists_DifferentDigest(t *testing.T) {
	host := startRegistry(t)
	src := host + "/img:v1.0"
	dst := host + "/img:v2.0"
	ctx := context.Background()

	// Push two different images to src and dst.
	pushRandomImage(t, src)
	pushRandomImage(t, dst)

	client := newTestClient()
	exists, err := client.AlreadyExists(ctx, src, dst)
	require.NoError(t, err)
	assert.False(t, exists, "different images should not report already exists")
}

func TestAlreadyExists_DestNotFound(t *testing.T) {
	host := startRegistry(t)
	src := host + "/img:v1.0"
	dst := host + "/img:does-not-exist"
	ctx := context.Background()

	pushRandomImage(t, src)

	client := newTestClient()
	exists, err := client.AlreadyExists(ctx, src, dst)
	require.NoError(t, err, "missing dest tag should not be an error")
	assert.False(t, exists, "should return false when destination tag does not exist")
}

func TestAlreadyExists_InvalidSource(t *testing.T) {
	client := newTestClient()
	_, err := client.AlreadyExists(context.Background(), ":::bad", "localhost:5000/img:tag")
	assert.Error(t, err)
}
