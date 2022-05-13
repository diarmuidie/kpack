package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/buildpacks/lifecycle/platform"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/authn/k8schain"
	"github.com/pkg/errors"
	"github.com/sigstore/cosign/cmd/cosign/cli/sign"

	buildapi "github.com/pivotal/kpack/pkg/apis/build/v1alpha2"
	corev1alpha1 "github.com/pivotal/kpack/pkg/apis/core/v1alpha1"
	"github.com/pivotal/kpack/pkg/cnb"
	"github.com/pivotal/kpack/pkg/cosign"
	"github.com/pivotal/kpack/pkg/dockercreds"
	"github.com/pivotal/kpack/pkg/flaghelpers"
	"github.com/pivotal/kpack/pkg/notary"
	"github.com/pivotal/kpack/pkg/reconciler/build"
	"github.com/pivotal/kpack/pkg/registry"
)

const (
	registrySecretsDir   = "/var/build-secrets"
	reportFilePath       = "/var/report/report.toml"
	notarySecretDir      = "/var/notary/v1"
	cosignSecretLocation = "/var/build-secrets/cosign"
)

var (
	cacheTag                string
	notaryV1URL             string
	dockerCredentials       flaghelpers.CredentialsFlags
	dockerCfgCredentials    flaghelpers.CredentialsFlags
	dockerConfigCredentials flaghelpers.CredentialsFlags
	cosignAnnotations       flaghelpers.CredentialsFlags
	cosignRepositories      flaghelpers.CredentialsFlags
	cosignDockerMediaTypes  flaghelpers.CredentialsFlags
	logger                  *log.Logger
)

func init() {
	flag.StringVar(&cacheTag, "cache-tag", os.Getenv(buildapi.CacheTagEnvVar), "Tag of image cache")
	flag.StringVar(&notaryV1URL, "notary-v1-url", "", "Notary V1 server url")
	flag.Var(&dockerCredentials, "basic-docker", "Basic authentication for docker of the form 'secretname=git.domain.com'")
	flag.Var(&dockerCfgCredentials, "dockercfg", "Docker Cfg credentials in the form of the path to the credential")
	flag.Var(&dockerConfigCredentials, "dockerconfig", "Docker Config JSON credentials in the form of the path to the credential")

	flag.Var(&cosignAnnotations, "cosign-annotations", "Cosign custom signing annotations")
	flag.Var(&cosignRepositories, "cosign-repositories", "Cosign signing repository of the form 'secretname=registry.example.com/project'")
	flag.Var(&cosignDockerMediaTypes, "cosign-docker-media-types", "Cosign signing with legacy docker media types of the form 'secretname=1'")
	logger = log.New(os.Stdout, "", 0)
}

