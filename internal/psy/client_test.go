package psy

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
)

// fakeCore e um Core minimo: aceita conexoes num socket Unix de verdade e deixa o teste conduzir a conversa.
type fakeCore struct {
	t      *testing.T
	ln     net.Listener
	socket string
	conns  chan net.Conn
}

func newFakeCore(t *testing.T) *fakeCore {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "core.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCore{t: t, ln: ln, socket: socket, conns: make(chan net.Conn, 8)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.conns <- c
		}
	}()
	return f
}

// accept espera uma conexao e le o Hello.
func (f *fakeCore) accept() (net.Conn, *psychoscopev1.HelloModulo) {
	f.t.Helper()
	select {
	case c := <-f.conns:
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		m, err := ReadMessage(c)
		if err != nil || m.GetHello() == nil {
			f.t.Fatalf("esperava um Hello: %v %v", m, err)
		}
		return c, m.GetHello()
	case <-time.After(5 * time.Second):
		f.t.Fatal("o modulo nao conectou")
		return nil, nil
	}
}

func reply(t *testing.T, c net.Conn, accepted bool, errMsg string) {
	t.Helper()
	err := WriteMessage(c, &psychoscopev1.MensagemIPC{Payload: &psychoscopev1.MensagemIPC_HelloResposta{
		HelloResposta: &psychoscopev1.HelloResposta{Aceito: accepted, MensagemErro: errMsg, VersaoCore: "teste", IntervaloStatusSeg: 1}}})
	if err != nil {
		t.Fatal(err)
	}
}

// next le a proxima mensagem de um tipo (ignora as outras, como status no meio de uma espera).
func next[T any](t *testing.T, c net.Conn, pick func(*psychoscopev1.MensagemIPC) *T) *T {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		m, err := ReadMessage(c)
		if err != nil {
			t.Fatalf("lendo do modulo: %v", err)
		}
		if v := pick(m); v != nil {
			return v
		}
	}
}

type fakeBehavior struct {
	mu      sync.Mutex
	paused  []bool
	started atomic.Int32
	emit    func(ctx context.Context, s Sink)
}

func (b *fakeBehavior) SetPaused(p bool) { b.mu.Lock(); b.paused = append(b.paused, p); b.mu.Unlock() }
func (b *fakeBehavior) Emit(ctx context.Context, s Sink) {
	b.started.Add(1)
	if b.emit != nil {
		b.emit(ctx, s)
		return
	}
	<-ctx.Done()
}

