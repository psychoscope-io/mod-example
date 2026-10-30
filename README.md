# mod-example

Módulo de exemplo do [Psychoscope](https://github.com/psychoscope-io): um programa pequeno em Go que se conecta ao Core, envia eventos genéricos e responde aos comandos. Serve de modelo para quem vai escrever um módulo. Ele usa **só o contrato** (`psychoscope-proto`) e não importa nada do Core.

O módulo simula o log de autenticação de um serviço: manda eventos `login.ok` como ruído normal e, de tempos em tempos, uma rajada de `login.falha` de um usuário. Com a regra `generic.burst` do Core vigiando o tipo `login.falha`, a rajada vira um alerta que aparece na CLI e na TUI.

## Rodar

Pré-requisitos: Go e um `psychoscope-core` ao lado (ou `PSYCHOSCOPE_CORE`/`PSYCHOSCOPE_BIN_DIR`, veja `AGENTS.md`).

```bash
make e2e
```

O `scripts/e2e.sh` sobe um Core de verdade sem privilégios, conecta o módulo, espera o alerta `generic.burst`, confere a tabela de módulos, os comandos `PAUSAR`, `RETOMAR` e `ENCERRAR`, a TUI num pseudo-terminal e a reconexão depois de reiniciar o Core.

Para instalar como serviço (systemd, usuário próprio, sem privilégios), siga o [guia](docs/GUIA.md#registrar-o-módulo-no-core): o molde de unit está em `deploy/systemd/psychoscope-module.service.example` no `psychoscope-core`.

## O que olhar

| Arquivo | O que mostra |
|---|---|
| `internal/psy/frame.go` | o framing: 4 bytes big-endian com o tamanho, depois a `MensagemIPC` |
| `internal/psy/client.go`, `session.go` | o Hello, a reconexão com backoff, o status periódico e os comandos |
| `internal/emitter/emitter.go` | a lógica do módulo e a validação dos limites do `EventoGenerico` |
| `docs/GUIA.md` | **como criar o seu módulo**, passo a passo |

## Licença

Apache-2.0, como o resto do Psychoscope.
