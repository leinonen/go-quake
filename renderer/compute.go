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
	m               *bsp.Map
	ssboPVSBitset   uint32 // binding 0: precomputed PVS bitset for current leaf
	ssboLeafTable   uint32 // binding 1
	ssboMarkSurface uint32 // binding 2
	ssboVisFlags    uint32 // binding 3
	ubo             uint32

	computeProg  uint32
	numFaces     uint32
	numLeafs     uint32
	lastLeaf     int // last leaf for which the PVS bitset was uploaded
}

// uboData matches the GLSL FrameUBO layout (std140).
type uboData struct {
	TotalLeafs uint32
	_pad       [3]uint32
}

// InitCompute uploads BSP leaf/marksurface data to GPU SSBOs.
func InitCompute(m *bsp.Map, computeSrc string) (*ComputeState, error) {
	prog, err := compileCompute(computeSrc)
	if err != nil {
		return nil, fmt.Errorf("compile compute shader: %w", err)
	}

	cs := &ComputeState{
		m:           m,
		computeProg: prog,
		numFaces:    uint32(len(m.Faces)),
		numLeafs:    uint32(len(m.Leaves)),
		lastLeaf:    -1, // force upload on first frame
	}

	// SSBO 0: PVS bitset — one bit per leaf, updated by CPU each time the player
	// changes leaf. Size: ceil(numLeafs/32) uint32s.
	bitsetSize := ((len(m.Leaves) + 31) / 32) * 4
	if bitsetSize == 0 {
		bitsetSize = 4
	}
	var pvsSsbo uint32
	gl.GenBuffers(1, &pvsSsbo)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, pvsSsbo)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, bitsetSize, nil, gl.DYNAMIC_DRAW)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, pvsSsbo)
	cs.ssboPVSBitset = pvsSsbo

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

	// SSBO 3: visible face flags (output) — zeroed each frame
	cs.ssboVisFlags = makeSSBOEmpty(3, int(cs.numFaces)*4)

	// UBO
	gl.GenBuffers(1, &cs.ubo)
	gl.BindBuffer(gl.UNIFORM_BUFFER, cs.ubo)
	gl.BufferData(gl.UNIFORM_BUFFER, int(unsafe.Sizeof(uboData{})), nil, gl.DYNAMIC_DRAW)
	gl.BindBufferBase(gl.UNIFORM_BUFFER, 0, cs.ubo)

	return cs, nil
}

// Dispatch zeros visFlags, updates the PVS bitset if the leaf changed, and
// dispatches the compute shader.
func (cs *ComputeState) Dispatch(currentLeaf int) {
	// Re-upload PVS bitset whenever the player changes leaf.
	if currentLeaf != cs.lastLeaf {
		cs.lastLeaf = currentLeaf

		var visofs int32 = -1
		if currentLeaf >= 0 && currentLeaf < len(cs.m.Leaves) {
			visofs = cs.m.Leaves[currentLeaf].VisOfs
		}
		pvs := bsp.DecompressPVS(cs.m.VisData, visofs, len(cs.m.Leaves))
		bitset := pvsToBitset(pvs, len(cs.m.Leaves))

		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, cs.ssboPVSBitset)
		gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(bitset)*4, unsafe.Pointer(&bitset[0]))
	}

	// Zero output SSBO
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, cs.ssboVisFlags)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, nil)

	// Update UBO
	data := uboData{TotalLeafs: cs.numLeafs}
	gl.BindBuffer(gl.UNIFORM_BUFFER, cs.ubo)
	gl.BufferSubData(gl.UNIFORM_BUFFER, 0, int(unsafe.Sizeof(data)), unsafe.Pointer(&data))

	// Bind SSBOs
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, cs.ssboPVSBitset)
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

// pvsToBitset converts a decompressed PVS byte slice to a uint32 bitset.
// nil pvs means all-visible (all bits set).
// Bit (leafIdx-1) of the result indicates whether leafIdx is visible.
func pvsToBitset(pvs []byte, numLeafs int) []uint32 {
	size := (numLeafs + 31) / 32
	if size == 0 {
		size = 1
	}
	result := make([]uint32, size)
	if pvs == nil {
		for i := range result {
			result[i] = 0xFFFFFFFF
		}
		return result
	}
	for i, b := range pvs {
		w := i / 4
		if w >= size {
			break
		}
		result[w] |= uint32(b) << uint((i%4)*8)
	}
	return result
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
		size = 4
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
