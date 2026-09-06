//go:build windows

package wasapi

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Core Audio is a COM API, so this file is mostly vtable dispatch. Interface
// handles are unsafe.Pointer rather than uintptr: they address memory COM owns,
// outside any Go span, so the collector ignores them — but keeping them typed
// avoids round-tripping through uintptr, which is what makes a pointer opaque to
// the collector and is exactly the mistake this API invites.

var (
	ole32 = windows.NewLazySystemDLL("ole32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	procPropVariantClear = ole32.NewProc("PropVariantClear")
)

var (
	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIMMEndpoint = windows.GUID{Data1: 0x1BE09788, Data2: 0x6894, Data3: 0x4089,
		Data4: [8]byte{0x85, 0x86, 0x9A, 0x2A, 0x6C, 0x26, 0x5A, 0xC5}}
	iidIAudioClient = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32,
		Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioCaptureClient = windows.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0,
		Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}

	ksSubtypePCM = windows.GUID{Data1: 0x00000001, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
	ksSubtypeIEEEFloat = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}

	pkeyDeviceFriendlyName = propertyKey{
		fmtID: windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD,
			Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}},
		pid: 14,
	}
)

const (
	clsctxAll = 0x17

	coinitMultithreaded = 0x0

	// EDataFlow
	eRender  = 0
	eCapture = 1

	// ERole
	eConsole = 0

	deviceStateActive = 0x1
	stgmRead          = 0

	audclntShareModeShared = 0

	streamFlagsLoopback          = 0x00020000
	streamFlagsSRCDefaultQuality = 0x08000000
	streamFlagsAutoConvertPCM    = 0x80000000

	bufferFlagsSilent = 0x2

	waveFormatTagPCM        = 0x0001
	waveFormatTagIEEEFloat  = 0x0003
	waveFormatTagExtensible = 0xFFFE

	vtLPWSTR = 31
)

// HRESULTs worth naming, because the raw system message for these is either
// missing or actively unhelpful.
const (
	sFalse                      hresult = 0x00000001
	rpcEChangedMode             hresult = 0x80010106
	audclntEDeviceInvalidated   hresult = 0x88890004
	audclntEUnsupportedFormat   hresult = 0x88890008
	audclntEDeviceInUse         hresult = 0x8889000A
	audclntEExclusiveNotAllowed hresult = 0x8889001A
	eAccessDenied               hresult = 0x80070005
)

var hresultText = map[hresult]string{
	audclntEDeviceInvalidated:   "the device was unplugged or disabled",
	audclntEUnsupportedFormat:   "Windows will not convert the format this device is using",
	audclntEDeviceInUse:         "another application has taken exclusive control of the device",
	audclntEExclusiveNotAllowed: "the device is set to allow exclusive access only",
	eAccessDenied:               "Windows denied access to the device (check microphone privacy settings)",
}

type hresult uint32

func (hr hresult) ok() bool { return hr&0x80000000 == 0 }

func (hr hresult) String() string {
	if text := hresultText[hr]; text != "" {
		return text
	}
	return fmt.Sprintf("%s (0x%08X)", syscall.Errno(hr), uint32(hr))
}

func (hr hresult) errorf(what string) error {
	return fmt.Errorf("%s: %s", what, hr)
}

// vtable is a COM object's method table. The length is a ceiling, not a fact:
// no interface used here has anywhere near this many methods, and only indices
// the interface actually defines are ever read.
type vtable [64]uintptr

// call invokes the method at index in obj's COM vtable. Every COM object begins
// with a pointer to its vtable, and every method takes the object itself as its
// first argument.
func call(obj unsafe.Pointer, index int, args ...uintptr) hresult {
	fn := (*(**vtable)(obj))[index]
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return hresult(uint32(r))
}

// release drops a reference. It is IUnknown::Release, at index 2 of every vtable.
func release(obj unsafe.Pointer) {
	if obj != nil {
		call(obj, 2)
	}
}

func coTaskMemFree(p unsafe.Pointer) {
	if p != nil {
		_, _, _ = syscall.SyscallN(procCoTaskMemFree.Addr(), uintptr(p))
	}
}

