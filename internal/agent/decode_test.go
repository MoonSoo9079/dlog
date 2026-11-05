package agent

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"
)

func muxFrame(stream byte, payload string) []byte {
	body := []byte(payload)
	hdr := make([]byte, 8)
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(body)))
	return append(hdr, body...)
}

func TestDecoderFramesAndSplitLines(t *testing.T) {
	var raw bytes.Buffer
	raw.Write(muxFrame(1, "hello "))
	raw.Write(muxFrame(1, "world\n"))
	raw.Write(muxFrame(2, "bad\r\n"))
	raw.Write(muxFrame(0, "ignored\n"))

	dec := NewDecoder(&oneByteReader{r: &raw}, false)
	got := map[string][]string{}
	for {
		stream, line, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[stream] = append(got[stream], line)
	}
	if strings.Join(got["stdout"], "|") != "hello world" {
		t.Fatalf("stdout: %#v", got["stdout"])
	}
	if strings.Join(got["stderr"], "|") != "bad" {
		t.Fatalf("stderr: %#v", got["stderr"])
	}
}

func TestDecoderTTYFlush(t *testing.T) {
	dec := NewDecoder(strings.NewReader("partial"), true)
	stream, line, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if stream != "stdout" || line != "partial" {
		t.Fatalf("got %s %q", stream, line)
	}
	if _, _, err := dec.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, msg, ok := SplitTimestamp("2020-01-02T03:04:05.123456789Z hello")
	if !ok || msg != "hello" {
		t.Fatalf("parse failed: %v %q", ok, msg)
	}
	want := time.Date(2020, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if !ts.Equal(want) {
		t.Fatalf("ts %s", ts)
	}
	if _, _, ok := SplitTimestamp("not a timestamp"); ok {
		t.Fatal("expected no timestamp")
	}
}

type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}
