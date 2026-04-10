package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	archiveexporter "github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/distribution/reference"
	"github.com/moby/buildkit/cache"
	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter"
	"github.com/moby/buildkit/exporter/containerimage"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/progress"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
)

type ExporterVariant string

const (
	VariantOCI    = client.ExporterOCI
	VariantDocker = client.ExporterDocker
)

const (
	keyTar = "tar"
)

type Opt struct {
	SessionManager *session.Manager
	ImageWriter    *containerimage.ImageWriter
	Variant        ExporterVariant
	LeaseManager   leases.Manager
}

type imageExporter struct {
	opt Opt
}

func New(opt Opt) (exporter.Exporter, error) {
	im := &imageExporter{opt: opt}
	return im, nil
}

func (e *imageExporter) Resolve(ctx context.Context, id int, opt map[string]string) (exporter.ExporterInstance, error) {
	i := &imageExporterInstance{
		imageExporter: e,
		id:            id,
		attrs:         opt,
		tar:           true,
		opts: containerimage.ImageCommitOpts{
			RefCfg: cacheconfig.RefConfig{
				Compression: compression.New(compression.Default),
			},
			OCITypes: e.opt.Variant == VariantOCI,
		},
	}

	opt, err := i.opts.Load(ctx, opt)
	if err != nil {
		return nil, err
	}

	for k, v := range opt {
		switch k {
		case keyTar:
			if v == "" {
				i.tar = true
				continue
			}
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, errors.Wrapf(err, "non-bool value specified for %s", k)
			}
			i.tar = b
		default:
			if i.meta == nil {
				i.meta = make(map[string][]byte)
			}
			i.meta[k] = []byte(v)
		}
	}
	return i, nil
}

type imageExporterInstance struct {
	*imageExporter
	id    int
	attrs map[string]string

	opts containerimage.ImageCommitOpts
	tar  bool
	meta map[string][]byte
}

func (e *imageExporterInstance) ID() int {
	return e.id
}

func (e *imageExporterInstance) Name() string {
	return fmt.Sprintf("exporting to %s image format", e.opt.Variant)
}

func (e *imageExporterInstance) Type() string {
	return string(e.opt.Variant)
}

func (e *imageExporterInstance) Attrs() map[string]string {
	return e.attrs
}

func (e *imageExporterInstance) Config() *exporter.Config {
	return exporter.NewConfigWithCompression(e.opts.RefCfg.Compression)
}

