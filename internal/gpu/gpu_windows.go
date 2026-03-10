// GPU utilisation monitor — Windows implementation via HWiNFO Shared Memory.
//
// Reads GPU load percentage from HWiNFO's shared memory region.
// Requires HWiNFO running with Shared Memory Support enabled.
// Falls back to nvidia-smi if HWiNFO is unavailable.

//go:build windows

package gpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const (
	hwinfoSMName    = "Global\\HWiNFO_SENS_SM2"
	hwinfoSMNameOld = "Global\\HWiNFO_SENS_SM"
	hwinfoSignature = 0x53695748 // 'HWiS' little-endian
	sensorUsage     = 7
)

// HWiNFO shared memory struct sizes and offsets.
const (
	headerSize  = 44 // dwSignature(4) + dwVersion(4) + dwRevision(4) + pollTime(8) + 6×uint32(24)
	sensorSize  = 264 // dwSensorID(4) + dwSensorInst(4) + nameOrig(128) + nameUser(128)
	readingSize = 424 // tReading(4) + dwSensorIndex(4) + dwReadingID(4) + labelOrig(128) + labelUser(128) + unit(16) + 4×float64(32) + padding
)

// readGPU reads GPU utilisation. Tries HWiNFO first, falls back to nvidia-smi.
func readGPU() (*float64, *string) {
	pct, label := readHWiNFO()
	if pct != nil {
		return pct, label
	}
	// Fall back to nvidia-smi
	return readNvidiaSMI()
}

// readHWiNFO reads GPU utilisation from HWiNFO shared memory.
func readHWiNFO() (*float64, *string) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openFileMapping := kernel32.NewProc("OpenFileMappingW")
	mapViewOfFile := kernel32.NewProc("MapViewOfFile")
	unmapViewOfFile := kernel32.NewProc("UnmapViewOfFile")
	closeHandle := kernel32.NewProc("CloseHandle")

	var hMap uintptr
	for _, name := range []string{hwinfoSMName, hwinfoSMNameOld} {
		namePtr, _ := syscall.UTF16PtrFromString(name)
		h, _, _ := openFileMapping.Call(0x0004, 0, uintptr(unsafe.Pointer(namePtr)))
		if h != 0 {
			hMap = h
			break
		}
	}
	if hMap == 0 {
		msg := "HWiNFO shared memory not available"
		return nil, &msg
	}
	defer closeHandle.Call(hMap)

	// Map header to get offsets
	pView, _, _ := mapViewOfFile.Call(hMap, 0x0004, 0, 0, uintptr(headerSize))
	if pView == 0 {
		return nil, nil
	}

	hdrBytes := make([]byte, headerSize)
	copy(hdrBytes, unsafe.Slice((*byte)(unsafe.Pointer(pView)), headerSize))
	unmapViewOfFile.Call(pView)

	sig := binary.LittleEndian.Uint32(hdrBytes[0:4])
	if sig != hwinfoSignature {
		msg := "invalid HWiNFO signature"
		return nil, &msg
	}

	// Parse header fields
	offSensor := binary.LittleEndian.Uint32(hdrBytes[20:24])
	sizeSensor := binary.LittleEndian.Uint32(hdrBytes[24:28])
	numSensor := binary.LittleEndian.Uint32(hdrBytes[28:32])
	offReading := binary.LittleEndian.Uint32(hdrBytes[32:36])
	sizeReading := binary.LittleEndian.Uint32(hdrBytes[36:40])
	numReading := binary.LittleEndian.Uint32(hdrBytes[40:44])

	// Calculate total size needed
	totalSize := offSensor + sizeSensor*numSensor
	readEnd := offReading + sizeReading*numReading
	if readEnd > totalSize {
		totalSize = readEnd
	}

	// Map full region
	pView, _, _ = mapViewOfFile.Call(hMap, 0x0004, 0, 0, uintptr(totalSize))
	if pView == 0 {
		return nil, nil
	}

	snapshot := make([]byte, totalSize)
	copy(snapshot, unsafe.Slice((*byte)(unsafe.Pointer(pView)), totalSize))
	unmapViewOfFile.Call(pView)

	// Build sensor name lookup
	sensorNames := make(map[uint32]string)
	for i := uint32(0); i < numSensor; i++ {
		off := offSensor + i*sizeSensor
		if off+sensorSize > totalSize {
			break
		}
		// Read sensor name (user name at offset 136, orig at offset 8)
		nameUser := cString(snapshot[off+136 : off+264])
		nameOrig := cString(snapshot[off+8 : off+136])
		name := nameUser
		if name == "" {
			name = nameOrig
		}
		sensorNames[i] = name
	}

	// Find GPU utilisation reading
	type gpuReading struct {
		label string
		value float64
	}
	var primary, secondary []gpuReading

	for i := uint32(0); i < numReading; i++ {
		off := offReading + i*sizeReading
		if off+readingSize > totalSize {
			break
		}

		tReading := binary.LittleEndian.Uint32(snapshot[off : off+4])
		if tReading != sensorUsage {
			continue
		}

		sensorIdx := binary.LittleEndian.Uint32(snapshot[off+4 : off+8])
		labelUser := cString(snapshot[off+140 : off+268])
		labelOrig := cString(snapshot[off+12 : off+140])
		unit := cString(snapshot[off+268 : off+284])
		value := math.Float64frombits(binary.LittleEndian.Uint64(snapshot[off+284 : off+292]))

		label := labelUser
		if label == "" {
			label = labelOrig
		}
		sensor := sensorNames[sensorIdx]
		lu := strings.ToUpper(label)
		su := strings.ToUpper(sensor)

		if strings.Contains(lu, "GPU") &&
			(strings.Contains(lu, "UTILIZATION") || strings.Contains(lu, "CORE LOAD") || strings.Contains(lu, "GPU LOAD")) {
			primary = append(primary, gpuReading{label, value})
		} else if strings.Contains(su, "GPU") && strings.TrimSpace(unit) == "%" {
			secondary = append(secondary, gpuReading{label, value})
		}
	}

	if len(primary) > 0 {
		v := math.Round(primary[0].value*10) / 10
		return &v, &primary[0].label
	}
	if len(secondary) > 0 {
		v := math.Round(secondary[0].value*10) / 10
		return &v, &secondary[0].label
	}

	msg := "no GPU reading found"
	return nil, &msg
}

// cString extracts a null-terminated string from a byte slice.
func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// readNvidiaSMI reads GPU utilisation via nvidia-smi as a fallback.
func readNvidiaSMI() (*float64, *string) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, nil
	}
	text := strings.TrimSpace(string(out))
	var pct float64
	if _, err := fmt.Sscanf(text, "%f", &pct); err == nil {
		label := "nvidia-smi"
		return &pct, &label
	}
	return nil, nil
}
