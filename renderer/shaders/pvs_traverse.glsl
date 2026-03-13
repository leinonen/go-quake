#version 430 core

layout(local_size_x = 64) in;

// SSBO 0: raw PVS data (RLE compressed)
layout(std430, binding = 0) readonly buffer PVSData {
    uint pvsBytes[];
};

// SSBO 1: leaf descriptors
struct LeafDesc {
    uint visofs;            // byte offset into pvsBytes; 0xFFFFFFFF = no vis
    uint firstMarkSurface;
    uint numMarkSurfaces;
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
    uint currentLeaf;
    uint totalLeafs;
};

// Read a byte from the packed uint array
uint readPVSByte(uint byteOffset) {
    uint wordIdx = byteOffset / 4u;
    uint byteInWord = byteOffset % 4u;
    return (pvsBytes[wordIdx] >> (byteInWord * 8u)) & 0xFFu;
}

// Decode Quake RLE PVS: check if leafIdx is visible from currentLeaf
bool isVisible(uint srcLeaf, uint testLeaf) {
    LeafDesc src = leafTable[srcLeaf];
    if (src.visofs == 0xFFFFFFFFu) {
        return true; // no vis data = all visible
    }
    if (testLeaf == 0u) {
        return false; // void leaf never visible
    }

    // Quake PVS is 1-indexed: bit (testLeaf-1) in bitstream
    uint targetBit = testLeaf - 1u;

    uint byteOffset = src.visofs;
    uint currentBit = 0u;

    while (currentBit / 8u < (totalLeafs + 7u) / 8u) {
        uint b = readPVSByte(byteOffset);
        byteOffset++;

        if (b == 0u) {
            // RLE skip: next byte = count of 8-leaf groups to skip
            uint skip = readPVSByte(byteOffset);
            byteOffset++;
            currentBit += skip * 8u;
        } else {
            // 8 literal bits
            for (uint bit = 0u; bit < 8u; bit++) {
                if (currentBit == targetBit) {
                    return (b & (1u << bit)) != 0u;
                }
                currentBit++;
            }
        }

        if (currentBit > targetBit) {
            break;
        }
    }

    return false;
}

void main() {
    uint leafIdx = gl_GlobalInvocationID.x;
    if (leafIdx >= totalLeafs) return;

    if (!isVisible(currentLeaf, leafIdx)) return;

    LeafDesc leaf = leafTable[leafIdx];
    for (uint i = 0u; i < leaf.numMarkSurfaces; i++) {
        uint faceIdx = markSurfaces[leaf.firstMarkSurface + i];
        visibleFaceFlags[faceIdx] = 1u;
    }
}
