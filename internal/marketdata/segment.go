// Package marketdata implements raw market-data recording and
// deterministic replay (docs/data-flow.md §5, SKILL.md §63–§64): append-
// only zstd-compressed segment files of length-prefixed frames, carrying
// both WS frames and the REST snapshots used for splices, so replay
// drives the exact same decode → validate → apply code path as live.
package marketdata

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Direction of a recorded frame.
type Direction uint8

const (
	DirWS   Direction = 1 // inbound WebSocket frame
	DirREST Direction = 2 // REST snapshot body (synthetic frame)
)

// Frame is one recorded unit. StreamID identifies the source stream
// within the session (assigned by the recorder; 0 is valid).
type Frame struct {
	Recv     time.Time
	Dir      Direction
	StreamID uint16
	Payload  []byte
}

const frameHeaderLen = 8 + 1 + 2 + 4 // ts | dir | stream | len

// maxFramePayload guards readers against corrupt length prefixes.
const maxFramePayload = 32 << 20

// SegmentMeta describes one closed segment file.
type SegmentMeta struct {
	Path   string    `json:"path"`
	FromTS time.Time `json:"from_ts"`
	ToTS   time.Time `json:"to_ts"`
	Frames int64     `json:"frames"`
	Bytes  int64     `json:"bytes"` // compressed on-disk size
	SHA256 string    `json:"sha256"`
}

// SegmentWriter appends frames to one zstd-compressed file.
type SegmentWriter struct {
	path    string
	file    *os.File
	zw      *zstd.Encoder
	buf     *bufio.Writer
	digest  hash.Hash
	scratch [frameHeaderLen]byte

	frames int64
	first  time.Time
	last   time.Time
}

// NewSegmentWriter creates the file (directories included).
func NewSegmentWriter(path string) (*SegmentWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path built from configured dir + session id
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	// Digest covers the compressed byte stream (what sits on disk).
	zw, err := zstd.NewWriter(io.MultiWriter(f, digest))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &SegmentWriter{
		path: path, file: f, zw: zw,
		buf: bufio.NewWriterSize(zw, 64<<10), digest: digest,
	}, nil
}

// Append writes one frame.
func (w *SegmentWriter) Append(fr Frame) error {
	if len(fr.Payload) > maxFramePayload {
		return fmt.Errorf("marketdata: frame payload %d exceeds cap", len(fr.Payload))
	}
	binary.BigEndian.PutUint64(w.scratch[0:8], uint64(fr.Recv.UnixNano())) //nolint:gosec // ns timestamps fit
	w.scratch[8] = byte(fr.Dir)
	binary.BigEndian.PutUint16(w.scratch[9:11], fr.StreamID)
	binary.BigEndian.PutUint32(w.scratch[11:15], uint32(len(fr.Payload))) //nolint:gosec // capped above
	if _, err := w.buf.Write(w.scratch[:]); err != nil {
		return err
	}
	if _, err := w.buf.Write(fr.Payload); err != nil {
		return err
	}
	if w.frames == 0 {
		w.first = fr.Recv
	}
	w.last = fr.Recv
	w.frames++
	return nil
}

// Frames written so far (rotation decisions).
func (w *SegmentWriter) Frames() int64 { return w.frames }

// Close flushes and returns the segment metadata.
func (w *SegmentWriter) Close() (SegmentMeta, error) {
	if err := w.buf.Flush(); err != nil {
		return SegmentMeta{}, err
	}
	if err := w.zw.Close(); err != nil {
		return SegmentMeta{}, err
	}
	if err := w.file.Close(); err != nil {
		return SegmentMeta{}, err
	}
	info, err := os.Stat(w.path)
	if err != nil {
		return SegmentMeta{}, err
	}
	return SegmentMeta{
		Path:   w.path,
		FromTS: w.first, ToTS: w.last,
		Frames: w.frames,
		Bytes:  info.Size(),
		SHA256: hex.EncodeToString(w.digest.Sum(nil)),
	}, nil
}

// ErrCorruptSegment marks a torn/invalid frame; frames before it are
// valid (append-only files can end mid-frame on crash).
var ErrCorruptSegment = errors.New("marketdata: corrupt segment frame")

// ReadSegment streams frames to fn until EOF, a torn tail (returned as
// ErrCorruptSegment after yielding intact frames), or fn error.
func ReadSegment(path string, fn func(Frame) error) error {
	f, err := os.Open(path) //nolint:gosec // paths come from recorded metadata
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	r := bufio.NewReaderSize(zr, 64<<10)

	var header [frameHeaderLen]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return ErrCorruptSegment
			}
			return err
		}
		n := binary.BigEndian.Uint32(header[11:15])
		if n > maxFramePayload {
			return ErrCorruptSegment
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return ErrCorruptSegment
		}
		fr := Frame{
			Recv:     time.Unix(0, int64(binary.BigEndian.Uint64(header[0:8]))), //nolint:gosec // symmetric with writer
			Dir:      Direction(header[8]),
			StreamID: binary.BigEndian.Uint16(header[9:11]),
			Payload:  payload,
		}
		if err := fn(fr); err != nil {
			return err
		}
	}
}
