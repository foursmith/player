package detection

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/foursmith/player/internal/native"
)

type inferenceAPI struct {
	ModelCreate       func(unsafe.Pointer, uintptr) unsafe.Pointer
	ModelDelete       func(unsafe.Pointer)
	OptionsCreate     func() unsafe.Pointer
	OptionsDelete     func(unsafe.Pointer)
	SetThreads        func(unsafe.Pointer, int32)
	InterpreterCreate func(unsafe.Pointer, unsafe.Pointer) unsafe.Pointer
	InterpreterDelete func(unsafe.Pointer)
	Allocate          func(unsafe.Pointer) int32
	Invoke            func(unsafe.Pointer) int32
	Input             func(unsafe.Pointer, int32) unsafe.Pointer
	Output            func(unsafe.Pointer, int32) unsafe.Pointer
	OutputCount       func(unsafe.Pointer) int32
	Dimensions        func(unsafe.Pointer) int32
	Dimension         func(unsafe.Pointer, int32) int32
	ByteSize          func(unsafe.Pointer) uintptr
	CopyFrom          func(unsafe.Pointer, unsafe.Pointer, uintptr) int32
	CopyTo            func(unsafe.Pointer, unsafe.Pointer, uintptr) int32
}

type inference struct {
	api                         inferenceAPI
	model, options, interpreter unsafe.Pointer
	bytes                       []byte
	pin                         runtime.Pinner
}

func openInference(library string, model []byte) (*inference, error) {
	if len(model) == 0 {
		return nil, errors.New("empty inference model")
	}
	if library == "" {
		library = native.InferenceLibrary()
	}
	handle, err := native.Open(library)
	if err != nil {
		return nil, err
	}
	e := &inference{}
	if err := native.Bind(handle, &e.api,
		"TfLiteModelCreate", "TfLiteModelDelete", "TfLiteInterpreterOptionsCreate", "TfLiteInterpreterOptionsDelete",
		"TfLiteInterpreterOptionsSetNumThreads", "TfLiteInterpreterCreate", "TfLiteInterpreterDelete",
		"TfLiteInterpreterAllocateTensors", "TfLiteInterpreterInvoke", "TfLiteInterpreterGetInputTensor",
		"TfLiteInterpreterGetOutputTensor", "TfLiteInterpreterGetOutputTensorCount", "TfLiteTensorNumDims",
		"TfLiteTensorDim", "TfLiteTensorByteSize", "TfLiteTensorCopyFromBuffer", "TfLiteTensorCopyToBuffer"); err != nil {
		return nil, err
	}
	// LiteRT retains the model buffer until ModelDelete. Own and pin that memory
	// explicitly instead of letting a native object retain an untracked Go slice.
	e.bytes = append([]byte(nil), model...)
	e.pin.Pin(&e.bytes[0])
	e.model = e.api.ModelCreate(unsafe.Pointer(&e.bytes[0]), uintptr(len(e.bytes)))
	if e.model != nil {
		e.options = e.api.OptionsCreate()
	}
	if e.options != nil {
		e.api.SetThreads(e.options, 2)
		e.interpreter = e.api.InterpreterCreate(e.model, e.options)
	}
	if e.interpreter == nil {
		e.close()
		return nil, errors.New("cannot create LiteRT interpreter")
	}
	if e.api.Allocate(e.interpreter) != 0 {
		e.close()
		return nil, errors.New("cannot allocate LiteRT tensors")
	}
	return e, nil
}

func (e *inference) close() {
	if e == nil {
		return
	}
	if e.interpreter != nil {
		e.api.InterpreterDelete(e.interpreter)
		e.interpreter = nil
	}
	if e.options != nil {
		e.api.OptionsDelete(e.options)
		e.options = nil
	}
	if e.model != nil {
		e.api.ModelDelete(e.model)
		e.model = nil
	}
	e.pin.Unpin()
	e.bytes = nil
}

func (e *inference) size(dimension int) int {
	tensor := e.api.Input(e.interpreter, 0)
	if tensor == nil || dimension < 0 || dimension >= int(e.api.Dimensions(tensor)) {
		return 0
	}
	return int(e.api.Dimension(tensor, int32(dimension)))
}

func (e *inference) outputCount() int { return int(e.api.OutputCount(e.interpreter)) }

func (e *inference) output(index int, dst []float32) (int, error) {
	if index < 0 || index >= e.outputCount() {
		return 0, errors.New("invalid LiteRT output index")
	}
	tensor := e.api.Output(e.interpreter, int32(index))
	if tensor == nil {
		return 0, errors.New("missing LiteRT output tensor")
	}
	size := e.api.ByteSize(tensor)
	if size%4 != 0 {
		return 0, errors.New("invalid LiteRT float output size")
	}
	if dst != nil {
		if uintptr(len(dst))*4 != size || len(dst) == 0 || e.api.CopyTo(tensor, unsafe.Pointer(&dst[0]), size) != 0 {
			return 0, errors.New("cannot read LiteRT output tensor")
		}
	}
	return int(size / 4), nil
}

func (e *inference) run(input []float32) error {
	tensor := e.api.Input(e.interpreter, 0)
	if len(input) == 0 || tensor == nil || e.api.ByteSize(tensor) != uintptr(len(input))*4 {
		return errors.New("LiteRT input size mismatch")
	}
	if e.api.CopyFrom(tensor, unsafe.Pointer(&input[0]), uintptr(len(input))*4) != 0 {
		return errors.New("cannot copy LiteRT input tensor")
	}
	if status := e.api.Invoke(e.interpreter); status != 0 {
		return fmt.Errorf("LiteRT inference failed: %d", status)
	}
	return nil
}