func startClient(t *testing.T, f *fakeCore, b Behavior) (done chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := New(Config{Socket: f.socket, ID: "mod-teste", Version: "1.2.3", MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	done = make(chan error, 1)
	go func() { done <- c.Run(ctx, b) }()
	return done
}

func TestClientHandshakeEventsStatusAndCommands(t *testing.T) {
	core := newFakeCore(t)
	b := &fakeBehavior{emit: func(ctx context.Context, s Sink) {
		_ = s.Send(&psychoscopev1.EventoGenerico{Tipo: "login.falha", Atributos: map[string]string{"usuario": "ana"}})
		<-ctx.Done()
	}}
	done := startClient(t, core, b)

	conn, hello := core.accept()
	defer conn.Close()
	if hello.IdModulo != "mod-teste" || hello.VersaoProtocolo != ProtocolVersion || hello.VersaoModulo != "1.2.3" || hello.Pid == 0 {
		t.Fatalf("Hello: %v", hello)
	}
	reply(t, conn, true, "")

	ev := next(t, conn, func(m *psychoscopev1.MensagemIPC) *psychoscopev1.EventoTelemetria { return m.GetTelemetria() })
	if ev.GetIdModulo() != "mod-teste" || ev.GetGenerico().GetTipo() != "login.falha" || ev.GetGenerico().GetAtributos()["usuario"] != "ana" {
		t.Fatalf("evento: %v", ev)
	}
	st := next(t, conn, func(m *psychoscopev1.MensagemIPC) *psychoscopev1.StatusModulo { return m.GetStatus() })
	if st.GetIdModulo() != "mod-teste" || st.GetEventosProcessados() != 1 || st.GetPacotesCapturados() != 0 {
		t.Fatalf("status: %v", st)
	}
	if _, ok := st.GetContadores()["perda.ipc"]; !ok {
		t.Errorf("o status deve trazer perda.ipc: %v", st.GetContadores())
	}

	send := func(id string, acao psychoscopev1.ComandoControle_TipoAcao) *psychoscopev1.RespostaComando {
		err := WriteMessage(conn, &psychoscopev1.MensagemIPC{Payload: &psychoscopev1.MensagemIPC_Comando{
			Comando: &psychoscopev1.ComandoControle{IdComando: id, Acao: acao}}})
		if err != nil {
			t.Fatal(err)
		}
		return next(t, conn, func(m *psychoscopev1.MensagemIPC) *psychoscopev1.RespostaComando { return m.GetResposta() })
	}
	if r := send("c1", psychoscopev1.ComandoControle_PAUSAR); !r.GetSucesso() || r.GetIdComandoReferencia() != "c1" {
		t.Errorf("PAUSAR: %v", r)
	}
	if r := send("c2", psychoscopev1.ComandoControle_RETOMAR); !r.GetSucesso() {
		t.Errorf("RETOMAR: %v", r)
	}
	if r := send("c3", psychoscopev1.ComandoControle_RECONFIGURAR); r.GetSucesso() || r.GetMensagemErro() == "" {
		t.Errorf("RECONFIGURAR deve receber NACK com motivo: %v", r)
	}
	if r := send("c4", psychoscopev1.ComandoControle_ACAO_DESCONHECIDA); r.GetSucesso() || !strings.Contains(r.GetMensagemErro(), "desconhecida") {
		t.Errorf("acao desconhecida deve receber NACK: %v", r)
	}
	b.mu.Lock()
	if len(b.paused) != 2 || !b.paused[0] || b.paused[1] {
		t.Errorf("SetPaused: %v", b.paused)
	}
	b.mu.Unlock()

	if r := send("c5", psychoscopev1.ComandoControle_ENCERRAR); !r.GetSucesso() {
		t.Errorf("ENCERRAR: %v", r)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run depois do ENCERRAR: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o modulo deveria sair depois do ENCERRAR")
	}
	select {
	case c := <-core.conns:
		t.Errorf("nao deveria reconectar depois do ENCERRAR: %v", c)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestClientReconnectsAfterTheCoreDropsTheConnection(t *testing.T) {
	core := newFakeCore(t)
	b := &fakeBehavior{}
	startClient(t, core, b)

	conn, _ := core.accept()
	reply(t, conn, true, "")
	conn.Close() // o Core reiniciou
	conn2, hello := core.accept()
	defer conn2.Close()
	reply(t, conn2, true, "")
	if hello.IdModulo != "mod-teste" {
		t.Errorf("novo Hello: %v", hello)
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.started.Load() < 2 {
		t.Errorf("a emissao deveria recomecar na nova sessao: %d", b.started.Load())
	}
}

func TestClientKeepsRetryingWhenTheCoreRefusesTheHello(t *testing.T) {
	core := newFakeCore(t)
	b := &fakeBehavior{}
	startClient(t, core, b)

	conn, _ := core.accept()
	reply(t, conn, false, "nao autorizado")
	conn.Close()
	conn2, _ := core.accept() // tentou de novo
	defer conn2.Close()
	if b.started.Load() != 0 {
		t.Errorf("sem Hello aceito o modulo nao emite: %d", b.started.Load())
	}
}

func TestClientStopsWhenTheContextEnds(t *testing.T) {
	core := newFakeCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	c := New(Config{Socket: core.socket, ID: "m"})
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, &fakeBehavior{}) }()
	conn, _ := core.accept()
	defer conn.Close()
	reply(t, conn, true, "")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run deveria voltar quando o contexto termina")
	}
}

func TestClientWithoutACoreWaitsAndKeepsTrying(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "nao-existe.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c := New(Config{Socket: socket, ID: "m", MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	if err := c.Run(ctx, &fakeBehavior{}); err != nil {
		t.Fatalf("Run sem Core deve esperar o contexto, nao falhar: %v", err)
	}
}
