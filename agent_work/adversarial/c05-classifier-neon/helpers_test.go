package adv_test

import (
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

const defaultSeed uint64 = 2026112605

func advSeed(t *testing.T) uint64 {
	t.Helper()
	seed := defaultSeed
	if raw := os.Getenv("ADV_SEED"); raw != "" {
		v, err := strconv.ParseUint(raw, 0, 64)
		if err != nil {
			t.Fatalf("bad ADV_SEED %q: %v", raw, err)
		}
		seed = v
	}
	t.Logf("seed=%d", seed)
	return seed
}

func advRNG(t *testing.T, stream uint64) *rand.Rand {
	t.Helper()
	return rand.New(rand.NewPCG(advSeed(t), stream))
}

func advLong() bool {
	return os.Getenv("ADV_LONG") == "1"
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// agent_work/adversarial/c05-classifier-neon/helpers_test.go -> repo root is 3 dirs up.
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found at %s: %v", root, err)
	}
	return root
}

type rawFrame struct {
	typ  byte
	body []byte
}

func decodeWireFrames(t *testing.T, b []byte) []rawFrame {
	t.Helper()
	var frames []rawFrame
	off := 0
	for off < len(b) {
		if len(b)-off < 5 {
			t.Fatalf("truncated wire frame at offset %d: remaining %x", off, b[off:])
		}
		typ := b[off]
		n := binary.BigEndian.Uint32(b[off+1 : off+5])
		if n < 4 || uint64(off)+1+uint64(n) > uint64(len(b)) {
			t.Fatalf("invalid wire frame length %d at offset %d (total %d)", n, off, len(b))
		}
		body := b[off+5 : off+1+int(n)]
		frames = append(frames, rawFrame{typ: typ, body: body})
		off += 1 + int(n)
	}
	return frames
}

func decodeErrorFields(t *testing.T, body []byte) map[byte]string {
	t.Helper()
	if len(body) == 0 || body[len(body)-1] != 0 {
		t.Fatalf("ErrorResponse body does not end with NUL terminator: %x", body)
	}
	fields := map[byte]string{}
	i := 0
	for i < len(body) {
		code := body[i]
		if code == 0 {
			if i != len(body)-1 {
				t.Fatalf("trailing bytes after ErrorResponse terminator at %d/%d: %x", i, len(body), body)
			}
			break
		}
		i++
		start := i
		for i < len(body) && body[i] != 0 {
			i++
		}
		if i >= len(body) {
			t.Fatalf("unterminated field %q in ErrorResponse: %x", code, body)
		}
		if _, dup := fields[code]; dup {
			t.Fatalf("duplicate field %q in ErrorResponse", code)
		}
		fields[code] = string(body[start:i])
		i++
	}
	return fields
}
