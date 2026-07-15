package build

// Manifest holds build artifacts produced by the Celerity build step.
//
// Target-specific output (AWS Lambda, container, etc.) lives under a
// dedicated typed sub-manifest so adding a new strategy does not require
// changing the top-level shape.
type Manifest struct {
	Version  string                       `json:"version"`
	Runtime  string                       `json:"runtime"`
	Target   string                       `json:"target"`
	Handlers map[string]*HandlerArtifacts `json:"handlers"`
	// Lambda is populated only when the build target is aws-serverless.
	Lambda *LambdaManifest `json:"lambda,omitempty"`
}

// HandlerArtifacts holds per-handler build metadata. Target-specific
// overrides (e.g. a custom dependency layer for Lambda) live under their
// own sub-struct so each strategy owns the shape of its per-handler data.
type HandlerArtifacts struct {
	Lambda *LambdaHandlerArtifacts `json:"lambda,omitempty"`
}

// LambdaArtifact represents a single build artifact (zip archive or image) for AWS Lambda.
type LambdaArtifact struct {
	Type        string `json:"type"`
	LocalPath   string `json:"localPath,omitempty"`
	S3Bucket    string `json:"s3Bucket,omitempty"`
	S3Key       string `json:"s3Key,omitempty"`
	ImageURI    string `json:"imageUri,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// LambdaManifest holds AWS Lambda-specific build output referenced by
// every handler's Lambda function.
type LambdaManifest struct {
	// AppCode is the single shared code asset. This includes the
	// whole app source tree plus the generated Celerity Lambda entry point file.
	AppCode *LambdaArtifact `json:"appCode,omitempty"`
	// EntryPoint is the Lambda Handler field value, identical for every
	// handler in a project (e.g. "__celerity_lambda_entry__.handler").
	EntryPoint string `json:"entryPoint,omitempty"`
	// SharedLayer is the production dependency layer used by handlers
	// that do not opt into a custom per-handler dep layer.
	SharedLayer *LambdaArtifact `json:"sharedLayer,omitempty"`
}

// LambdaHandlerArtifacts holds per-handler Lambda overrides.
type LambdaHandlerArtifacts struct {
	// Dependencies is the handler's custom dependency layer, populated
	// only when the developer has declared handlerDependencies. Nil
	// handlers fall back to LambdaManifest.SharedLayer.
	Dependencies *LambdaArtifact `json:"dependencies,omitempty"`
}
