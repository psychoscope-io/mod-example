package psy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ProtocolVersion e a versao do protocolo IPC que este cliente fala (HelloModulo.versao_protocolo).
// So muda numa mudanca incompativel do contrato; veja "Compatibilidade" no README do psychoscope-proto.
const ProtocolVersion = 1

const (
	helloTimeout   = 10 * time.Second
	minBackoff     = time.Second
	maxBackoff     = 30 * time.Second
	stableSession  = 30 * time.Second // sessao que durou isto zera o backoff
	defaultStatusS = 5
)

// errStop marca o encerramento pedido pelo Core (comando ENCERRAR): o cliente sai sem reconectar.
var errStop = errors.New("psy: encerramento pedido pelo Core")

// Sink recebe os eventos que o modulo produz e os envia ao Core.
type Sink interface {
	// Send envia um evento generico. Devolve erro se a sessao nao esta ativa; o evento se perde e e
	// contado como perda (contador perda.ipc no status).
	Send(g *psychoscopev1.EventoGenerico) error
}

// Behavior e a parte do modulo que muda de um modulo para outro; o cliente cuida do resto.
type Behavior interface {
	// Emit produz eventos em sink e so volta quando ctx terminar (sessao encerrada ou modulo parando).
	Emit(ctx context.Context, sink Sink)
	// SetPaused reflete os comandos PAUSAR (true) e RETOMAR (false) do Core.
	SetPaused(paused bool)
}

// Config parametriza o cliente.
type Config struct {
	Socket  string // caminho do socket do Core
	ID      string // id do modulo, o mesmo de psychoscope.json
	Version string // versao do modulo, so informativa
	Log     *slog.Logger
	// MinBackoff e MaxBackoff limitam a espera entre tentativas de reconexao (zero = 1 s e 30 s).
	MinBackoff, MaxBackoff time.Duration
}

// Client mantem a conexao do modulo com o Core: conecta, faz o handshake, envia status periodico,
// responde aos comandos e reconecta com backoff se a conexao cair.
type Client struct {
	cfg   Config
	sent  atomic.Uint64 // eventos enviados
	lost  atomic.Uint64 // eventos perdidos por sessao fora do ar ou falha de escrita
	msgID atomic.Uint64
}

// New cria o cliente.
func New(cfg Config) *Client {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Client{cfg: cfg}
}

// Run executa o modulo ate ctx terminar ou o Core pedir ENCERRAR, e devolve nil nos dois casos.
// Cai a conexao ou o Core recusa o Hello: espera (backoff exponencial, de 1 s a 30 s) e tenta de novo.
func (c *Client) Run(ctx context.Context, b Behavior) error {
	lo, hi := c.cfg.MinBackoff, c.cfg.MaxBackoff
	if lo <= 0 {
		lo = minBackoff
	}
	if hi <= 0 {
		hi = maxBackoff
	}
	backoff := lo
	for {
		started := time.Now()
		err := c.session(ctx, b)
		if errors.Is(err, errStop) || ctx.Err() != nil {
			return nil //nolint:nilerr // ENCERRAR e o cancelamento sao saidas normais, nao falhas
		}
		if time.Since(started) >= stableSession {
			backoff = lo
		}
		c.cfg.Log.Warn("sessao com o Core terminou", "erro", err, "nova_tentativa_em", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, hi)
	}
}

// conn serializa as escritas de varias goroutines na mesma conexao.
type conn struct {
	mu sync.Mutex
	c  net.Conn
}

func (w *conn) write(m *psychoscopev1.MensagemIPC) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WriteMessage(w.c, m)
}

func (c *Client) message() *psychoscopev1.MensagemIPC {
	return &psychoscopev1.MensagemIPC{IdMensagem: fmt.Sprint(c.msgID.Add(1)), Timestamp: timestamppb.Now()}
}

// session cobre uma conexao: handshake, goroutines de status e de emissao, e o laco de comandos.
func (c *Client) session(ctx context.Context, b Behavior) error {
	nc, err := net.Dial("unix", c.cfg.Socket)
	if err != nil {
		return fmt.Errorf("conectando em %s: %w", c.cfg.Socket, err)
	}
	defer nc.Close()
	w := &conn{c: nc}
	interval, err := c.handshake(w, nc)
	if err != nil {
		return err
	}
	c.cfg.Log.Info("conectado ao Core", "socket", c.cfg.Socket)

	sctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(sctx, func() { _ = nc.Close() }) // destrava a leitura ao cancelar
	var wg sync.WaitGroup
	defer func() { stop(); cancel(); wg.Wait() }()
	wg.Add(2)
	go func() { defer wg.Done(); c.statusLoop(sctx, w, interval) }()
	go func() { defer wg.Done(); b.Emit(sctx, &sink{c: c, w: w}) }()
	return c.commandLoop(nc, w, b)
}

// handshake envia o Hello e le a resposta; devolve o intervalo de status que o Core pediu.
func (c *Client) handshake(w *conn, nc net.Conn) (time.Duration, error) {
	hello := c.message()
	hello.Payload = &psychoscopev1.MensagemIPC_Hello{Hello: &psychoscopev1.HelloModulo{
		IdModulo: c.cfg.ID, VersaoProtocolo: ProtocolVersion, VersaoModulo: c.cfg.Version, Pid: uint32(pid()), //nolint:gosec // PID nao e negativo
	}}
	if err := w.write(hello); err != nil {
		return 0, fmt.Errorf("enviando Hello: %w", err)
	}
	_ = nc.SetReadDeadline(time.Now().Add(helloTimeout))
	m, err := ReadMessage(nc)
	if err != nil {
		return 0, fmt.Errorf("lendo a resposta ao Hello: %w", err)
	}
	_ = nc.SetReadDeadline(time.Time{})
	r := m.GetHelloResposta()
	switch {
	case r == nil:
		return 0, errors.New("a primeira resposta do Core nao e um HelloResposta")
	case !r.GetAceito():
		return 0, fmt.Errorf("o Core recusou o modulo: %s", r.GetMensagemErro())
	}
	return time.Duration(cmpNonZero(r.GetIntervaloStatusSeg(), defaultStatusS)) * time.Second, nil
}

func cmpNonZero(v uint32, def uint32) uint32 {
	if v == 0 {
		return def
	}
	return v
}

// sink e o Sink de uma sessao.
type sink struct {
	c *Client
	w *conn
}

func (s *sink) Send(g *psychoscopev1.EventoGenerico) error {
	m := s.c.message()
	m.Payload = &psychoscopev1.MensagemIPC_Telemetria{Telemetria: &psychoscopev1.EventoTelemetria{
		IdModulo: s.c.cfg.ID, Dado: &psychoscopev1.EventoTelemetria_Generico{Generico: g}}}
	if err := s.w.write(m); err != nil {
		s.c.lost.Add(1)
		return err
	}
	s.c.sent.Add(1)
	return nil
}
