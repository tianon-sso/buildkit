package llbsolver

import (
	"context"
	"strconv"
	"sync"

	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/worker"
)

// setupIntermediateImages parses the "intermediate-images" frontend opt and, if
// enabled, wires up a refAccumulator and per-step export function on the job.
// It returns a cleanup function to release accumulated refs (nil if not enabled).
func (s *Solver) setupIntermediateImages(ctx context.Context, frontendOpt map[string]string, exp ExporterRequest, sessionID string, j *solver.Job) func() {
	v, ok := frontendOpt["intermediate-images"]
	if !ok {
		return nil
	}
	enabled, _ := strconv.ParseBool(v)
	if !enabled && v != "docker-stream" {
		return nil
	}
	stream := v == "docker-stream"
	ociPush := false
	for _, expi := range exp.Exporters {
		if expi.Type() == client.ExporterOCI && expi.Attrs()["tar"] == "false" {
			ociPush = true
			break
		}
	}
	acc := &refAccumulator{batchExport: !ociPush}
	bklog.G(ctx).Debugf("intermediate-images enabled: stream=%v ociPush=%v sessionID=%s", stream, ociPush, sessionID)
	j.SetValue(solver.KeyIntermediateImageAccumulator, acc)
	j.SetValue(solver.KeyIntermediateImageExporter, s.makeIntermediateExporter(sessionID, acc, stream, ociPush))
	return func() { acc.release(context.WithoutCancel(ctx)) }
}

// refAccumulator collects cloned ImmutableRefs from per-step exec results so
// they can be passed to exporters as a batch via ExportBuildInfo.IntermediateImages.
// In ociPush mode batchExport is false: refs are retained only for cleanup,
// since each step's blobs were already pushed to the OCI dir store per-step.
type refAccumulator struct {
	mu          sync.Mutex
	refs        []cache.ImmutableRef
	batchExport bool
}

func (a *refAccumulator) add(ref cache.ImmutableRef) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refs = append(a.refs, ref)
	return len(a.refs) - 1
}

func (a *refAccumulator) list() []cache.ImmutableRef {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.batchExport {
		return nil
	}
	out := make([]cache.ImmutableRef, len(a.refs))
	copy(out, a.refs)
	return out
}

func (a *refAccumulator) release(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ref := range a.refs {
		ref.Release(ctx)
	}
	a.refs = nil
}

func (s *Solver) makeIntermediateExporter(sessionID string, acc *refAccumulator, stream, ociPush bool) worker.IntermediateImageExportFunc {
	return func(ctx context.Context, ref cache.ImmutableRef, sg session.Group) error {
		bklog.G(ctx).Debugf("intermediate-images: export func called ref=%s stream=%v ociPush=%v", ref.ID(), stream, ociPush)

		// In ociPush mode, push each step's image blobs to the client's OCI
		// directory content store immediately after the step completes, then
		// signal index.json update via store.Update(). We add to acc only for
		// ref lifetime management; list() returns nil in this mode so the
		// final exporter pass does not redundantly re-export blobs.
		if ociPush {
			idx := acc.add(ref.Clone())
			w, err := s.resolveWorker()
			if err != nil {
				return err
			}
			exp, err := w.Exporter(client.ExporterOCI, s.sm)
			if err != nil {
				return err
			}
			expi, err := exp.Resolve(ctx, client.ExporterIntermediateImagesID, map[string]string{"tar": "false"})
			if err != nil {
				return err
			}
			bi := exporter.ExportBuildInfo{
				SessionID:           sessionID,
				IntermediateStepIdx: &idx,
			}
			bklog.G(ctx).Debugf("intermediate-images: OCI per-step export ref=%s idx=%d", ref.ID(), idx)
			_, _, descref, err := expi.Export(ctx, &exporter.Source{Ref: ref}, bi)
			if descref != nil {
				descref.Release()
			}
			bklog.G(ctx).Debugf("intermediate-images: OCI per-step export done err=%v", err)
			return err
		}

		acc.add(ref.Clone())
		if !stream {
			return nil
		}
		w, err := s.resolveWorker()
		if err != nil {
			return err
		}
		exp, err := w.Exporter(client.ExporterDocker, s.sm)
		if err != nil {
			return err
		}
		expi, err := exp.Resolve(ctx, client.ExporterIntermediateImagesID, map[string]string{})
		if err != nil {
			return err
		}
		src := &exporter.Source{Ref: ref}
		bi := exporter.ExportBuildInfo{SessionID: sessionID}
		bklog.G(ctx).Debugf("intermediate-images: calling Docker export for ref=%s sessionID=%s", ref.ID(), sessionID)
		_, finalize, descref, err := expi.Export(ctx, src, bi)
		if descref != nil {
			descref.Release()
		}
		if err == nil && finalize != nil {
			err = finalize(ctx)
		}
		bklog.G(ctx).Debugf("intermediate-images: Docker export done err=%v", err)
		return err
	}
}
