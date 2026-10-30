package psy

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"time"

	psychoscopev1 "github.com/psychoscope-io/psychoscope-proto/gen/go/psychoscope/v1"
)

func pid() int { return os.Getpid() }

// statusLoop envia o StatusModulo a cada interval ate ctx terminar. Os contadores seguem o contrato
// do ipc.proto: perda.ipc conta os eventos que nao chegaram ao Core, e pacotes_descartados e a soma
// das perdas do modulo. Este modulo nao captura pacotes, entao pacotes_capturados fica em zero (a
// tabela de modulos mostra "-" nessa coluna).
func (c *Client) statusLoop(ctx context.Context, w *conn, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			lost := c.lost.Load()
			m := c.message()
			m.Payload = &psychoscopev1.MensagemIPC_Status{Status: &psychoscopev1.StatusModulo{
				IdModulo: c.cfg.ID, EventosProcessados: c.sent.Load(), PacotesDescartados: lost,
				UsoMemoriaMb: float64(mem.Alloc) / (1 << 20),
				Contadores:   map[string]uint64{"perda.ipc": lost},
			}}
			if err := w.write(m); err != nil {
				return // a leitura percebe a queda e encerra a sessao
			}
		}
	}
}

// commandLoop le as mensagens do Core ate a conexao cair (devolve o erro) ou ate o ENCERRAR
// (devolve errStop, depois de confirmar o comando).
func (c *Client) commandLoop(nc net.Conn, w *conn, b Behavior) error {
	for {
		m, err := ReadMessage(nc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("o Core encerrou a conexao")
			}
			return err
		}
		cmd := m.GetComando()
		if cmd == nil {
			continue // o Core so envia comandos depois do Hello; o resto e ignorado
		}
		ok, msg, stop := c.apply(cmd, b)
		r := c.message()
		r.Payload = &psychoscopev1.MensagemIPC_Resposta{Resposta: &psychoscopev1.RespostaComando{
			IdComandoReferencia: cmd.GetIdComando(), Sucesso: ok, MensagemErro: msg}}
		if err := w.write(r); err != nil {
			return err
		}
		if stop {
			return errStop
		}
	}
}

// apply executa um comando do Core. Um comando que o modulo nao suporta recebe NACK com o motivo,
// nunca silencio: o Core espera a resposta.
func (c *Client) apply(cmd *psychoscopev1.ComandoControle, b Behavior) (ok bool, msg string, stop bool) {
	switch cmd.GetAcao() {
	case psychoscopev1.ComandoControle_PAUSAR:
		b.SetPaused(true)
		return true, "", false
	case psychoscopev1.ComandoControle_RETOMAR:
		b.SetPaused(false)
		return true, "", false
	case psychoscopev1.ComandoControle_ENCERRAR:
		return true, "", true
	case psychoscopev1.ComandoControle_RECONFIGURAR:
		return false, "este modulo nao suporta reconfiguracao em execucao", false
	}
	return false, "acao desconhecida: " + cmd.GetAcao().String(), false
}
