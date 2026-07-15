package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"

	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	awsshared "github.com/newstack-cloud/bluelink-transformer-celerity/shared/aws"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/newstack-cloud/bluelink-transformer-celerity/transformer"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/plugin"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/pluginservicev1"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/pluginutils"
	"github.com/newstack-cloud/bluelink/libs/plugin-framework/sdk/transformerv1"
	"github.com/spf13/afero"
)

//go:embed transformer_description.md
var embedded embed.FS

func main() {
	serviceClient, closeService, err := pluginservicev1.NewEnvServiceClient()
	if err != nil {
		log.Fatal(err.Error())
	}
	defer closeService()

	fileSystem := afero.NewOsFs()
	hostInfoContainer := pluginutils.NewHostInfoContainer()
	fsResourceLoader := build.NewFSResourceLoader(fileSystem)
	envMap := shared.EnvMap(os.Environ())
	s3ResourceLoader := build.NewS3ResourceLoader(
		awsshared.NewS3Client,
		envMap,
		nil, // Use default config loader
	)
	manifestLoader := build.NewManifestLoader(
		build.WithDefaultResourceLoader(fsResourceLoader),
		build.WithSchemeResourceLoader("s3", s3ResourceLoader),
	)
	deps := &shared.Dependencies{
		BuildManifestLoader: manifestLoader,
	}
	transformerServer := transformerv1.NewTransformerPlugin(
		transformer.NewTransformer(deps),
		hostInfoContainer,
		serviceClient,
	)

	transformerDescription, _ := embedded.ReadFile("transformer_description.md")
	config := plugin.ServePluginConfiguration{
		ID: "newstack-cloud/celerity",
		PluginMetadata: &pluginservicev1.PluginMetadata{
			PluginVersion:        version,
			DisplayName:          "Celerity",
			FormattedDescription: string(transformerDescription),
			RepositoryUrl:        "https://github.com/newstack-cloud/bluelink-transformer-celerity",
			Author:               "NewStack Cloud Limited",
		},
		ProtocolVersion: "1.0",
	}

	fmt.Println("Starting Bluelink Celerity Transformer Plugin Server...")
	close, err := plugin.ServeTransformerV1(
		context.Background(),
		transformerServer,
		serviceClient,
		hostInfoContainer,
		config,
	)
	if err != nil {
		log.Fatal(err.Error())
	}
	pluginutils.WaitForShutdown(close)
}
