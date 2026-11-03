// Package emitter e a logica propria do modulo de exemplo: simula o log de autenticacao de um
// servico e produz eventos genericos. Um modulo de verdade troca este pacote pelo seu; o protocolo
// fica em internal/psy.
package emitter

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/psychoscope-io/mod-example/internal/psy"
	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Tipos de evento que o modulo emite. O operador escolhe quais o Core vigia (detection.generic_burst.types).
const (
	TypeLoginFail = "login.falha"
	TypeLoginOK   = "login.ok"
)

// Limites do EventoGenerico no contrato (ipc.proto). O Core descarta o evento que os violar; conferi-los
// aqui evita enviar o que seria rejeitado.
const (
	maxAttrs     = 32
	maxNameLen   = 64
	maxValueSize = 256
)

// printable informa se s e UTF-8 valido e sem caracteres de controle. O Core recusa o contrario: o valor
// chega ao contexto dos alertas, que a CLI e a TUI mostram.
func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Validate confere um evento contra os limites que o Core aplica: tipo e chaves em [a-z0-9._-] (ate 64
// caracteres), ate 32 atributos e valores de ate 256 bytes, UTF-8 valido e sem caracteres de controle.
func Validate(g *psychoscopev1.EventoGenerico) error {
	if !nameRe.MatchString(g.GetTipo()) {
		return fmt.Errorf("tipo %q fora do formato do contrato", g.GetTipo())
	}
	if len(g.GetAtributos()) > maxAttrs {
		return fmt.Errorf("%d atributos (maximo %d)", len(g.GetAtributos()), maxAttrs)
	}
	for k, v := range g.GetAtributos() {
		if !nameRe.MatchString(k) {
			return fmt.Errorf("chave de atributo %q fora do formato do contrato", k)
		}
		if len(v) > maxValueSize {
			return fmt.Errorf("valor de %q com %d bytes (maximo %d)", k, len(v), maxValueSize)
		}
		if !printable(v) {
			return fmt.Errorf("valor de %q com caractere de controle ou UTF-8 invalido", k)
		}
	}
	return nil
}

// Config controla a simulacao.
type Config struct {
	Rate       float64       // eventos login.ok por segundo, o ruido normal (0 = nenhum)
	BurstEvery time.Duration // intervalo entre rajadas de login.falha (0 = sem rajadas)
	BurstSize  int           // eventos login.falha por rajada
	BurstUser  string        // usuario que sofre a rajada
}

// Emitter implementa psy.Behavior.
type Emitter struct {
	cfg    Config
	paused atomic.Bool
}

// New cria o emissor.
func New(cfg Config) *Emitter { return &Emitter{cfg: cfg} }

var _ psy.Behavior = (*Emitter)(nil)

// SetPaused liga e desliga a emissao (comandos PAUSAR e RETOMAR).
func (e *Emitter) SetPaused(paused bool) { e.paused.Store(paused) }

var users = []string{"ana", "bia", "caio", "dora"}

// Emit produz o ruido normal e as rajadas ate ctx terminar ou o envio falhar (sessao caiu).
func (e *Emitter) Emit(ctx context.Context, sink psy.Sink) {
	var noise, burst <-chan time.Time
	if e.cfg.Rate > 0 {
		t := time.NewTicker(time.Duration(float64(time.Second) / e.cfg.Rate))
		defer t.Stop()
		noise = t.C
	}
	if e.cfg.BurstEvery > 0 {
		t := time.NewTicker(e.cfg.BurstEvery)
		defer t.Stop()
		burst = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-noise:
			user := users[rand.IntN(len(users))] //nolint:gosec // sorteio de simulacao, nao e segredo
			if !e.send(sink, TypeLoginOK, user, "198.51.100.10") {
				return
			}
		case <-burst:
			for i := 0; i < e.cfg.BurstSize; i++ {
				if !e.send(sink, TypeLoginFail, e.cfg.BurstUser, "192.0.2.77") {
					return
				}
			}
		}
	}
}

// send monta e envia um evento; devolve false quando o envio falha (a sessao acabou). Em pausa,
// nao envia nada e segue.
func (e *Emitter) send(sink psy.Sink, tipo, user, origin string) bool {
	if e.paused.Load() {
		return true
	}
	g := &psychoscopev1.EventoGenerico{
		Tipo: tipo, Timestamp: timestamppb.Now(),
		Atributos: map[string]string{"usuario": user, "origem-ip": origin},
	}
	if err := Validate(g); err != nil {
		return true // evento nosso invalido: descarta e segue (nao deveria acontecer)
	}
	return sink.Send(g) == nil
}
