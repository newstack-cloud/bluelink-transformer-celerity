//go:build e2e

package e2e

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared"
	"github.com/newstack-cloud/bluelink-transformer-celerity/shared/build"
	"github.com/stretchr/testify/require"
)

const (
	appCodeKey     = "app.zip"
	sharedLayerKey = "shared-layer.zip"
	// The Lambda Handler value comes verbatim from the manifest entryPoint,
	// so the stub app code must contain a matching module + export.
	lambdaEntryFileName = "__celerity_lambda_entry__.js"
	lambdaEntryPoint    = "__celerity_lambda_entry__.handler"
	lambdaEntrySource   = "exports.handler = async () => ({ statusCode: 200 });"
	sharedLayerReadme   = "Placeholder shared dependency layer for celerity e2e tests.\n"
	manifestRuntime     = "nodejs24.x"
)

// PrestageArtifacts uploads a stub nodejs app-code zip and shared dependency
// layer zip to a run-unique S3 bucket, then writes a build-manifest.json
// pointing at them to a temp dir, returning the manifest path for
// Harness.Deploy. This stands in for the `celerity build` step: the
// transformer only needs valid S3 coordinates and an entry point that the
// zipped code actually provides. Bucket and objects are deleted via t.Cleanup.
func (h *Harness) PrestageArtifacts(t *testing.T) string {
	t.Helper()

	client := s3.NewFromConfig(h.AWSConfig)
	bucketName := h.NamePrefix + "-artifacts"
	createArtifactBucket(t, h, client, bucketName)

	appCode := buildZipArchive(t, map[string]string{lambdaEntryFileName: lambdaEntrySource})
	layer := buildZipArchive(t, map[string]string{"nodejs/README.md": sharedLayerReadme})

	uploadArtifact(t, h, client, bucketName, appCodeKey, appCode)
	uploadArtifact(t, h, client, bucketName, sharedLayerKey, layer)

	manifest := &build.Manifest{
		Version:  "1",
		Runtime:  manifestRuntime,
		Target:   shared.AWSServerless,
		Handlers: map[string]*build.HandlerArtifacts{},
		Lambda: &build.LambdaManifest{
			AppCode:     zipArtifact(bucketName, appCodeKey, appCode),
			EntryPoint:  lambdaEntryPoint,
			SharedLayer: zipArtifact(bucketName, sharedLayerKey, layer),
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err, "marshal build manifest")

	manifestPath := filepath.Join(t.TempDir(), "build-manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, manifestJSON, 0o600), "write build manifest")
	return manifestPath
}

func createArtifactBucket(t *testing.T, h *Harness, client *s3.Client, bucketName string) {
	t.Helper()

	createInput := &s3.CreateBucketInput{Bucket: aws.String(bucketName)}
	// us-east-1 is the default location and rejects an explicit constraint.
	if h.Region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(h.Region),
		}
	}
	_, err := client.CreateBucket(h.Ctx, createInput)
	require.NoErrorf(t, err, "create artifact bucket %s", bucketName)

	t.Cleanup(func() {
		for _, key := range []string{appCodeKey, sharedLayerKey} {
			_, err := client.DeleteObject(h.Ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(key),
			})
			if err != nil {
				t.Errorf("cleanup: delete artifact %s/%s: %v", bucketName, key, err)
			}
		}
		if _, err := client.DeleteBucket(h.Ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(bucketName),
		}); err != nil {
			t.Errorf("cleanup: delete artifact bucket %s: %v", bucketName, err)
		}
	})
}

func uploadArtifact(
	t *testing.T,
	h *Harness,
	client *s3.Client,
	bucketName string,
	key string,
	content []byte,
) {
	t.Helper()
	_, err := client.PutObject(h.Ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(content),
	})
	require.NoErrorf(t, err, "upload artifact %s/%s", bucketName, key)
}

func buildZipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range entries {
		entry, err := writer.Create(name)
		require.NoErrorf(t, err, "create zip entry %s", name)
		_, err = entry.Write([]byte(content))
		require.NoErrorf(t, err, "write zip entry %s", name)
	}
	require.NoError(t, writer.Close(), "finalise zip archive")
	return buf.Bytes()
}

func zipArtifact(bucketName, key string, content []byte) *build.LambdaArtifact {
	hash := sha256.Sum256(content)
	return &build.LambdaArtifact{
		Type:        "zip",
		S3Bucket:    bucketName,
		S3Key:       key,
		Size:        int64(len(content)),
		ContentHash: hex.EncodeToString(hash[:]),
	}
}
