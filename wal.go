package runneldb

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

const (
	walMagic      = "RNDB"
	walVersion    = uint32(1)
	walHeaderSize = 16
	maxRecordSize = 64 << 20
	recordHdrSize = 8
)

type operation struct {
	Key    string `json:"key"`
	Value  []byte `json:"value,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

type record struct {
	Version uint64      `json:"version"`
	Ops     []operation `json:"ops"`
}

func writeWALHeader(file *os.File) error {
	var hdr [walHeaderSize]byte
	copy(hdr[:4], walMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], walVersion)
	if _, err := file.WriteAt(hdr[:], 0); err != nil {
		return err
	}
	return file.Sync()
}

func readWALHeader(file *os.File) error {
	var hdr [walHeaderSize]byte
	if _, err := file.ReadAt(hdr[:], 0); err != nil {
		return fmt.Errorf("runneldb: reading WAL header: %w", err)
	}
	if string(hdr[:4]) != walMagic {
		return ErrWALMagic
	}
	version := binary.LittleEndian.Uint32(hdr[4:8])
	if version != walVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrWALVersion, version, walVersion)
	}
	return nil
}

// initWAL prepares an empty or existing WAL: writes a header on empty files,
// validates the header otherwise, then replays records with torn-tail repair.
func initWAL(file *os.File, apply func(record)) (lastGood int64, err error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() == 0 {
		if err := writeWALHeader(file); err != nil {
			return 0, err
		}
		if _, err := file.Seek(walHeaderSize, io.SeekStart); err != nil {
			return 0, err
		}
		return walHeaderSize, nil
	}
	if info.Size() < walHeaderSize {
		return 0, ErrWALMagic
	}
	if err := readWALHeader(file); err != nil {
		return 0, err
	}
	return replay(file, apply)
}

func encodeRecord(rec record) ([]byte, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	out := make([]byte, recordHdrSize+len(payload))
	binary.LittleEndian.PutUint32(out[:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(out[4:8], crc32.ChecksumIEEE(payload))
	copy(out[recordHdrSize:], payload)
	return out, nil
}

func appendRecord(file *os.File, rec record) error {
	frame, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	if _, err = file.Write(frame); err != nil {
		return err
	}
	return file.Sync()
}

// replay reads records after the file header. Torn or corrupt data at the tail
// is reported via lastGood so the caller can truncate. Corruption followed by
// another valid record fails with ErrWALCorrupt.
func replay(file *os.File, apply func(record)) (lastGood int64, err error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := info.Size()
	lastGood = walHeaderSize
	if _, err := file.Seek(walHeaderSize, io.SeekStart); err != nil {
		return 0, err
	}
	reader := bufio.NewReader(file)
	offset := int64(walHeaderSize)

	for {
		var hdr [recordHdrSize]byte
		n, readErr := io.ReadFull(reader, hdr[:])
		if readErr == io.EOF {
			break
		}
		if readErr == io.ErrUnexpectedEOF {
			// Partial frame header at end of file.
			return lastGood, nil
		}
		if readErr != nil {
			return lastGood, readErr
		}
		offset += int64(n)

		size := binary.LittleEndian.Uint32(hdr[:4])
		if size == 0 || size > maxRecordSize {
			return recoverOrCorrupt(file, reader, lastGood, offset, fileSize)
		}

		payload := make([]byte, size)
		n, readErr = io.ReadFull(reader, payload)
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			return lastGood, nil
		}
		if readErr != nil {
			return lastGood, readErr
		}
		offset += int64(n)

		if got, want := crc32.ChecksumIEEE(payload), binary.LittleEndian.Uint32(hdr[4:]); got != want {
			return recoverOrCorrupt(file, reader, lastGood, offset, fileSize)
		}
		var rec record
		if err := json.Unmarshal(payload, &rec); err != nil {
			return recoverOrCorrupt(file, reader, lastGood, offset, fileSize)
		}
		apply(rec)
		lastGood = offset
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return lastGood, err
	}
	return lastGood, nil
}

// recoverOrCorrupt decides whether bytes after a failed record are a garbage
// tail (truncate to lastGood) or mid-file corruption (fail).
func recoverOrCorrupt(file *os.File, reader *bufio.Reader, lastGood, failEnd, fileSize int64) (int64, error) {
	if failEnd >= fileSize {
		return lastGood, nil
	}
	// Discard buffered bytes and scan the remainder for any valid record.
	remainder, err := io.ReadAll(reader)
	if err != nil {
		return lastGood, err
	}
	if hasValidRecord(remainder) {
		return lastGood, ErrWALCorrupt
	}
	_ = file // file position is repaired by Truncate(lastGood) in Open
	return lastGood, nil
}

func hasValidRecord(data []byte) bool {
	for len(data) >= recordHdrSize {
		size := binary.LittleEndian.Uint32(data[:4])
		if size == 0 || size > maxRecordSize {
			data = data[1:]
			continue
		}
		if int(size)+recordHdrSize > len(data) {
			return false
		}
		payload := data[recordHdrSize : recordHdrSize+int(size)]
		want := binary.LittleEndian.Uint32(data[4:8])
		if crc32.ChecksumIEEE(payload) != want {
			data = data[1:]
			continue
		}
		var rec record
		if json.Unmarshal(payload, &rec) != nil {
			data = data[1:]
			continue
		}
		return true
	}
	return false
}
