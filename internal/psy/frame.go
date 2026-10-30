// Package psy e o cliente do protocolo IPC do Psychoscope: framing, handshake, status periodico e
// comandos do Core. E a parte que um modulo copia; a logica propria do modulo fica fora daqui
// (veja internal/emitter) e entra pela interface Behavior.
package psy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
	"google.golang.org/protobuf/proto"
)

const (
	headerSize = 4
	// MaxFrameSize e o maior payload aceito por frame (1 MiB), igual ao do Core.
	MaxFrameSize uint32 = 1 << 20
)

var (
	// ErrEmptyFrame indica um frame com tamanho declarado zero (violacao de protocolo).
	ErrEmptyFrame = errors.New("psy: frame vazio (tamanho 0)")
	// ErrFrameTooLarge indica um frame acima do limite (violacao de protocolo).
	ErrFrameTooLarge = errors.New("psy: frame excede o tamanho maximo")
)

// ReadFrame le um frame de r e devolve o payload. Formato: 4 bytes com o tamanho N em big-endian,
// depois N bytes, com 1 <= N <= MaxFrameSize. O tamanho e conferido ANTES de alocar o payload: um
// peer hostil nao consegue forcar uma alocacao grande. io.EOF significa fim limpo na fronteira de
// um frame; io.ErrUnexpectedEOF, fim no meio dele.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	switch {
	case n == 0:
		return nil, ErrEmptyFrame
	case n > MaxFrameSize:
		return nil, fmt.Errorf("%w: declarado=%d max=%d", ErrFrameTooLarge, n, MaxFrameSize)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF // n > 0: EOF aqui e sempre truncamento
		}
		return nil, err
	}
	return payload, nil
}

// WriteFrame escreve header e payload numa unica chamada Write. Quem escreve de varias goroutines
// na mesma conexao precisa serializar as chamadas.
func WriteFrame(w io.Writer, payload []byte) error {
	switch {
	case len(payload) == 0:
		return ErrEmptyFrame
	case uint64(len(payload)) > uint64(MaxFrameSize):
		return fmt.Errorf("%w: tamanho=%d max=%d", ErrFrameTooLarge, len(payload), MaxFrameSize)
	}
	buf := make([]byte, headerSize+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload))) //nolint:gosec // limitado a MaxFrameSize acima
	copy(buf[headerSize:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadMessage le um frame e o decodifica como MensagemIPC.
func ReadMessage(r io.Reader) (*psychoscopev1.MensagemIPC, error) {
	payload, err := ReadFrame(r)
	if err != nil {
		return nil, err
	}
	m := &psychoscopev1.MensagemIPC{}
	if err := proto.Unmarshal(payload, m); err != nil {
		return nil, fmt.Errorf("psy: mensagem invalida: %w", err)
	}
	return m, nil
}

// WriteMessage serializa m e a escreve como um frame.
func WriteMessage(w io.Writer, m *psychoscopev1.MensagemIPC) error {
	payload, err := proto.Marshal(m)
	if err != nil {
		return fmt.Errorf("psy: serializando mensagem: %w", err)
	}
	return WriteFrame(w, payload)
}