func (e *imageExporterInstance) Export(ctx context.Context, src *exporter.Source, buildInfo exporter.ExportBuildInfo) (_ map[string]string, _ exporter.FinalizeFunc, descref exporter.DescriptorReference, err error) {
	if e.opt.Variant == VariantDocker && len(src.Refs) > 0 {
		return nil, nil, nil, errors.Errorf("docker exporter does not currently support exporting manifest lists")
	}

	src = src.Clone()
	if src.Metadata == nil {
		src.Metadata = make(map[string][]byte)
	}
	maps.Copy(src.Metadata, e.meta)

	opts := e.opts
	as, _, err := containerimage.ParseAnnotations(src.Metadata)
	if err != nil {
		return nil, nil, nil, err
	}
	opts.Annotations = opts.Annotations.Merge(as)

	// For per-step intermediate exports, embed step annotations in the manifest
	// blob so they are verifiable from the blob content, not only from the
	// index descriptor written via store.Update().
	if buildInfo.IntermediateStepIdx != nil {
		anns := intermediateStepAnnotations(*buildInfo.IntermediateStepIdx, src.Ref)
		opts.Annotations = opts.Annotations.Merge(containerimage.AnnotationsGroup{
			"": &containerimage.Annotations{Manifest: anns},
		})
	}

	ctx, done, err := leaseutil.WithLease(ctx, e.opt.LeaseManager, leaseutil.MakeTemporary)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if descref == nil {
			done(context.WithoutCancel(ctx))
		}
	}()

	desc, err := e.opt.ImageWriter.Commit(ctx, src, buildInfo.SessionID, buildInfo.InlineCache, &opts)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if err == nil {
			descref = containerimage.NewDescriptorReference(*desc, done)
		}
	}()

	if desc.Annotations == nil {
		desc.Annotations = map[string]string{}
	}
	if _, ok := desc.Annotations[ocispecs.AnnotationCreated]; !ok {
		tm := time.Now()
		if opts.Epoch != nil && opts.Epoch.Value != nil {
			tm = *opts.Epoch.Value
		}
		desc.Annotations[ocispecs.AnnotationCreated] = tm.UTC().Format(time.RFC3339)
	}

	resp := make(map[string]string)

	resp[exptypes.ExporterImageDigestKey] = desc.Digest.String()
	if v, ok := desc.Annotations[exptypes.ExporterConfigDigestKey]; ok {
		resp[exptypes.ExporterImageConfigDigestKey] = v
		delete(desc.Annotations, exptypes.ExporterConfigDigestKey)
	}

	dtdesc, err := json.Marshal(desc)
	if err != nil {
		return nil, nil, nil, err
	}
	resp[exptypes.ExporterImageDescriptorKey] = base64.StdEncoding.EncodeToString(dtdesc)

	if n, ok := src.Metadata["image.name"]; e.opts.ImageName == "*" && ok {
		e.opts.ImageName = string(n)
	}

	names, err := normalizedNames(e.opts.ImageName)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(names) != 0 {
		resp[exptypes.ExporterImageNameKey] = strings.Join(names, ",")
	}

	// Commit intermediate images (accumulated from per-step exec results when
	// intermediate-images is enabled). Use the same options as the primary image
	// but without inline cache or names so they appear as unnamed manifests.
	iOpts := e.opts
	var intermediateDescs []ocispecs.Descriptor
	for i, ref := range buildInfo.IntermediateImages {
		// Annotations shared between the manifest blob and the index descriptor.
		anns := intermediateStepAnnotations(i, ref)

		// Start from a fresh AnnotationsGroup (to avoid mutating the shared
		// e.opts.Annotations) and inject anns at the manifest level so they
		// are embedded in the manifest blob.
		stepOpts := iOpts
		stepOpts.Annotations = containerimage.AnnotationsGroup(nil).
			Merge(iOpts.Annotations).
			Merge(containerimage.AnnotationsGroup{
				"": &containerimage.Annotations{Manifest: anns},
			})
		iDesc, err := e.opt.ImageWriter.Commit(ctx, &exporter.Source{Ref: ref}, buildInfo.SessionID, nil, &stepOpts)
		if err != nil {
			return nil, nil, nil, err
		}
		// Strip the internal config.digest side-channel and apply the same
		// annotations to the index descriptor.
		delete(iDesc.Annotations, exptypes.ExporterConfigDigestKey)
		if iDesc.Annotations == nil {
			iDesc.Annotations = make(map[string]string)
		}
		maps.Copy(iDesc.Annotations, anns)
		intermediateDescs = append(intermediateDescs, *iDesc)
	}

	expOpts := []archiveexporter.ExportOpt{archiveexporter.WithManifest(*desc, names...)}
	for _, iDesc := range intermediateDescs {
		expOpts = append(expOpts, archiveexporter.WithManifest(iDesc))
	}
	switch e.opt.Variant {
	case VariantOCI:
		expOpts = append(expOpts, archiveexporter.WithAllPlatforms(), archiveexporter.WithSkipDockerManifest())
	case VariantDocker:
	default:
		return nil, nil, nil, errors.Errorf("invalid variant %q", e.opt.Variant)
	}

	timeoutCtx, cancel := context.WithCancelCause(ctx)
	timeoutCtx, _ = context.WithTimeoutCause(timeoutCtx, 5*time.Second, errors.WithStack(context.DeadlineExceeded)) //nolint:govet
	defer func() { cancel(errors.WithStack(context.Canceled)) }()

	caller, err := e.opt.SessionManager.Get(timeoutCtx, buildInfo.SessionID, false)
	if err != nil {
		return nil, nil, nil, err
	}

	var refs []cache.ImmutableRef
	if src.Ref != nil {
		refs = append(refs, src.Ref)
	}
	for _, ref := range src.Refs {
		refs = append(refs, ref)
	}
	refs = append(refs, buildInfo.IntermediateImages...)
	eg, egCtx := errgroup.WithContext(ctx)
	mprovider := contentutil.NewMultiProvider(e.opt.ImageWriter.ContentStore())
	for _, ref := range refs {
		eg.Go(func() error {
			if ref == nil {
				return nil
			}
			remotes, err := ref.GetRemotes(egCtx, false, e.opts.RefCfg, false, session.NewGroup(buildInfo.SessionID))
			if err != nil {
				return err
			}
			remote := remotes[0]
			if unlazier, ok := remote.Provider.(cache.Unlazier); ok {
				if err := unlazier.Unlazy(egCtx); err != nil {
					return err
				}
			}
			for _, desc := range remote.Descriptors {
				mprovider.Add(desc.Digest, remote.Provider)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, nil, err
	}

	if e.tar {
		w, err := filesync.CopyFileWriter(ctx, resp, e.id, caller)
		if err != nil {
			return nil, nil, nil, err
		}

		report := progress.OneOff(ctx, "sending tarball")
		if err := archiveexporter.Export(ctx, mprovider, w, expOpts...); err != nil {
			w.Close()
			if grpcerrors.Code(err) == codes.AlreadyExists {
				return resp, nil, nil, report(nil)
			}
			return nil, nil, nil, report(err)
		}
		err = w.Close()
		if grpcerrors.Code(err) == codes.AlreadyExists {
			return resp, nil, nil, report(nil)
		}
		if err != nil {
			return nil, nil, nil, report(err)
		}
		report(nil)
	} else {
		store := sessioncontent.NewCallerStore(caller, "export")
		if err != nil {
			return nil, nil, nil, err
		}
		err := contentutil.CopyChain(ctx, store, mprovider, *desc)
		if err != nil {
			return nil, nil, nil, err
		}
		// For per-step intermediate exports (OCI dir + intermediate-images enabled),
		// signal the client's indexUpdatingStore to update index.json with this
		// step's descriptor. The annotations carry step index and command so the
		// client can identify the step without fetching the manifest.
		if buildInfo.IntermediateStepIdx != nil {
			anns := intermediateStepAnnotations(*buildInfo.IntermediateStepIdx, src.Ref)
			stepDesc := *desc
			if stepDesc.Annotations == nil {
				stepDesc.Annotations = make(map[string]string)
			}
			maps.Copy(stepDesc.Annotations, anns)
			if descJSON, merr := json.Marshal(stepDesc); merr == nil {
				_, _ = store.Update(ctx, content.Info{
					Digest: stepDesc.Digest,
					Labels: map[string]string{exptypes.LabelIntermediateImageDescriptor: string(descJSON)},
				}, "labels."+exptypes.LabelIntermediateImageDescriptor)
			}
		}
		if len(intermediateDescs) > 0 {
			for _, iDesc := range intermediateDescs {
				if err := contentutil.CopyChain(ctx, store, mprovider, iDesc); err != nil {
					return nil, nil, nil, err
				}
			}
			dt, err := json.Marshal(intermediateDescs)
			if err != nil {
				return nil, nil, nil, err
			}
			resp[exptypes.ExporterIntermediateImageDescriptorsKey] = string(dt)
		}
	}

	return resp, nil, nil, nil
}

// intermediateStepAnnotations returns the index and command annotations for an
// intermediate build-step image. idx is the 0-based accumulation order. The
// command is extracted from the ref description when it matches the root-mount
// format produced by ExecOp ("mount / from exec <cmd>").
func intermediateStepAnnotations(idx int, ref cache.ImmutableRef) map[string]string {
	anns := map[string]string{
		exptypes.ExporterIntermediateIndexKey: strconv.Itoa(idx),
	}
	if ref != nil {
		if cmd, ok := strings.CutPrefix(ref.GetDescription(), "mount / from exec "); ok {
			anns[exptypes.ExporterIntermediateStepCommandKey] = cmd
		}
	}
	return anns
}

func normalizedNames(name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	names := strings.Split(name, ",")
	tagNames := make([]string, len(names))
	for i, name := range names {
		parsed, err := reference.ParseNormalizedNamed(name)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse %s", name)
		}
		tagNames[i] = reference.TagNameOnly(parsed).String()
	}
	return tagNames, nil
}
