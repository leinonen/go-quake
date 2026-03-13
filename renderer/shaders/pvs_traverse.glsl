#version 430 core

layout(local_size_x = 64) in;

// SSBO 0: pre-decoded PVS bitset for the current leaf (uploaded by CPU).
// Bit (leafIdx-1) = 1 means leafIdx is visible from the player's current leaf.
layout(std430, binding = 0) readonly buffer PVSBitset {
    uint pvsBits[];
};

// SSBO 1: leaf descriptors
struct LeafDesc {
    uint visofs;           // unused here; kept for layout compatibility
    uint firstMarkSurface;
    uint numMarkSurfaces;
    uint _pad;
};
layout(std430, binding = 1) readonly buffer LeafTable {
    LeafDesc leafTable[];
};

// SSBO 2: marksurface indices (face indices)
layout(std430, binding = 2) readonly buffer MarkSurfaces {
    uint markSurfaces[];
};

// SSBO 3: output — one uint per face, 1 = visible
layout(std430, binding = 3) buffer VisibleFaces {
    uint visibleFaceFlags[];
};

// UBO per frame
layout(std140, binding = 0) uniform FrameUBO {
    uint totalLeafs;
};

// O(1) visibility check: single bit read from the precomputed PVS bitset.
bool isVisible(uint leafIdx) {
    if (leafIdx == 0u) return false; // void leaf is never visible
    uint bit  = leafIdx - 1u;
    uint word = bit / 32u;
    if (word >= uint(pvsBits.length())) return false;
    return (pvsBits[word] & (1u << (bit & 31u))) != 0u;
}

void main() {
    uint leafIdx = gl_GlobalInvocationID.x;
    if (leafIdx >= totalLeafs) return;

    if (!isVisible(leafIdx)) return;

    LeafDesc leaf = leafTable[leafIdx];
    for (uint i = 0u; i < leaf.numMarkSurfaces; i++) {
        uint faceIdx = markSurfaces[leaf.firstMarkSurface + i];
        visibleFaceFlags[faceIdx] = 1u;
    }
}
