package cnb_test

import (
	"testing"

	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/pivotal/kpack/pkg/cnb"
	"github.com/pivotal/kpack/pkg/registry/imagehelpers"
	"github.com/pivotal/kpack/pkg/registry/registryfakes"
	"github.com/sclevine/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataRetriever(t *testing.T) {
	spec.Run(t, "Metadata Retriever", testMetadataRetriever)
}

func testMetadataRetriever(t *testing.T, when spec.G, it spec.S) {
	var (
		imageFetcher = registryfakes.NewFakeClient()
	)

	when("RemoteMetadataRetriever", func() {
		when("GetBuiltImage", func() {
			when("images are built with lifecycle 0.5", func() {
				it("retrieves the metadata from the registry", func() {
					appImageKeychain := &registryfakes.FakeKeychain{}

					appImage := randomImage(t)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.build.metadata", `{"buildpacks": [{"id": "test.id", "version": "1.2.3"}]}`)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.lifecycle.metadata", `{
  "app": {
    "sha": "sha256:119f3f610dade1fdf5b4b2473aea0c6b1317497cf20691ab6d184a9b2fa5c409"
  },
  "runImage": {
    "topLayer": "sha256:719f3f610dade1fdf5b4b2473aea0c6b1317497cf20691ab6d184a9b2fa5c409",
    "reference": "localhost:5000/node@sha256:0fd6395e4fe38a0c089665cbe10f52fb26fc64b4b15e672ada412bd7ab5499a0"
  },
  "stack": {
    "runImage": {
      "image": "gcr.io:443/run:full-cnb"
    }
  }
}`)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.stack.id", "io.buildpacks.stack.bionic")
					imageFetcher.AddImage("reg.io/appimage/name", appImage, appImageKeychain)

					subject := cnb.RemoteMetadataRetriever{
						Keychain:     appImageKeychain,
						ImageFetcher: imageFetcher,
					}

					result, err := subject.GetBuiltImage("reg.io/appimage/name")
					assert.NoError(t, err)

					metadata := result.BuildpackMetadata
					require.Len(t, metadata, 1)
					assert.Equal(t, "test.id", metadata[0].ID)
					assert.Equal(t, "1.2.3", metadata[0].Version)

					createdAtTime, err := imagehelpers.GetCreatedAt(appImage)
					assert.NoError(t, err)

					assert.Equal(t, createdAtTime, result.CompletedAt)
					assert.Equal(t, "gcr.io:443/run@sha256:0fd6395e4fe38a0c089665cbe10f52fb26fc64b4b15e672ada412bd7ab5499a0", result.Stack.RunImage)
					assert.Equal(t, "io.buildpacks.stack.bionic", result.Stack.ID)

					digest, err := appImage.Digest()
					require.NoError(t, err)
					assert.Equal(t, "reg.io/appimage/name@"+digest.String(), result.Identifier)
				})
			})

			when("images are built with lifecycle 0.6+", func() {

				it("retrieves the metadata from the registry", func() {
					appImageKeychain := &registryfakes.FakeKeychain{}

					appImage := randomImage(t)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.build.metadata", `{"buildpacks": [{"id": "test.id", "version": "1.2.3"}]}`)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.lifecycle.metadata", `{
  "app": [
    {
      "sha": "sha256:919f3f610dade1fdf5b4b2473aea0c6b1317497cf20691ab6d184a9b2fa5c409"
    },
    {
      "sha": "sha256:119f3f610dade1fdf5b4b2473aea0c6b1317497cf20691ab6d184a9b2fa5c409"
    }
  ],
  "runImage": {
    "topLayer": "sha256:719f3f610dade1fdf5b4b2473aea0c6b1317497cf20691ab6d184a9b2fa5c409",
    "reference": "localhost:5000/node@sha256:0fd6395e4fe38a0c089665cbe10f52fb26fc64b4b15e672ada412bd7ab5499a0"
  },
  "stack": {
    "runImage": {
      "image": "gcr.io:443/run:full-cnb"
    }
  }
}`)
					appImage, _ = imagehelpers.SetStringLabel(appImage, "io.buildpacks.stack.id", "io.buildpacks.stack.bionic")
					imageFetcher.AddImage("reg.io/appimage/name", appImage, appImageKeychain)

					subject := cnb.RemoteMetadataRetriever{
						Keychain:     appImageKeychain,
						ImageFetcher: imageFetcher,
					}

					result, err := subject.GetBuiltImage("reg.io/appimage/name")
					assert.NoError(t, err)

					metadata := result.BuildpackMetadata
					require.Len(t, metadata, 1)
					assert.Equal(t, "test.id", metadata[0].ID)
					assert.Equal(t, "1.2.3", metadata[0].Version)

					createdAtTime, err := imagehelpers.GetCreatedAt(appImage)
					assert.NoError(t, err)

					assert.Equal(t, createdAtTime, result.CompletedAt)
					assert.Equal(t, "gcr.io:443/run@sha256:0fd6395e4fe38a0c089665cbe10f52fb26fc64b4b15e672ada412bd7ab5499a0", result.Stack.RunImage)
					assert.Equal(t, "io.buildpacks.stack.bionic", result.Stack.ID)

					digest, err := appImage.Digest()
					require.NoError(t, err)
					assert.Equal(t, "reg.io/appimage/name@"+digest.String(), result.Identifier)
				})
			})
		})
	})
}

func randomImage(t *testing.T) ggcrv1.Image {
	image, err := random.Image(5, 10)
	require.NoError(t, err)
	return image
}