// comInit puts the calling thread into a COM apartment and returns the matching
// teardown.
//
// The caller must already have locked its goroutine to the thread: apartments
// belong to threads, not goroutines, so a goroutine that migrated mid-stream
// would leave its objects behind in an apartment it can no longer reach.
func comInit() (func(), error) {
	r, _, _ := syscall.SyscallN(procCoInitializeEx.Addr(), 0, coinitMultithreaded)
	switch hr := hresult(uint32(r)); {
	case hr.ok():
		return func() { _, _, _ = syscall.SyscallN(procCoUninitialize.Addr()) }, nil
	case hr == rpcEChangedMode:
		// Something already put this thread in a single-threaded apartment. That
		// works for Core Audio too; we simply must not tear down an apartment we
		// did not create.
		return func() {}, nil
	default:
		return nil, hr.errorf("initialise COM")
	}
}

// onCOMThread runs fn on a dedicated, COM-initialised OS thread and waits for it.
func onCOMThread(fn func() error) error {
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		uninit, err := comInit()
		if err != nil {
			result <- err
			return
		}
		defer uninit()

		result <- fn()
	}()
	return <-result
}

func newDeviceEnumerator() (unsafe.Pointer, error) {
	var enum unsafe.Pointer
	r, _, _ := syscall.SyscallN(procCoCreateInstance.Addr(),
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enum)),
	)
	if hr := hresult(uint32(r)); !hr.ok() {
		return nil, hr.errorf("open the Windows audio service")
	}
	return enum, nil
}

// ------------------------------------------------------------------ formats

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveFormatExtensible struct {
	Format      waveFormatEx
	Samples     uint16
	ChannelMask uint32
	SubFormat   windows.GUID
}

type propertyKey struct {
	fmtID windows.GUID
	pid   uint32
}

// propVariant is only ever read for PKEY_Device_FriendlyName, which is documented
// as VT_LPWSTR, so the union is declared as the one member that matters. Any
// other type leaves a small integer or zero in that field, which is not a valid
// heap address and so is ignored by the collector.
type propVariant struct {
	vt  uint16
	_   [3]uint16
	val *uint16
	_   uintptr
}

func formatOf(w *waveFormatEx) (Format, error) {
	f := Format{
		SampleRate:    int(w.SamplesPerSec),
		Channels:      int(w.Channels),
		BitsPerSample: int(w.BitsPerSample),
	}

	switch w.FormatTag {
	case waveFormatTagPCM:
	case waveFormatTagIEEEFloat:
		f.Float = true
	case waveFormatTagExtensible:
		if w.Size < 22 {
			return Format{}, errors.New("the device reported a malformed audio format")
		}
		ext := (*waveFormatExtensible)(unsafe.Pointer(w))
		switch ext.SubFormat {
		case ksSubtypePCM:
		case ksSubtypeIEEEFloat:
			f.Float = true
		default:
			return Format{}, fmt.Errorf("the device is using an audio format this app cannot decode (%s)", f)
		}
	default:
		return Format{}, fmt.Errorf("the device is using an audio format this app cannot decode (%s)", f)
	}

	return f, f.validate()
}

// ------------------------------------------------------------- enumeration

// Device is an audio endpoint that can be streamed.
type Device struct {
	ID   string
	Name string

	// IsOutput marks a render endpoint — speakers, headphones, or the input
	// side of a virtual cable. These are captured in loopback mode, so what the
	// device is playing is what gets streamed.
	IsOutput bool

	// IsDefault marks the endpoint Windows would use by default for its side of
	// the split (one output and one input can both be default).
	IsDefault bool
}

