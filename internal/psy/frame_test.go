package psy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
)

type vector struct {
	Name        string `json:"name"`
	Header      string `json:"header"`
	Payload     string `json:"payload"`
	PayloadFill *struct {
		Byte  string `json:"byte"`
		Count int    `json:"count"`
	} `json:"payload_fill"`
	Expect string `json:"expect"`
}

// Os golden vectors vem do psychoscope-proto (testdata/framing_vectors.json): os mesmos casos que o
// Core e o sensor conferem, entao um modulo que passa neles fala o mesmo framing.
func TestReadFrameGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/framing_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		MaxFrameSize uint32   `json:"max_frame_size"`
		Cases        []vector `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.MaxFrameSize != MaxFrameSize {
		t.Fatalf("max_frame_size do contrato = %d, cliente = %d", file.MaxFrameSize, MaxFrameSize)
	}
	for _, v := range file.Cases {
		hdr, _ := hex.DecodeString(v.Header)
		var payload []byte
		if v.PayloadFill != nil {
			b, _ := hex.DecodeString(v.PayloadFill.Byte)
			payload = bytes.Repeat(b, v.PayloadFill.Count)
		} else {
			payload, _ = hex.DecodeString(v.Payload)
		}
		got, err := ReadFrame(bytes.NewReader(append(hdr, payload...)))
		switch v.Expect {
		case "ok":
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("%s: err=%v", v.Name, err)
			}
		case "eof":
			if !errors.Is(err, io.EOF) {
				t.Errorf("%s: esperava io.EOF, veio %v", v.Name, err)
			}
		case "empty":
			if !errors.Is(err, ErrEmptyFrame) {
				t.Errorf("%s: esperava ErrEmptyFrame, veio %v", v.Name, err)
			}
		case "too_large":
			if !errors.Is(err, ErrFrameTooLarge) {
				t.Errorf("%s: esperava ErrFrameTooLarge, veio %v", v.Name, err)
			}
		case "truncated":
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("%s: esperava io.ErrUnexpectedEOF, veio %v", v.Name, err)
			}
		default:
			t.Errorf("%s: expect desconhecido %q", v.Name, v.Expect)
		}
	}
}

// Um frame que declara 1 MiB + 1 e rejeitado sem ler o payload: o leitor so tem o header.
func TestReadFrameRejectsOversizeBeforeReadingPayload(t *testing.T) {
	r := &countingReader{r: bytes.NewReader([]byte{0x00, 0x10, 0x00, 0x01})}
	if _, err := ReadFrame(r); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("esperava ErrFrameTooLarge, veio %v", err)
	}
	if r.n != headerSize {
		t.Fatalf("leu %d bytes; so o header (%d) deveria ser lido", r.n, headerSize)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestMessageRoundTripAndWriteLimits(t *testing.T) {
	var buf bytes.Buffer
	in := &psychoscopev1.MensagemIPC{IdMensagem: "7", Payload: &psychoscopev1.MensagemIPC_Hello{Hello: &psychoscopev1.HelloModulo{IdModulo: "m"}}}
	if err := WriteMessage(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadMessage(&buf)
	if err != nil || out.GetIdMensagem() != "7" || out.GetHello().GetIdModulo() != "m" {
		t.Fatalf("ida e volta: %v %v", out, err)
	}
	if err := WriteFrame(io.Discard, nil); !errors.Is(err, ErrEmptyFrame) {
		t.Errorf("frame vazio: %v", err)
	}
	if err := WriteFrame(io.Discard, make([]byte, MaxFrameSize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("frame grande: %v", err)
	}
	if _, err := ReadMessage(bytes.NewReader([]byte{0, 0, 0, 3, 0xff, 0xff, 0xff})); err == nil {
		t.Error("payload que nao e protobuf valido deve dar erro")
	}
}
