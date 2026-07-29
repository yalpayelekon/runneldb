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

const maxRecordSize = 64 << 20

type operation struct {
	Key    string `json:"key"`
	Value  []byte `json:"value,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

type record struct {
	Version uint64      `json:"version"`
	Ops     []operation `json:"ops"`
}

func appendRecord(file *os.File, rec record) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var header [8]byte
	binary.LittleEndian.PutUint32(header[:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[4:], crc32.ChecksumIEEE(payload))
	if _, err = file.Write(header[:]); err != nil {
		return err
	}
	if _, err = file.Write(payload); err != nil {
		return err
	}
	return file.Sync()
}

func replay(file *os.File, apply func(record)) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	for {
		var header [8]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			if err == io.EOF {
				break
			}
			if err == io.ErrUnexpectedEOF {
				return fmt.Errorf("runneldb: truncated WAL header: %w", err)
			}
			return err
		}
		size := binary.LittleEndian.Uint32(header[:4])
		if size > maxRecordSize {
			return fmt.Errorf("runneldb: WAL record too large: %d", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return fmt.Errorf("runneldb: truncated WAL record: %w", err)
		}
		if got, want := crc32.ChecksumIEEE(payload), binary.LittleEndian.Uint32(header[4:]); got != want {
			return fmt.Errorf("runneldb: WAL checksum mismatch")
		}
		var rec record
		if err := json.Unmarshal(payload, &rec); err != nil {
			return fmt.Errorf("runneldb: invalid WAL record: %w", err)
		}
		apply(rec)
	}
	_, err := file.Seek(0, io.SeekEnd)
	return err
}
