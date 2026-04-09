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
	// ExporterIntermediateStepIndexKey is an annotation set on each intermediate
	// build-step image manifest indicating its 0-based step index within the
	// build (i.e. its position in the ordered sequence of accumulated exec steps).
	ExporterIntermediateStepIndexKey = "moby.buildkit.intermediate.step"
	// ExporterIntermediateStepCommandKey is an annotation set on each intermediate
	// build-step image manifest with the command that produced it (the exec args,
	// joined with spaces).
	ExporterIntermediateStepCommandKey = "moby.buildkit.intermediate.command"
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
