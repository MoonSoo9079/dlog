package agent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Decoder turns a Docker container log stream into lines.
// When the container is not a TTY, Docker frames the stream with an 8-byte header:
// byte 0 is the stream (1 stdout, 2 stderr) and bytes 4-7 are the big-endian payload size.
type Decoder struct {
	r       io.Reader
	tty     bool
	bufs    [2][]byte
	lines   []decoded
	eof     bool
	maxLine int
}

type decoded struct {
	stream string
	line   string
}

func NewDecoder(r io.Reader, tty bool) *Decoder {
	return &Decoder{r: r, tty: tty, maxLine: 256 * 1024}
}

func (d *Decoder) Next() (stream, line string, err error) {
	for {
		if len(d.lines) > 0 {
			ln := d.lines[0]
			d.lines = d.lines[1:]
			return ln.stream, ln.line, nil
		}
		if d.eof {
			return "", "", io.EOF
		}
		err := d.readChunk()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			d.eof = true
			d.flush()
			continue
		}
		if err != nil {
			return "", "", err
		}
	}
}

func (d *Decoder) readChunk() error {
	if d.tty {
		tmp := make([]byte, 32*1024)
		n, err := d.r.Read(tmp)
		if n > 0 {
			d.bufs[0] = append(d.bufs[0], tmp[:n]...)
			d.consume(0, "stdout")
		}
		return err
	}
	var hdr [8]byte
	if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
		return err
	}
	size := int(binary.BigEndian.Uint32(hdr[4:8]))
	if size > 1<<20 {
		return fmt.Errorf("docker log frame too large: %d", size)
	}
	payload := make([]byte, size)
	if size > 0 {
		if _, err := io.ReadFull(d.r, payload); err != nil {
			return err
		}
	}
	stream, idx, ok := streamIndex(hdr[0])
	if !ok {
		return nil
	}
	d.bufs[idx] = append(d.bufs[idx], payload...)
	d.consume(idx, stream)
	return nil
}

func streamIndex(b byte) (string, int, bool) {
	switch b {
	case 1:
		return "stdout", 0, true
	case 2:
		return "stderr", 1, true
	default:
		return "", 0, false
	}
}

func (d *Decoder) consume(idx int, stream string) {
	b := d.bufs[idx]
	for {
		nl := bytes.IndexByte(b, '\n')
		if nl >= 0 && nl <= d.maxLine {
			d.lines = append(d.lines, decoded{stream, trimCR(b[:nl])})
			b = b[nl+1:]
			continue
		}
		if len(b) > d.maxLine {
			d.lines = append(d.lines, decoded{stream, trimCR(b[:d.maxLine])})
			b = b[d.maxLine:]
			continue
		}
		break
	}
	d.bufs[idx] = b
}

func (d *Decoder) flush() {
	if len(d.bufs[0]) > 0 {
		d.lines = append(d.lines, decoded{"stdout", trimCR(d.bufs[0])})
		d.bufs[0] = nil
	}
	if len(d.bufs[1]) > 0 {
		d.lines = append(d.lines, decoded{"stderr", trimCR(d.bufs[1])})
		d.bufs[1] = nil
	}
}

func trimCR(b []byte) string {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return string(b)
}

// SplitTimestamp separates the RFC3339Nano prefix Docker adds when timestamps=1.
func SplitTimestamp(line string) (time.Time, string, bool) {
	i := strings.IndexByte(line, ' ')
	if i <= 0 {
		return time.Time{}, line, false
	}
	ts, err := time.Parse(time.RFC3339Nano, line[:i])
	if err != nil {
		return time.Time{}, line, false
	}
	return ts, line[i+1:], true
}
