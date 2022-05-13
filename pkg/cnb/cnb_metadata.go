package cnb

import (
	"time"

	lifecyclebuildpack "github.com/buildpacks/lifecycle/buildpack"
	"github.com/buildpacks/lifecycle/platform"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/pivotal/kpack/pkg/registry/imagehelpers"
	"github.com/pkg/errors"
)

type ImageFetcher interface {
	Fetch(keychain authn.Keychain, repoName string) (ggcrv1.Image, string, error)
}

type RemoteMetadataRetriever struct {
	Keychain     authn.Keychain
	ImageFetcher ImageFetcher
}

func (r *RemoteMetadataRetriever) GetBuiltImage(tag string) (BuiltImage, error) {
	appImage, appImageId, err := r.ImageFetcher.Fetch(r.Keychain, tag)
	if err != nil {
		return BuiltImage{}, errors.Wrap(err, "unable to fetch app image")
	}

	return readBuiltImage(appImage, appImageId)
}

func (r *RemoteMetadataRetriever) GetCacheImage(cacheTag string) (string, error) {
	_, cacheImageId, err := r.ImageFetcher.Fetch(r.Keychain, cacheTag)
	if err != nil {
		return "", errors.Wrap(err, "unable to fetch cache image")
	}

	return cacheImageId, nil
}

type BuiltImage struct {
	Identifier        string
	CompletedAt       time.Time
	BuildpackMetadata []lifecyclebuildpack.GroupBuildpack
	Stack             BuiltImageStack
}

func readBuiltImage(appImage ggcrv1.Image, appImageId string) (BuiltImage, error) {
	stackId, err := imagehelpers.GetStringLabel(appImage, platform.StackIDLabel)
	if err != nil {
		return BuiltImage{}, nil
	}

	var buildMetadata platform.BuildMetadata
	err = imagehelpers.GetLabel(appImage, platform.BuildMetadataLabel, &buildMetadata)
	if err != nil {
		return BuiltImage{}, err
	}

	var layerMetadata appLayersMetadata
	err = imagehelpers.GetLabel(appImage, platform.LayerMetadataLabel, &layerMetadata)
	if err != nil {
		return BuiltImage{}, err
	}

	imageCreatedAt, err := imagehelpers.GetCreatedAt(appImage)
	if err != nil {
		return BuiltImage{}, err
	}

	runImageRef, err := name.ParseReference(layerMetadata.RunImage.Reference)
	if err != nil {
		return BuiltImage{}, err
	}

	baseImageRef, err := name.ParseReference(layerMetadata.Stack.RunImage.Image)
	if err != nil {
		return BuiltImage{}, err
	}

	return BuiltImage{
		Identifier:        appImageId,
		CompletedAt:       imageCreatedAt,
		BuildpackMetadata: buildMetadata.Buildpacks,
		Stack: BuiltImageStack{
			RunImage: baseImageRef.Context().String() + "@" + runImageRef.Identifier(),
			ID:       stackId,
		},
	}, nil
}

type appLayersMetadata struct {
	RunImage runImageAppMetadata `json:"runImage" toml:"run-image"`
	Stack    StackMetadata       `json:"stack" toml:"stack"`
}

type runImageAppMetadata struct {
	TopLayer  string `json:"topLayer" toml:"top-layer"`
	Reference string `json:"reference" toml:"reference"`
}
