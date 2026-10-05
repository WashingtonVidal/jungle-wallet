# Jungle Wallet

Serviço financeiro em Go para carteiras de jogadores e processamento de BET, WIN, LOSS, REFUND e ROLLBACK. HTTP e SQS compartilham o caso de uso transacional, idempotência persistente e ledger auditável.

Fonte dos requisitos: [desafio oficial](https://github.com/junglegaming/backend-challenge-go). Decisões e limites em [ARCHITECTURE.md](ARCHITECTURE.md). Evidências e lacunas em [VALIDATION.md](VALIDATION.md).

## Ambiente local

Go **1.26.0**, Docker Engine com Compose, Linux/WSL2. Portas locais: PostgreSQL 5432, Keycloak 8081, LocalStack 4566 e aplicação 8080. Os testes utilizam exclusivamente `jungle_wallet_test`, separado do banco da aplicação. A variável opcional `JUNGLE_TEST_DATABASE_URL` deve identificar um banco cujo nome termine em `_test`; a suíte limpa apenas tabelas de fixtures nesse banco.

```sh
docker compose up -d postgres keycloak localstack
cp .env.example .env
set -a
. ./.env
set +a
# Banco novo:
go run ./cmd/migrate up
go run ./cmd/wallet
```

Aguarde o discovery do Keycloak em `http://localhost:8081/realms/jungle-wallet/.well-known/openid-configuration`. O realm e os clientes locais são importados no primeiro início; filas FIFO e redrive são provisionados pelo hook de LocalStack.

Para o checkpoint antigo, com as migrations 1–3 já aplicadas e **sem** tabela de histórico, rode **uma vez** `go run ./cmd/migrate baseline=3`, depois `up`. Não use baseline em banco novo. O runner aplica SQL e histórico na mesma transação, com lock exclusivo apenas durante migrations.

`go run ./cmd/migrate down` reverte **uma** versão, em ordem inversa; verifique a versão antes de usar. Reverter as migrations iniciais remove tabelas e dados. Nenhuma migration histórica foi reescrita nesta finalização.

## Docker da aplicação

```sh
docker compose --profile app up --build
```

O perfil `app` compila os dois binários, aplica migrations e inicia a aplicação. Usa rede do host para manter o mesmo issuer OIDC que os tokens emitidos pelo Keycloak local; este perfil é destinado a Docker Engine em Linux/WSL2. O Compose sem esse perfil inicia apenas a infraestrutura.

## Configuração

`DATABASE_URL` e `OIDC_ISSUER_URL` são obrigatórios. Defaults: `HTTP_ADDRESS=:8080`, `INTERNAL_CLIENT_ID=wallet-internal`, `AWS_REGION=us-east-1`, `SQS_ENDPOINT=http://localhost:4566`, filas e URLs de `.env.example`. O cliente SQS usa a cadeia de credenciais do SDK (ambiente, profile ou role). Somente localhost/127.0.0.1 sem credenciais explícitas recebe o fallback local `test/test`. Políticas efetivamente impostas pelo broker continuam pendentes no emulador: ver ARCHITECTURE.md.

## Autenticação e API

Obtenha tokens via client credentials em `POST http://localhost:8081/realms/jungle-wallet/protocol/openid-connect/token`, com `grant_type=client_credentials`, `client_id` e o segredo do cliente local declarado em `deploy/keycloak/realm-export.json`. Use `Authorization: Bearer <token>`. Não registre tokens em logs.

`wallet-internal` pode criar, ler, reconciliar carteiras, consultar ledger e métricas. `provider-a` só pode enviar e consultar suas próprias transações. JWTs são verificados pelo IdP: assinatura, issuer e expiração. A identidade é `client_id`, com fallback para `azp`. O serviço não oferece cadastro/login.

- `POST /wallets`: corpo `{"playerId":"player-1","initialBalance":{"amount":"100.00","currency":"BRL"}}`; resposta 201 com `id`, jogador, saldo e versão 1.
- `GET /wallets/{walletId}`: identidade, jogador, saldo, versão e timestamps.
- `GET /wallets/{walletId}/ledger?limit=50&cursor=...`: `entries` e `nextCursor`. Limite 1–200; ordenação ascendente por timestamp e ID. Cursor opaco vinculado à carteira.
- `POST /wallets/{walletId}/reconciliation`: saldo armazenado, soma do ledger, diferença, consistência e quantidade de lançamentos; não altera a carteira.
- `POST /wagering/transactions`: exige `Idempotency-Key`.
- `GET /wagering/transactions/{transactionId}` e `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}`: status, referências, código de rejeição e resultado persistido.
- `GET /health` e `GET /health/live`: liveness pública.
- `GET /health/ready`: verifica PostgreSQL e ambas as filas SQS.
- `GET /metrics`: texto Prometheus para o cliente interno.

Exemplo de operação:

```json
{
  "providerId": "provider-a",
  "externalTransactionId": "bet-1",
  "playerId": "player-1",
  "walletId": "<id devolvido na abertura>",
  "roundId": "round-1",
  "gameId": "game-1",
  "kind": "BET",
  "money": {"amount": "25.00", "currency": "BRL"}
}
```

Reversões exigem `referenceExternalTransactionId`. `LOSS` exige `0.00`. `OPENING` é interno. WIN pode referenciar uma BET da mesma rodada.

HTTP: 200 operação concluída/replay, 201 abertura, 202 referência pendente, 400 entrada inválida, 401 token ausente/inválido, 403 identidade não autorizada, 404 inexistente/não visível, 409 carteira duplicada ou chave/payload conflitantes, 422 rejeição definitiva com `failureCode`, 503 dependência temporariamente indisponível. Processados retornam saldo original; pendentes/rejeitados podem não ter saldo. Erros simples incluem texto ou `{"error":"..."}`; resultados financeiros incluem `transactionId`, `status`, `idempotentReplay` e saldo/código quando aplicáveis.

## Consistência e mensageria

Money usa centavos em int64/BIGINT, sem ponto flutuante financeiro. BRL, USD e EUR: exatamente duas casas decimais. Entradas sem espaços, expoentes, negativos ou zeros à esquerda; não há arredondamento. Máximo positivo: `92233720368547758.07`. Diferenças internas podem ser negativas.

Uma abertura positiva confirma carteira + OPENING + ledger + dois eventos atomicamente. Saldo zero cria apenas a carteira. Unicidade jogador/moeda impede crédito inicial repetido.

Cada movimento usa lock da linha da carteira, versão e uma transação PostgreSQL. O ledger valida a equação financeira, possui unicidade carteira/transação e triggers contra UPDATE/DELETE. Carteiras diferentes não compartilham lock global.

Idempotência por provedor/ID externo e por provedor/chave, com SHA-256 dos campos de negócio em JSON determinístico. HTTP e SQS usam a mesma representação e excluem a chave e metadados de transporte do hash. Replay devolve o resultado persistido e não gera novo ledger/evento.

A entrada SQS recebe o envelope do desafio (`messageId`, `type=WagerTransactionRequested`, `occurredAt`, `data`), com chave em `data.idempotencyKey`. Inbox identifica consumidor + messageId e confere hash do envelope. O commit inclui inbox, domínio, ledger e outbox. A mensagem só é removida após o commit. Na fila FIFO, use `MessageGroupId=walletId` e `MessageDeduplicationId=messageId`. Deduplicação financeira não depende da janela do broker.

Timeout de visibilidade: 30s. Falhas de consumo recebem backoff exponencial de 1s a 30s; redrive após 5 recebimentos para `wager-transactions-dlq.fifo`. Mensagens inválidas também chegam à DLQ. Rejeições duráveis são terminais e removíveis. Cancelamento libera visibilidade quando possível; se houver falha na remoção/liberação, a visibilidade expira e a inbox protege a reentrega.

Referências ausentes/pendentes são persistidas. Retry a cada 500ms, com lease de 30s e até 6 tentativas; backoff 1, 2, 4, 8 e 16 segundos. Expiração gera REJECTED e evento. Quando a original chega, pendências compatíveis podem ser resolvidas no mesmo commit.

Outbox com claim SQL SKIP LOCKED e lease de 30s; backoff 1, 2, 5, 10 e 30s. Publisher separado envia para `wager-events.fifo`, preserva `eventId` na republicação e marca publicado após o envio. Consumidores de saída devem deduplicar por eventId, pois a entrega é at-least-once. Grupo: aggregateId; deduplicationId: eventId.

## Testes e validação

Pare apenas processos da aplicação antes da suíte, mantendo a infraestrutura: `docker compose stop wallet`, se o perfil estiver ativo. Crie o banco exclusivo de teste, aplique migrations nele e aguarde o IdP. Os testes exigem endpoints locais da configuração acima e aplicam limpeza de dados exclusivamente de teste.

```sh
# Execute createdb uma única vez (não recrie/remova um banco existente):
docker compose exec -T postgres createdb -U jungle jungle_wallet_test
DATABASE_URL=postgres://jungle:jungle@localhost:5432/jungle_wallet_test?sslmode=disable go run ./cmd/migrate up
go test -p 1 ./... -count=1
go test -race -p 1 ./... -count=1
go vet ./...
go build -o /tmp/jungle-wallet ./cmd/wallet
git diff --check
```

A execução serial é necessária porque os testes históricos compartilham o banco. O teste em `cmd/wallet` inicia três processos com pools/memórias separados, usa tokens reais e cruza HTTP/SQS. Não deixe outras instâncias em execução durante a suíte. Para repetir esse cenário: `go test -race ./cmd/wallet -run TestThreeInstancesHTTPAndSQSIntegration -count=1`.

`internal/application` cobre atomicidade da abertura, reenvios, paralelismo, reversões, expiração e rejeições. `internal/infrastructure/postgres` cobre inbox/outbox, claims e constraints. Testes de carga, tracing e partidas dobradas ficaram fora do escopo.

O teste de token expirado utiliza um JWT genuíno assinado pelo Keycloak e avança apenas o relógio do verificador para depois de exp. Não modifica tokens, configurações do realm ou o relógio do sistema. Reconciliação divergente é exercitada com injeção de falha apenas na carteira criada no banco de teste.

## Autorização do broker

Políticas de menor privilégio, ambiente isolado de reprodução e teste positivo/negativo para AWS real estão em [deploy/broker/README.md](deploy/broker/README.md). LocalStack Community ignora restrições; MiniStack 1.5.18/1.5.21 impõe políticas, mas aceita secret incorreto. O teste detecta essas limitações e falha. Nenhum desses resultados fecha a pendência: falta a execução completa em broker que valide credenciais e políticas. Não houve mudanças no caso de uso financeiro.
