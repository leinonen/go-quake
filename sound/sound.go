// Package sound provides OpenAL-based audio playback for go-quake.
// All sounds are loaded from the PAK file at startup; missing files are silently skipped.
package sound

// #cgo LDFLAGS: -lopenal
// #include <AL/al.h>
// #include <AL/alc.h>
// #include <stdlib.h>
import "C"
import (
	"fmt"
	"log"
	"unsafe"
)

// SoundEvent identifies a game sound to play.
type SoundEvent int

const (
	SndAxeSwing SoundEvent = iota
	SndAxeHit
	SndShotgun
	SndSuperShotgun
	SndNailgun
	SndSuperNailgun
	SndRocket
	SndGrenade
	SndLightning
	SndItemPickup
	sndCount // sentinel — keep last
)

// soundPaths maps each SoundEvent to its PAK path.
var soundPaths = [sndCount]string{
	SndAxeSwing:     "sound/weapons/ax1.wav",
	SndAxeHit:       "sound/weapons/ax1.wav",
	SndShotgun:      "sound/weapons/guncock.wav",
	SndSuperShotgun: "sound/weapons/shotgn2.wav",
	SndNailgun:      "sound/weapons/rocket1i.wav",
	SndSuperNailgun: "sound/weapons/rocket1i.wav",
	SndRocket:       "sound/weapons/sgun1.wav",
	SndGrenade:      "sound/weapons/bounce.wav",
	SndLightning:    "sound/weapons/lhit.wav",
	SndItemPickup: "sound/misc/basekey.wav",
}

const sourcePoolSize = 16

// Manager holds all OpenAL state.
type Manager struct {
	device      *C.ALCdevice
	ctx         *C.ALCcontext
	buffers     [sndCount]C.ALuint    // 0 = not loaded
	pathBuffers map[string]C.ALuint   // dynamic path-keyed buffers
	sources     [sourcePoolSize]C.ALuint
}

var mgr *Manager

// Init opens the OpenAL device/context and loads all sound buffers.
// readFile is called for each sound path; missing files are silently skipped.
// Call Cleanup() when done.
func Init(readFile func(string) ([]byte, error)) error {
	m := &Manager{}

	m.device = C.alcOpenDevice(nil)
	if m.device == nil {
		return fmt.Errorf("sound: alcOpenDevice failed")
	}

	m.ctx = C.alcCreateContext(m.device, nil)
	if m.ctx == nil {
		C.alcCloseDevice(m.device)
		return fmt.Errorf("sound: alcCreateContext failed")
	}
	C.alcMakeContextCurrent(m.ctx)

	// Pre-allocate source pool
	C.alGenSources(C.ALsizei(sourcePoolSize), &m.sources[0])

	// Load all sound buffers
	loaded := 0
	for id := SoundEvent(0); id < sndCount; id++ {
		path := soundPaths[id]
		if path == "" {
			continue
		}
		// Check if same path already loaded (e.g. SndAxeSwing and SndAxeHit share ax1.wav)
		duplicate := false
		for prev := SoundEvent(0); prev < id; prev++ {
			if soundPaths[prev] == path {
				m.buffers[id] = m.buffers[prev]
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}

		data, err := readFile(path)
		if err != nil {
			// Missing file — silently skip
			continue
		}
		wav, err := decodeWAV(data)
		if err != nil {
			log.Printf("sound: decode %s: %v", path, err)
			continue
		}

		var buf C.ALuint
		C.alGenBuffers(1, &buf)
		C.alBufferData(buf,
			C.ALenum(wav.format),
			unsafe.Pointer(&wav.data[0]),
			C.ALsizei(len(wav.data)),
			C.ALsizei(wav.sampleRate),
		)
		m.buffers[id] = buf
		loaded++
	}

	m.pathBuffers = make(map[string]C.ALuint)
	log.Printf("sound: loaded %d sounds", loaded)
	mgr = m
	return nil
}

// PreloadPaths loads additional sounds by PAK path into the dynamic buffer map.
// Missing or undecodable files are silently skipped.
func PreloadPaths(paths []string, readFile func(string) ([]byte, error)) {
	if mgr == nil {
		return
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := mgr.pathBuffers[path]; ok {
			continue // already loaded
		}
		data, err := readFile(path)
		if err != nil {
			continue
		}
		wav, err := decodeWAV(data)
		if err != nil {
			log.Printf("sound: decode %s: %v", path, err)
			continue
		}
		var buf C.ALuint
		C.alGenBuffers(1, &buf)
		C.alBufferData(buf,
			C.ALenum(wav.format),
			unsafe.Pointer(&wav.data[0]),
			C.ALsizei(len(wav.data)),
			C.ALsizei(wav.sampleRate),
		)
		mgr.pathBuffers[path] = buf
	}
}

// PlayPath plays a sound identified by its PAK path.
// No-op if the path was not pre-loaded or the manager is nil.
func PlayPath(path string) {
	if mgr == nil || path == "" {
		return
	}
	buf, ok := mgr.pathBuffers[path]
	if !ok || buf == 0 {
		return
	}
	playBuf(buf)
}

// Play plays the sound for the given event on an available source.
// Non-blocking: if all sources are busy the sound is dropped.
func Play(id SoundEvent) {
	if mgr == nil || id < 0 || id >= sndCount {
		return
	}
	playBuf(mgr.buffers[id])
}

// playBuf plays an AL buffer on a free source from the pool.
func playBuf(buf C.ALuint) {
	if buf == 0 {
		return
	}
	for i := range mgr.sources {
		src := mgr.sources[i]
		var state C.ALint
		C.alGetSourcei(src, C.AL_SOURCE_STATE, &state)
		if state == C.AL_INITIAL || state == C.AL_STOPPED {
			C.alSourcei(src, C.AL_BUFFER, C.ALint(buf))
			C.alSourcePlay(src)
			return
		}
	}
	// All sources busy — drop the sound
}

// Cleanup stops all sources and releases OpenAL resources.
func Cleanup() {
	if mgr == nil {
		return
	}
	for i := range mgr.sources {
		C.alSourceStop(mgr.sources[i])
	}
	C.alDeleteSources(C.ALsizei(sourcePoolSize), &mgr.sources[0])

	// Delete unique buffers only
	deleted := map[C.ALuint]bool{}
	for _, buf := range mgr.buffers {
		if buf != 0 && !deleted[buf] {
			C.alDeleteBuffers(1, &buf)
			deleted[buf] = true
		}
	}
	for _, buf := range mgr.pathBuffers {
		if buf != 0 && !deleted[buf] {
			C.alDeleteBuffers(1, &buf)
			deleted[buf] = true
		}
	}

	C.alcMakeContextCurrent(nil)
	C.alcDestroyContext(mgr.ctx)
	C.alcCloseDevice(mgr.device)
	mgr = nil
}