func main() {
	flag.Parse()

	var report platform.ExportReport
	_, err := toml.DecodeFile(reportFilePath, &report)
	if err != nil {
		log.Fatal(err, "toml decode")
	}

	if len(report.Image.Tags) == 0 {
		log.Fatal(errors.New("no image found in report"))
	}

	builtImageRef := fmt.Sprintf("%s@%s", report.Image.Tags[0], report.Image.Digest)

	logger.Println("Loading cluster credential helpers")
	k8sKeychain, err := k8schain.New(context.Background(), nil, k8schain.Options{})
	if err != nil {
		log.Fatal(err)
	}

	creds, err := dockercreds.ParseBasicAuthSecrets(registrySecretsDir, dockerCredentials)
	if err != nil {
		log.Fatal(err)
	}

	for _, c := range append(dockerCfgCredentials, dockerConfigCredentials...) {
		credPath := filepath.Join(registrySecretsDir, c)

		dockerCfgCreds, err := dockercreds.ParseDockerConfigSecret(credPath)
		if err != nil {
			log.Fatal(err)
		}

		for domain := range dockerCfgCreds {
			logger.Printf("Loading secret for %q from secret %q at location %q", domain, c, credPath)
		}

		creds, err = creds.Append(dockerCfgCreds)
		if err != nil {
			log.Fatal(err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(errors.Wrapf(err, "error obtaining home directory"))
	}

	err = creds.Save(filepath.Join(homeDir, ".docker", "config.json"))
	if err != nil {
		log.Fatal(errors.Wrapf(err, "error writing docker creds"))
	}

	keychain := authn.NewMultiKeychain(k8sKeychain, creds)

	metadataRetriever := cnb.RemoteMetadataRetriever{
		Keychain:     keychain,
		ImageFetcher: &registry.Client{},
	}

	cacheImageRef := ""
	if cacheTag != "" {
		cacheImageRef, err = metadataRetriever.GetCacheImage(cacheTag)
		if err != nil {
			log.Fatal(err)
		}
	}

	builtImage, err := metadataRetriever.GetBuiltImage(builtImageRef)
	if err != nil {
		log.Fatal(err)
	}

	buildStatusMetadata := &build.BuildStatusMetadata{
		BuildpackMetadata: buildMetadataFromBuiltImage(builtImage),
		LatestImage:       builtImageRef,
		LatestCacheImage:  cacheImageRef,
		StackRunImage:     builtImage.Stack.RunImage,
		StackID:           builtImage.Stack.ID,
	}

	compressor := build.GzipMetadataCompressor{}
	data, err := compressor.Compress(buildStatusMetadata)
	if err != nil {
		log.Fatal(err)
	}

	if err := ioutil.WriteFile(buildapi.CompletionTerminationMessagePath, []byte(data), 0666); err != nil {
		log.Fatal(err)
	}

	if hasCosign() || notaryV1URL != "" {
		if err := signImage(report, keychain); err != nil {
			log.Fatal(err)
		}
	}

	logger.Println("Build successful")
}

func buildMetadataFromBuiltImage(image cnb.BuiltImage) corev1alpha1.BuildpackMetadataList {
	buildpackMetadata := make([]corev1alpha1.BuildpackMetadata, 0, len(image.BuildpackMetadata))
	for _, metadata := range image.BuildpackMetadata {
		buildpackMetadata = append(buildpackMetadata, corev1alpha1.BuildpackMetadata{
			Id:       metadata.ID,
			Version:  metadata.Version,
			Homepage: metadata.Homepage,
		})
	}
	return buildpackMetadata
}

func signImage(report platform.ExportReport, keychain authn.Keychain) error {
	if hasCosign() {
		cosignSigner := cosign.NewImageSigner(logger, sign.SignCmd)

		annotations, err := mapKeyValueArgs(cosignAnnotations)
		if err != nil {
			return err
		}

		repositories, err := mapKeyValueArgs(cosignRepositories)
		if err != nil {
			return err
		}

		mediaTypes, err := mapKeyValueArgs(cosignDockerMediaTypes)
		if err != nil {
			return err
		}

		if err := cosignSigner.Sign(
			context.Background(),
			report,
			cosignSecretLocation,
			annotations,
			repositories,
			mediaTypes); err != nil {
			return errors.Wrap(err, "cosign sign")
		}
	}

	if notaryV1URL != "" {
		signer := notary.ImageSigner{
			Logger:  logger,
			Client:  &registry.Client{},
			Factory: &notary.RemoteRepositoryFactory{},
		}
		if err := signer.Sign(notaryV1URL, notarySecretDir, report, keychain); err != nil {
			return err
		}
	}
	return nil
}

func mapKeyValueArgs(args flaghelpers.CredentialsFlags) (map[string]interface{}, error) {
	overrides := make(map[string]interface{})

	for _, arg := range args {
		splitArg := strings.Split(arg, "=")

		if len(splitArg) != 2 {
			return nil, errors.Errorf("argument not formatted as -arg=key=value: %s", arg)
		}

		key := splitArg[0]
		value := splitArg[1]

		overrides[key] = value
	}

	return overrides, nil
}

func hasCosign() bool {
	_, err := os.Stat(cosignSecretLocation)
	return !os.IsNotExist(err)
}
