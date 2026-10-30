package emitter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
)

type recSink struct {
	mu   sync.Mutex
	got  []*psychoscopev1.EventoGenerico
	fail bool
}

func (r *recSink) Send(g *psychoscopev1.EventoGenerico) error {
	if r.fail {
		return errors.New("sessao fora do ar")
	}
	r.mu.Lock()
	r.got = append(r.got, g)
	r.mu.Unlock()
	return nil
}

func (r *recSink) count(tipo string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, g := range r.got {
		if g.GetTipo() == tipo {
			n++
		}
	}
	return n
}

func runFor(t *testing.T, e *Emitter, s *recSink, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	e.Emit(ctx, s)
}

func TestBurstsEmitFailureEventsForTheConfiguredUser(t *testing.T) {
	s := &recSink{}
	runFor(t, New(Config{BurstEvery: 20 * time.Millisecond, BurstSize: 5, BurstUser: "ana"}), s, 110*time.Millisecond)
	n := s.count(TypeLoginFail)
	if n == 0 || n%5 != 0 {
		t.Fatalf("deveria emitir rajadas completas de 5: %d", n)
	}
	for _, g := range s.got {
		if g.GetAtributos()["usuario"] != "ana" || g.GetTimestamp() == nil {
			t.Fatalf("evento: %v", g)
		}
		if err := Validate(g); err != nil {
			t.Fatalf("evento fora do contrato: %v", err)
		}
	}
}

func TestNoiseEmitsLoginOKAndNoBurstWhenDisabled(t *testing.T) {
	s := &recSink{}
	runFor(t, New(Config{Rate: 200}), s, 100*time.Millisecond)
	if s.count(TypeLoginOK) == 0 || s.count(TypeLoginFail) != 0 {
		t.Fatalf("ok=%d falha=%d", s.count(TypeLoginOK), s.count(TypeLoginFail))
	}
}

func TestPausedEmitterSendsNothingAndResumes(t *testing.T) {
	s := &recSink{}
	e := New(Config{Rate: 500})
	e.SetPaused(true)
	runFor(t, e, s, 80*time.Millisecond)
	if n := s.count(TypeLoginOK); n != 0 {
		t.Fatalf("em pausa nao pode enviar: %d", n)
	}
	e.SetPaused(false)
	runFor(t, e, s, 80*time.Millisecond)
	if s.count(TypeLoginOK) == 0 {
		t.Fatal("depois de RETOMAR deve voltar a enviar")
	}
}

func TestEmitStopsWhenTheSinkFails(t *testing.T) {
	s := &recSink{fail: true}
	done := make(chan struct{})
	go func() { New(Config{Rate: 500}).Emit(context.Background(), s); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit deveria voltar quando o envio falha (sessao caiu)")
	}
}

func TestValidateMatchesTheContractLimits(t *testing.T) {
	attrs := func(n int) map[string]string {
		m := map[string]string{}
		for i := 0; i < n; i++ {
			m["k"+strings.Repeat("a", i)] = "v"
		}
		return m
	}
	cases := []struct {
		name string
		g    *psychoscopev1.EventoGenerico
		ok   bool
	}{
		{"valido", &psychoscopev1.EventoGenerico{Tipo: "login.falha", Atributos: map[string]string{"usuario": "ana"}}, true},
		{"tipo vazio", &psychoscopev1.EventoGenerico{}, false},
		{"tipo com maiuscula", &psychoscopev1.EventoGenerico{Tipo: "Login"}, false},
		{"tipo com 65 caracteres", &psychoscopev1.EventoGenerico{Tipo: strings.Repeat("a", 65)}, false},
		{"chave invalida", &psychoscopev1.EventoGenerico{Tipo: "t", Atributos: map[string]string{"Chave": "v"}}, false},
		{"32 atributos", &psychoscopev1.EventoGenerico{Tipo: "t", Atributos: attrs(32)}, true},
		{"33 atributos", &psychoscopev1.EventoGenerico{Tipo: "t", Atributos: attrs(33)}, false},
		{"valor com 256 bytes", &psychoscopev1.EventoGenerico{Tipo: "t", Atributos: map[string]string{"k": strings.Repeat("x", 256)}}, true},
		{"valor com 257 bytes", &psychoscopev1.EventoGenerico{Tipo: "t", Atributos: map[string]string{"k": strings.Repeat("x", 257)}}, false},
	}
	for _, c := range cases {
		if err := Validate(c.g); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, esperado ok=%v", c.name, err, c.ok)
		}
	}
}
