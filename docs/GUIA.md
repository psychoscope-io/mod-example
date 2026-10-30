# Como criar um módulo

Um módulo do Psychoscope é um **processo separado** que se conecta ao Core por um socket Unix, diz quem é, envia telemetria e obedece a poucos comandos. O Core roda sem privilégios, e cada módulo recebe só o que precisa. Este guia usa o `mod-example` como modelo; o código que ele mostra é o que você copia.

## 1. A conversa com o Core

Tudo trafega em frames: **4 bytes com o tamanho `N` em big-endian, seguidos de `N` bytes de `MensagemIPC`** (protobuf), com `1 <= N <= 1 MiB`. Quem lê confere `N` **antes** de alocar. `internal/psy/frame.go` faz isso e é conferido com os golden vectors do proto.

```
módulo                                   Core
  | -- HelloModulo(id, versao_protocolo) -->|   autentica pelo UID do kernel (SO_PEERCRED)
  |<-- HelloResposta(aceito, intervalo) ----|   recusa com mensagem_erro se algo não bate
  | -- StatusModulo a cada intervalo ------>|
  | -- EventoTelemetria(...) -------------->|   quantos quiser, respeitando os limites
  |<-- ComandoControle(PAUSAR/...) ---------|
  | -- RespostaComando(sucesso/erro) ------>|   todo comando recebe resposta
```

- **Hello.** `id_modulo` é o `id` do `psychoscope.json`; `versao_protocolo` é `1`; `pid` tem de ser o seu (o Core confere). O Core só aceita se o UID da conexão for o do `user` declarado para o módulo.
- **Status.** Use o `intervalo_status_seg` da resposta. Os contadores seguem o `ipc.proto`: `perda.*` são perdas (`pacotes_descartados` é a soma delas), e `pacotes_capturados` só faz sentido para quem captura pacotes (a tabela de módulos mostra `-` para os demais).
- **Comandos.** Responda a **todos**: `PAUSAR` e `RETOMAR` (pare e volte a emitir), `ENCERRAR` (confirme e saia com código 0, para o systemd não reiniciar) e `RECONFIGURAR`, que, se você não suporta, recebe um NACK com o motivo. Nunca fique em silêncio.
- **Reconexão.** O Core pode reiniciar. Reconecte com backoff e refaça o Hello; o `psy.Client` já faz isso (de 1 s a 30 s).

## 2. Escrever o seu módulo

1. Copie `internal/psy` para o seu projeto (ou use como referência). É a parte que não muda de um módulo para outro.
2. Implemente `psy.Behavior`: `Emit(ctx, sink)` produz eventos até o contexto terminar, e `SetPaused` reflete `PAUSAR` e `RETOMAR`. O `emitter` do exemplo é o modelo.
3. **Valide antes de enviar.** O `EventoGenerico` tem limites no contrato: tipo e chaves em `[a-z0-9._-]` (até 64 caracteres), até 32 atributos e valores de até 256 bytes; o `timestamp` pode estar até 5 minutos à frente da chegada. O Core rejeita o que violar (e conta). `emitter.Validate` confere tudo isso.
4. Monte em `main.go`: `psy.New(...).Run(ctx, comportamento)`.
5. Dependa do contrato por tag: `go get github.com/psychoscope-io/psychoscope-proto@vX.Y.Z`.

Eventos de rede têm a sua própria variante (`EventoFluxoRede`); todo o resto usa o `EventoGenerico`. Um módulo com semântica estável deve propor uma variante tipada no proto, porque atributos em texto não são verificados pelo contrato.

## 3. Registrar o módulo no Core

Siga o [modelo de privilégios](https://github.com/psychoscope-io/psychoscope-core/blob/main/docs/MODULOS.md) do Core:

1. Crie um usuário de sistema para o módulo e use o molde `psychoscope-module.service.example` (sem capabilities, `NoNewPrivileges=yes`, só o socket do Core).
2. Declare-o em `/etc/psychoscope/psychoscope.json`:

   ```json
   { "id": "mod-example", "external": true, "user": "psychoscope-example" }
   ```
3. Escolha o que o Core vigia: a regra `generic.burst` avisa quando um módulo manda N eventos do mesmo tipo na janela. Ela é opt-in:

   ```json
   "detection": { "generic_burst": { "types": ["login.falha"], "group_by": ["usuario"], "min_events": 10, "window": "60s" } }
   ```

   `group_by` separa os grupos por atributo (aqui, uma conta não soma com outra) e `ignore_groups` isenta valores conhecidos. Detalhes na [referência das regras](https://github.com/psychoscope-io/psychoscope-core/blob/main/docs/REGRAS.md#genericburst-rajada-de-eventos-genéricos).

Uma regra própria para o seu módulo vive no Core (`internal/detection/rules`) e precisa de testes, documentação e falsos positivos conhecidos; abra uma issue no `psychoscope-core` descrevendo o problema que ela resolve.

## 4. Testar

- **Framing:** `internal/psy/frame_test.go` roda os golden vectors do proto e confere que um frame grande demais é rejeitado sem ler o payload.
- **Protocolo sem o Core:** `internal/psy/client_test.go` sobe um Core falso num socket Unix de verdade e confere o Hello, o status, os eventos, cada comando, a reconexão e a recusa.
- **Ponta a ponta:** `scripts/e2e.sh` sobe um Core de verdade e confere o alerta, a CLI, a TUI, os comandos, a reconexão e o `ENCERRAR`.

## 5. Boas práticas

- **Menor privilégio.** Nenhuma capability por padrão; se precisar de alguma, documente e entregue como ambiente (nunca `setcap`).
- **Entrada é hostil.** Valide o que vier de fora antes de montar o evento, e não coloque segredos nem dados pessoais nos atributos.
- **Perda é informação.** Se descartar eventos (fila cheia, sessão fora do ar), conte em `perda.*`. O `psychoscope-cli doctor` avisa o operador.
- **Compatibilidade.** Mudanças no contrato são aditivas; um módulo compilado contra uma versão anterior continua funcionando. Veja "Compatibilidade" no README do `psychoscope-proto`.
