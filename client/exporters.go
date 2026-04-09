package client

import "math"

const (
	ExporterImage  = "image"
	ExporterLocal  = "local"
	ExporterTar    = "tar"
	ExporterOCI    = "oci"
	ExporterDocker = "docker"

	// ExporterIntermediateImagesID is the reserved session file-sync ID used to
	// stream intermediate build step images back to the client when the
	// --intermediate-images flag is enabled. This value is chosen to avoid
	// conflict with normal exporter IDs (0, 1, 2, …).
	ExporterIntermediateImagesID = math.MaxInt32 - 1
)
