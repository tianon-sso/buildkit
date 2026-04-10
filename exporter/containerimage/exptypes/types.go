package exptypes

import (
	"context"

	"github.com/moby/buildkit/solver/result"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	ExporterConfigDigestKey      = "config.digest"
	ExporterImageNameKey         = "image.name"
	ExporterImageDigestKey       = "containerimage.digest"
	ExporterImageConfigKey       = "containerimage.config"
	ExporterImageConfigDigestKey = "containerimage.config.digest"
	ExporterImageDescriptorKey   = "containerimage.descriptor"
	ExporterImageBaseConfigKey   = "containerimage.base.config"
	ExporterPlatformsKey         = "refs.platforms"
	// ExporterIntermediateImageDescriptorsKey carries a JSON-encoded
	// []ocispecs.Descriptor for intermediate build-step images. Only set by the
	// OCI non-tar (directory) exporter; used by the client to update index.json.
	ExporterIntermediateImageDescriptorsKey = "containerimage.intermediate.descriptors"
	// ExporterIntermediateIndexKey is an annotation set on each intermediate
	// build-step image manifest with its 0-based index in the ordered sequence
	// of intermediate images accumulated during the build. This is NOT the
	// BuildKit vertex/step index; it is simply the order in which exec steps
	// completed and were captured.
	ExporterIntermediateIndexKey = "moby.buildkit.intermediate.index"
	// ExporterIntermediateStepCommandKey is an annotation set on each intermediate
	// build-step image manifest with the command that produced it (the exec args,
	// joined with spaces).
	ExporterIntermediateStepCommandKey = "moby.buildkit.intermediate.command"
	// LabelIntermediateImageDescriptor is a content store label used as a
	// side-channel to notify the client to update index.json after a per-step
	// OCI intermediate image blob has been pushed. The value is a JSON-encoded
	// ocispecs.Descriptor with step annotations. Only used when
	// intermediate-images is enabled with OCI directory (tar=false) output.
	LabelIntermediateImageDescriptor = "buildkit.intermediate.image.descriptor"
)

// KnownRefMetadataKeys are the subset of exporter keys that can be suffixed by
// a platform to become platform specific
var KnownRefMetadataKeys = []string{
	ExporterImageConfigKey,
	ExporterImageBaseConfigKey,
}

type Platforms struct {
	Platforms []Platform
}

type Platform struct {
	ID       string
	Platform ocispecs.Platform
}

type InlineCacheEntry struct {
	Data []byte
}
type InlineCache func(ctx context.Context) (*result.Result[*InlineCacheEntry], error)
