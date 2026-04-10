package client

import (
	"context"
	"encoding/json"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/moby/buildkit/client/ociindex"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// indexUpdatingStore wraps a content.Store to intercept Update() calls that
// carry an intermediate image descriptor label (LabelIntermediateImageDescriptor).
// When such a call arrives, it writes the descriptor into the OCI layout
// index.json at storePath before forwarding the Update to the underlying store.
// This enables incremental index.json updates during a build: the server pushes
// each step's blobs via CopyChain and then calls Update() as a side-channel
// signal, so index.json grows step-by-step even if the build fails mid-way.
type indexUpdatingStore struct {
	content.Store
	storePath string
}

func (s *indexUpdatingStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return s.Store.Info(ctx, dgst)
}

func (s *indexUpdatingStore) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	if dt, ok := info.Labels[exptypes.LabelIntermediateImageDescriptor]; ok {
		var desc ocispecs.Descriptor
		if err := json.Unmarshal([]byte(dt), &desc); err == nil {
			idx := ociindex.NewStoreIndex(s.storePath)
			// Best-effort: a failed index.json write is non-fatal; the blobs
			// are already in the store so a manual index rebuild is possible.
			_ = idx.Put(desc)
		}
		// This Update() is only a signalling call; don't forward it to the
		// underlying store because the label is internal and the content item
		// being updated may not carry persistent metadata in all store impls.
		return info, nil
	}
	return s.Store.Update(ctx, info, fieldpaths...)
}
