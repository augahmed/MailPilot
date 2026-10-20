//go:build darwin && arm64 && cgo

package embedding

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	ort "github.com/yalue/onnxruntime_go"
	"mailpilot.local/core/internal/embedding/model"
)

var nativeOwner struct {
	sync.Mutex
	active bool
}

type nativeEngine struct{ session *ort.DynamicAdvancedSession }

func loadNative(ctx context.Context) (engine, error) {
	root, err := resourceDirectory()
	if err != nil {
		return nil, err
	}
	return loadNativeResources(ctx, root)
}
func loadNativeResources(ctx context.Context, root string) (engine, error) {
	version, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return nil, ErrUnavailable
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return nil, ErrUnavailable
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < 13 || major == 13 && minor < 4 {
		return nil, ErrUnavailable
	}
	data, library, err := verifiedAssets(ctx, root)
	if err != nil {
		return nil, err
	}
	nativeOwner.Lock()
	defer nativeOwner.Unlock()
	if nativeOwner.active {
		return nil, ErrUnavailable
	}
	ort.SetSharedLibraryPath(library)
	if err = ort.InitializeEnvironment(ort.WithLogLevelFatal()); err != nil {
		return nil, ErrUnavailable
	}
	success := false
	defer func() {
		if !success {
			_ = ort.DestroyEnvironment()
		}
	}()
	if ort.GetVersion() != model.Artifacts().RuntimeVersion {
		return nil, ErrUnavailable
	}
	if err = ort.DisableTelemetry(); err != nil {
		return nil, ErrUnavailable
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer options.Destroy()
	for _, configure := range []func() error{
		func() error { return options.SetIntraOpNumThreads(2) },
		func() error { return options.SetInterOpNumThreads(1) },
		func() error { return options.SetExecutionMode(ort.ExecutionModeSequential) },
		func() error { return options.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableBasic) },
		func() error { return options.SetLogSeverityLevel(ort.LoggingLevelFatal) },
		func() error { return options.SetCpuMemArena(false) },
		func() error { return options.SetMemPattern(false) },
		func() error { return options.AddSessionConfigEntry("session.intra_op.allow_spinning", "0") },
		func() error { return options.AddSessionConfigEntry("session.inter_op.allow_spinning", "0") },
	} {
		if err = configure(); err != nil {
			return nil, ErrUnavailable
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSessionWithONNXData(data, []string{"input_ids", "attention_mask", "token_type_ids"}, []string{"last_hidden_state"}, options)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = ctx.Err(); err != nil {
		_ = session.Destroy()
		return nil, err
	}
	nativeOwner.active = true
	success = true
	return &nativeEngine{session}, nil
}
func (e *nativeEngine) Run(ctx context.Context, b tokenBatch) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// ORT retains tensor buffers until ReleaseOrtValue, across multiple C calls.
	// Pin the Go backing arrays before creation, unpin after all tensor defers.
	var pinned runtime.Pinner
	defer pinned.Unpin()
	pinned.Pin(&b.IDs[0])
	pinned.Pin(&b.Mask[0])
	pinned.Pin(&b.Types[0])
	shape := ort.NewShape(int64(b.Rows), int64(b.Length))
	ids, err := ort.NewTensor(shape, b.IDs)
	if err != nil {
		return nil, ErrInference
	}
	defer ids.Destroy()
	mask, err := ort.NewTensor(shape, b.Mask)
	if err != nil {
		return nil, ErrInference
	}
	defer mask.Destroy()
	types, err := ort.NewTensor(shape, b.Types)
	if err != nil {
		return nil, ErrInference
	}
	defer types.Destroy()
	outputData := make([]float32, b.Rows*b.Length*model.Dimensions)
	pinned.Pin(&outputData[0])
	output, err := ort.NewTensor(ort.NewShape(int64(b.Rows), int64(b.Length), model.Dimensions), outputData)
	if err != nil {
		return nil, ErrInference
	}
	defer output.Destroy()
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, ErrInference
	}
	defer options.Destroy()
	// Join the cancellation callback before destroying RunOptions: ORT allows
	// setting its terminate flag concurrently with Run, not with Release.
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = options.Terminate(); close(callbackDone) })
	err = e.session.RunWithOptions([]ort.Value{ids, mask, types}, []ort.Value{output}, options)
	if !stop() {
		<-callbackDone
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, ErrInference
	}
	return outputData, nil
}
func (e *nativeEngine) Close() error {
	nativeOwner.Lock()
	defer nativeOwner.Unlock()
	err := e.session.Destroy()
	envErr := ort.DestroyEnvironment()
	nativeOwner.active = false
	if err != nil || envErr != nil {
		return ErrInference
	}
	return nil
}
