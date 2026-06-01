package registry

import (
	"github.com/CurtMeadows/straddler/internal/config"
	"github.com/google/go-containerregistry/pkg/authn"
)

// BuildKeychain returns an authn.Keychain for the given registry credentials.
//
// Resolution order:
//  1. If both Username and Password are non-empty, use them as static credentials.
//     This covers Docker Hub, GHCR (PAT), Quay, Harbor, and ECR/GCR when the
//     caller has already obtained a token and placed it in the password field.
//  2. Otherwise fall back to authn.DefaultKeychain, which reads
//     ~/.docker/config.json and invokes any registered credential helpers
//     (docker-credential-ecr-login, docker-credential-gcr, etc.).
//
// Source and destination each get their own keychain so they can have
// completely independent credentials — e.g. pull from Docker Hub anonymously
// and push to a private ECR registry with an AWS token.
func BuildKeychain(creds config.RegistryCredentials) authn.Keychain {
	if creds.Username != "" && creds.Password != "" {
		return &staticKeychain{
			auth: authn.FromConfig(authn.AuthConfig{
				Username: creds.Username,
				Password: creds.Password,
			}),
		}
	}
	return authn.DefaultKeychain
}

// staticKeychain returns the same authenticator for every registry resource.
// Used when the operator has provided explicit credentials in config — we
// trust them to have given the right creds for their chosen registry.
type staticKeychain struct {
	auth authn.Authenticator
}

func (k *staticKeychain) Resolve(_ authn.Resource) (authn.Authenticator, error) {
	return k.auth, nil
}
