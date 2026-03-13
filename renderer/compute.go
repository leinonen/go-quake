package renderer

import (
	"fmt"
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
	"go-quake/bsp"
)

// LeafDescGPU mirrors the GLSL LeafDesc struct (std430).
type LeafDescGPU struct {
	VisOfs           uint32
	FirstMarkSurface uint32
	NumMarkSurfaces  uint32
	_pad             uint32
}

// ComputeState holds SSBO handles and compute program.
type ComputeState struct {
	ssboPVS         uint32 // binding 0
	ssboLeafTable   uint32 // binding 1
	ssboMarkSurface uint32 // binding 2
	ssboVisFlags    uint32 // binding 3
	ubo             uint32 // binding 0

	computeProg uint32
	numFaces    uint32
	numLeafs    uint32
}

// uboData matches the GLSL FrameUBO layout (std140).
type uboData struct {
	CurrentLeaf uint32
	TotalLeafs  uint32
	_pad        [2]uint32
}

// InitCompute uploads all BSP vis data to GPU SSBOs.
func InitCompute(m *bsp.Map, computeSrc string) (*ComputeState, error) {
	prog, err := compileCompute(computeSrc)
	if err != nil {
		return nil, fmt.Errorf("compile compute shader: %w", err)
	}

	cs := &ComputeState{
		computeProg: prog,
		numFaces:    uint32(len(m.Faces)),
		numLeafs:    uint32(len(m.Leaves)),
	}

	// SSBO 0: PVS bytes — pack into uint32 array
	pvsUints := bytesToUint32(m.VisData)
	cs.ssboPVS = makeSSBO(0, pvsUints)

	// SSBO 1: leaf descriptors
	leafDescs := make([]LeafDescGPU, len(m.Leaves))
	for i, leaf := range m.Leaves {
		visofs := uint32(0xFFFFFFFF)
		if leaf.VisOfs >= 0 {
			visofs = uint32(leaf.VisOfs)
		}
		leafDescs[i] = LeafDescGPU{
			VisOfs:           visofs,
			FirstMarkSurface: uint32(leaf.FirstMarkSurface),
			NumMarkSurfaces:  uint32(leaf.NumMarkSurfaces),
		}
	}
	cs.ssboLeafTable = makeSSBO(1, leafDescs)

	// SSBO 2: marksurfaces as uint32
	ms := make([]uint32, len(m.MarkSurfaces))
	for i, v := range m.MarkSurfaces {
		ms[i] = uint32(v)
	}
	cs.ssboMarkSurface = makeSSBO(2, ms)

	// SSBO 3: visible face flags (output) — allocated, zeroed each frame
	cs.ssboVisFlags = makeSSBOEmpty(3, int(cs.numFaces)*4)

	// UBO
	gl.GenBuffers(1, &cs.ubo)
	gl.BindBuffer(gl.UNIFORM_BUFFER, cs.ubo)
	gl.BufferData(gl.UNIFORM_BUFFER, int(unsafe.Sizeof(uboData{})), nil, gl.DYNAMIC_DRAW)
	gl.BindBufferBase(gl.UNIFORM_BUFFER, 0, cs.ubo)

	return cs, nil
}

// Dispatch zeros visFlags SSBO, updates UBO, dispatches compute shader.
func (cs *ComputeState) Dispatch(currentLeaf uint32) {
	// Zero output SSBO
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, cs.ssboVisFlags)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, nil)

	// Update UBO
	data := uboData{CurrentLeaf: currentLeaf, TotalLeafs: cs.numLeafs}
	gl.BindBuffer(gl.UNIFORM_BUFFER, cs.ubo)
	gl.BufferSubData(gl.UNIFORM_BUFFER, 0, int(unsafe.Sizeof(data)), unsafe.Pointer(&data))

	// Bind SSBOs
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, cs.ssboPVS)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, cs.ssboLeafTable)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, cs.ssboMarkSurface)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, cs.ssboVisFlags)
	gl.BindBufferBase(gl.UNIFORM_BUFFER, 0, cs.ubo)

	gl.UseProgram(cs.computeProg)
	groups := (cs.numLeafs + 63) / 64
	gl.DispatchCompute(groups, 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
}

// BindVisFlags binds the visible face flags SSBO for the fragment shader.
func (cs *ComputeState) BindVisFlags() {
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, cs.ssboVisFlags)
}

// CountVisible reads back visible face count (debug).
func (cs *ComputeState) CountVisible() int {
	flags := make([]uint32, cs.numFaces)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, cs.ssboVisFlags)
	gl.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(flags)*4,
		unsafe.Pointer(&flags[0]))
	count := 0
	for _, f := range flags {
		if f != 0 {
			count++
		}
	}
	return count
}

func bytesToUint32(b []byte) []uint32 {
	if len(b) == 0 {
		return []uint32{0} // non-empty so GL doesn't complain
	}
	// Pad to multiple of 4
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = uint32(b[i*4]) |
			uint32(b[i*4+1])<<8 |
			uint32(b[i*4+2])<<16 |
			uint32(b[i*4+3])<<24
	}
	return out
}

func makeSSBO(binding int, data interface{}) uint32 {
	var ssbo uint32
	gl.GenBuffers(1, &ssbo)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, ssbo)

	var ptr unsafe.Pointer
	var size int
	switch d := data.(type) {
	case []uint32:
		if len(d) > 0 {
			ptr = unsafe.Pointer(&d[0])
		}
		size = len(d) * 4
	case []LeafDescGPU:
		if len(d) > 0 {
			ptr = unsafe.Pointer(&d[0])
		}
		size = len(d) * int(unsafe.Sizeof(LeafDescGPU{}))
	}

	if size == 0 {
		size = 4 // ensure non-zero
	}
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, size, ptr, gl.STATIC_DRAW)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(binding), ssbo)
	return ssbo
}

func makeSSBOEmpty(binding int, size int) uint32 {
	if size == 0 {
		size = 4
	}
	var ssbo uint32
	gl.GenBuffers(1, &ssbo)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, ssbo)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, size, nil, gl.DYNAMIC_DRAW)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(binding), ssbo)
	return ssbo
}
