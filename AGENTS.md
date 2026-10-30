# AGENTS.md — mod-example

Módulo de exemplo do Psychoscope, em Go: um programa pequeno que se conecta ao Core, envia eventos genéricos e responde aos comandos. É o modelo para quem escreve um módulo (veja `docs/GUIA.md`). Usa só o `psychoscope-proto` publicado; **não importa nada do Core**, e é isso que o torna um exemplo válido.

## Estrutura

- `cmd/mod-example/main.go`: flags, sinais e a montagem (`psy.Client` + `emitter.Emitter`). Único ponto de composição.
- `internal/psy`: o protocolo IPC, a parte que um módulo copia. `frame.go` (framing de 4 bytes big-endian + `MensagemIPC`, com o tamanho conferido antes de alocar), `client.go` (conexão, Hello, reconexão com backoff, `Behavior` e `Sink`) e `session.go` (status periódico e comandos `PAUSAR`, `RETOMAR`, `ENCERRAR`, com NACK para o resto).
- `internal/emitter`: a lógica própria do módulo (simula um log de autenticação) e `Validate`, que confere o `EventoGenerico` contra os limites do contrato. Um módulo de verdade troca este pacote.
- `testdata/framing_vectors.json`: cópia dos golden vectors do `psychoscope-proto` (a fonte é o proto).
- `scripts/e2e.sh`: o teste ponta a ponta contra um Core de verdade (módulo externo, alerta `generic.burst`, `PAUSAR`/`RETOMAR`/NACK, TUI num pty, reconexão após reiniciar o Core, `ENCERRAR`). `tag.sh` e `check-commits.sh` são cópias compartilhadas.

## Comandos

```bash
make hooks   # uma vez por clone: ativa commit-msg, pre-commit e pre-push
make check   # o que o CI roda: gofmt, vet, golangci-lint, shellcheck, actionlint, go test -race e o e2e
make build   # dist/mod-example (estático, sem CGO)
make e2e     # scripts/e2e.sh; o Core vem de ../psychoscope-core, PSYCHOSCOPE_CORE ou PSYCHOSCOPE_BIN_DIR
make tag     # scripts/tag.sh: tag anotada da próxima versão (local; o push é seu)
```

Ferramentas (fora do `go.mod`): `golangci-lint` v2, `govulncheck`, `actionlint` e `shellcheck`. O proto vem por `go.mod` (`psychoscope-proto` na tag publicada).

## Regras e skills

Regras (`.claude/rules/`): as oito compartilhadas (`fluxo-de-trabalho`, `principios-de-design`, `organizacao-do-codigo`, `testes-e-verificacao`, `git-e-publicacao`, `seguranca`, `contrato-proto`, `estilo-e-idioma`). Skills (`.claude/skills/`): `refatorar-sem-quebrar`, `revisar-pr-dependabot`, `publicar-versao`. Tudo isso é cópia: a fonte está em `psychoscope-io/.github` (`scripts/sync-agent-rules.sh`).

## Armadilhas conhecidas

- **Não importe o Core.** O exemplo existe para provar que um módulo se escreve só com o contrato. O framing é reimplementado em `internal/psy` de propósito (o `ipc` do Core é interno).
- Os limites do `EventoGenerico` estão repetidos em `emitter.Validate`; se o contrato mudar, o teste `TestValidateMatchesTheContractLimits` e o e2e acusam.
- ENCERRAR confirmado e saída com código 0: com `Restart=on-failure` o systemd não reinicia o módulo. Sair com erro faria o systemd reiniciá-lo.
- O módulo é **externo**: o systemd o inicia e o Core só o aceita se o UID do kernel for o do `user` declarado em `psychoscope.json`. No e2e, o `user` é o do próprio usuário que roda o teste.