// Devices lists every active audio endpoint on the machine, outputs first.
//
// Both directions are listed because both are useful and both work: an output
// carries what the machine is playing, an input carries a microphone or the
// receiving end of a virtual cable.
func Devices() ([]Device, error) {
	var devices []Device
	err := onCOMThread(func() error {
		enum, err := newDeviceEnumerator()
		if err != nil {
			return err
		}
		defer release(enum)

		defaults := make(map[string]bool, 2)
		for _, flow := range []uintptr{eRender, eCapture} {
			if id, err := defaultEndpointID(enum, flow); err == nil {
				defaults[id] = true
			}
		}

		// One direction failing must not cost the user the other: a machine with
		// no recording devices at all is unusual but not broken, and it should
		// still be able to stream its speakers.
		var failure error
		for _, flow := range []uintptr{eRender, eCapture} {
			found, err := endpoints(enum, flow)
			if err != nil {
				failure = err
				continue
			}
			for i := range found {
				found[i].IsDefault = defaults[found[i].ID]
			}
			devices = append(devices, found...)
		}
		if len(devices) == 0 && failure != nil {
			return failure
		}
		return nil
	})

	if err == nil {
		outputs := 0
		for _, d := range devices {
			if d.IsOutput {
				outputs++
			}
		}
		slog.Info("enumerated audio endpoints",
			slog.Int("outputs", outputs),
			slog.Int("inputs", len(devices)-outputs))
	}
	return devices, err
}

func endpoints(enum unsafe.Pointer, flow uintptr) ([]Device, error) {
	var collection unsafe.Pointer
	if hr := call(enum, 3, flow, deviceStateActive, uintptr(unsafe.Pointer(&collection))); !hr.ok() {
		return nil, hr.errorf("list audio devices")
	}
	defer release(collection)

	var count uint32
	if hr := call(collection, 3, uintptr(unsafe.Pointer(&count))); !hr.ok() {
		return nil, hr.errorf("count audio devices")
	}

	devices := make([]Device, 0, count)
	for i := uint32(0); i < count; i++ {
		var dev unsafe.Pointer
		if hr := call(collection, 4, uintptr(i), uintptr(unsafe.Pointer(&dev))); !hr.ok() {
			// One endpoint with a broken driver must not hide all the others.
			continue
		}
		if id, err := endpointID(dev); err == nil {
			devices = append(devices, Device{
				ID:       id,
				Name:     friendlyName(dev, id),
				IsOutput: flow == eRender,
			})
		}
		release(dev)
	}
	return devices, nil
}

func defaultEndpointID(enum unsafe.Pointer, flow uintptr) (string, error) {
	var dev unsafe.Pointer
	if hr := call(enum, 4, flow, eConsole, uintptr(unsafe.Pointer(&dev))); !hr.ok() {
		return "", hr.errorf("find the default audio device")
	}
	defer release(dev)
	return endpointID(dev)
}

func endpointID(dev unsafe.Pointer) (string, error) {
	var p *uint16
	if hr := call(dev, 5, uintptr(unsafe.Pointer(&p))); !hr.ok() {
		return "", hr.errorf("read an audio device's id")
	}
	defer coTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p), nil
}

// friendlyName reads the label Windows shows in its own sound settings. A device
// whose property store cannot be read is still usable, so its opaque id stands
// in rather than the device being dropped.
func friendlyName(dev unsafe.Pointer, fallback string) string {
	var store unsafe.Pointer
	if hr := call(dev, 4, stgmRead, uintptr(unsafe.Pointer(&store))); !hr.ok() {
		return fallback
	}
	defer release(store)

	var pv propVariant
	if hr := call(store, 5, uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)), uintptr(unsafe.Pointer(&pv))); !hr.ok() {
		return fallback
	}
	defer func() { _, _, _ = syscall.SyscallN(procPropVariantClear.Addr(), uintptr(unsafe.Pointer(&pv))) }()

	if pv.vt != vtLPWSTR {
		return fallback
	}
	if name := windows.UTF16PtrToString(pv.val); name != "" {
		return name
	}
	return fallback
}

func isRenderEndpoint(dev unsafe.Pointer) (bool, error) {
	var endpoint unsafe.Pointer
	if hr := call(dev, 0, uintptr(unsafe.Pointer(&iidIMMEndpoint)), uintptr(unsafe.Pointer(&endpoint))); !hr.ok() {
		return false, hr.errorf("identify the audio device")
	}
	defer release(endpoint)

	var flow uint32
	if hr := call(endpoint, 3, uintptr(unsafe.Pointer(&flow))); !hr.ok() {
		return false, hr.errorf("identify the audio device")
	}
	return flow == eRender, nil
}
