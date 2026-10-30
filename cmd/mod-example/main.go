// Comando mod-example: modulo de exemplo do Psychoscope. Simula o log de autenticacao de um servico
// e envia os eventos ao Core como eventos genericos. Veja docs/GUIA.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/psychoscope-io/mod-example/internal/emitter"
	"github.com/psychoscope-io/mod-example/internal/psy"
)

// version vem do -ldflags "-X main.version=..." (a versao da tag).
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mod-example:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("mod-example", flag.ContinueOnError)
	socket := fs.String("socket", "/run/psychoscope/core.sock", "socket do Core")
	id := fs.String("id", "mod-example", "id do modulo, o mesmo de psychoscope.json")
	rate := fs.Float64("rate", 1, "eventos login.ok por segundo (0 = nenhum)")
	burstEvery := fs.Duration("burst-every", 2*time.Minute, "intervalo entre rajadas de login.falha (0 = sem rajadas)")
	burstSize := fs.Int("burst-size", 12, "eventos login.falha por rajada")
	burstUser := fs.String("burst-user", "ana", "usuario que sofre a rajada")
	showVersion := fs.Bool("version", false, "mostra a versao e sai")
	verbose := fs.Bool("v", false, "log detalhado")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("mod-example", version)
		return nil
	}
	if *rate < 0 || *burstEvery < 0 || *burstSize < 0 {
		return fmt.Errorf("--rate, --burst-every e --burst-size nao podem ser negativos")
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := psy.New(psy.Config{Socket: *socket, ID: *id, Version: version, Log: log})
	b := emitter.New(emitter.Config{Rate: *rate, BurstEvery: *burstEvery, BurstSize: *burstSize, BurstUser: *burstUser})
	return client.Run(ctx, b)
}
