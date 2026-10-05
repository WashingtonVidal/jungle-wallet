# Validação contra o desafio oficial

Referência: https://github.com/junglegaming/backend-challenge-go (README consultado em 04/10/2026). A comparação abaixo distingue código/evidência de requisitos ainda parcialmente demonstrados.

Legenda: [x] implementado e validado; [!] parcial; [ ] ausente; [-] opcional/diferencial.

## Checklist

1. Objetivo
- [x] Go, Uber Fx, HTTP e consumidor SQS com o mesmo caso de uso financeiro.
- [x] Idempotência persistente, integridade do ledger e coordenação por carteira.

2. Autenticação e autorização
- [x] Keycloak real, client credentials e verificação OIDC; nenhuma emissão própria de tokens.
- [x] providerId vinculado à identidade assinada; isolamento em envio, replay e consultas.
- [x] Carteira/ledger/reconciliação/métricas restritos a wallet-internal.
- [!] Credenciais SQS configuráveis pela cadeia AWS; fallback fictício restrito a localhost sem credenciais explícitas.
- [!] Políticas IAM/SQS versionadas e provisionadas em MiniStack por CloudFormation; enforcement completo não comprovado. LocalStack Community ignora denies; MiniStack aceita secret errado. É obrigatório (seção 2); portas de loopback não substituem IAM. Ver evidências abaixo.

3. Falhas e at-least-once
- [x] Duplicidade HTTP/SQS sem movimento adicional, referência fora de ordem, saldo sem lost update.
- [x] Reinício real de uma das três instâncias, reconstrução de pools, persistência de resultados.
- [x] Reentrega real após commit sem remoção da mensagem; publicação aceita com confirmação SQL deliberadamente falhando; retomada por outro publisher.
- [!] Não foram automatizados SIGKILL precisamente em cada ponto crítico nem derrubada temporária de PostgreSQL/SQS durante uma operação. As janelas foram verificadas por abandono de receipt e falha SQL direcionada.

4. Stack e ciclo de vida
- [x] Go 1.26.0 em go.mod/Dockerfile, modules versionados, net/http, pgx, SQS/LocalStack e Compose.
- [x] Fx.Module, Provide, Invoke; servidor/workers iniciados e encerrados por três processos reais.
- [x] HTTP para novas entradas antes de encerrar workers; pool fechado depois dos consumidores.
- [x] Timeouts de conexão, discovery e servidor; cancelamento e conclusão observável nos testes.

5. Garantias financeiras
- [x] Sem ponto flutuante monetário; BIGINT e int64, moeda e overflow.
- [x] Restrições de unicidade/não negatividade, ledger append-only e equação financeira.
- [x] Locks por linha e condição de versão; carteiras distintas sem lock global.
- [x] Eventos registrados no commit e enviados por worker posterior; lease/retry duráveis.

6. Domínio
- [x] Money: parsing estrito, zero, soma/subtração/negação/comparação, serialização e moedas incompatíveis.
- [x] Wallet: criação/reidratação, jogador/moeda única, versão 1 e mudanças controladas.
- [x] WagerTransaction: estados, resultado imutável, reidratação validada e terminais protegidos.
- [x] OPENING somente interno; abertura positiva atômica e abertura zero sem movimento.
- [x] Ledger: equação e unicidade, UPDATE/DELETE rejeitados no PostgreSQL.
- [x] Inbox: hash/identidade/registro e conclusão transacional; concorrência sem abortar a transação por unique violation.
- [x] Outbox: snapshot, identidade persistente, tentativas, disponibilidade e publicação.
- [x] FAILED é terminal e validado pelo domínio; indisponibilidade transitória faz rollback/retry, mensagens inválidas vão à DLQ. Não há necessidade de converter falhas recuperáveis em FAILED nem de persistir aceite intermediário.

7. Operações e referências
- [x] BET positivo e saldo suficiente; WIN positivo; LOSS zero sem ledger/versão.
- [x] WIN opcionalmente referenciado valida BET da mesma rodada.
- [x] REFUND integral de BET; ROLLBACK integral de BET/WIN/REFUND.
- [x] Provedor, jogador, carteira, moeda e rodada compatíveis; original processada.
- [x] Uma única devolução por BET, inclusive combinação REFUND/ROLLBACK, protegida por índices.
- [x] Reversão de crédito sem saldo é rejeitada auditavelmente com código próprio.
- [x] Ausência/pendência persistente com backoff e limite de 6 tentativas; expiração com evento de rejeição.
- [x] Referência rejeitada gera rejeição definitiva; pendência inválida não impede confirmar a original.

8. Concorrência
- [x] Dois BET de 80.00 sobre 100.00: um PROCESSED, um REJECTED persistido, saldo 20.00 e um débito.
- [x] 50 cópias da mesma BET em três processos: um movimento; replays preservam saldo original.
- [x] Carteiras distintas em paralelo no teste histórico, sem locking global.

9. HTTP
- [x] Abertura devolve id/saldo/versão e conflito por jogador/moeda.
- [x] Consulta de carteira e transações; acompanhamento de pendência/rejeição.
- [x] Ledger com cursor opaco ligado à carteira, ordem timestamp/ID e limite validado.
- [x] SHA-256 com chaves ordenadas, igualdade entre transportes e compatibilidade de replay com hash histórico.
- [x] Chave recebida preservada; conflito independente de chave e identidade externa.
- [x] Reconciliação REPEATABLE READ, inclui abertura, diferença armazenado-calculado; não altera saldo.
- [x] Divergências reportadas na resposta/log/métrica.
- [x] /health/live e /health/ready com PostgreSQL e filas reais.
- [x] Reconciliação consistente e divergente exercitadas; diferença decimal, contador e ausência de alteração do saldo verificados. REPEATABLE READ garante a visão consistente; teste específico de divergência sob escrita concorrente é ampliação de cobertura.

10. Consumidor SQS
- [x] Fila FIFO, DLQ, redrive de 5 recebimentos e destino de saída provisionados.
- [x] Identidade do envelope + hash, conclusão da inbox no mesmo commit e remoção posterior.
- [x] Rejeição definitiva é removível; falhas transitórias/entrada inválida recebem retries e DLQ.
- [x] Redrive real de mensagem inválida observado na DLQ.
- [x] Grupo por carteira e deduplicationId por mensagem documentados.
- [x] Cancelamento sem perda financeira; liberação de visibility quando possível ou expiração segura.

11. Outbox
- [x] Múltiplos processos/publishers e claims SKIP LOCKED; retomada após lease.
- [x] Falha após envio e antes da marca de publicação: segunda tentativa confirmada com mesmo eventId.
- [x] WagerTransactionProcessed, WagerTransactionRejected, WalletBalanceChanged e WagerTransactionPendingReference.
- [x] Payload de saldo completo, valores em strings, UTC; envelope versionado/correlacionado.
- [x] correlationId estável usa transactionId, com fallback histórico para aggregateId. causationId é explicitamente opcional; propagação de correlationId externo não é exigida pelo contrato de entrada.
- [x] Republicação conserva eventId e registra duas tentativas; a deduplicação FIFO dentro da janela é comportamento esperado do broker, não lacuna financeira.

12. Observabilidade
- [x] slog JSON, incluindo logs Fx; identidades disponíveis sem tokens ou payload financeiro completo.
- [x] Métricas de status/duplicatas/latência, retries, conflitos de concorrência, atraso da outbox e reconciliação.
- [x] Métricas DLQ visible/inflight consultam o broker real com timeout e são verificadas em integração. Log do commit SQS reúne messageId, correlationId, transactionId, walletId e providerId quando disponíveis.
- [-] OpenTelemetry e dashboards.

13. Verificação
- [x] Unitários Money/Wallet/ledger/transições/zero/OPENING e proteção contra mutabilidade do resultado.
- [x] Integração PostgreSQL/Keycloak/LocalStack real, constraints, atomicidade, retries, inbox/outbox e DLQ.
- [x] Ausência de token, JWT adulterado, cliente não autorizado e isolamento sem exposição de dados.
- [x] JWT genuíno assinado pelo Keycloak aceito antes de exp e rejeitado depois de exp com avanço apenas do relógio do verificador; nenhum token fabricado ou alteração do IdP.
- [x] Três instâncias, SIGTERM/reinício, transporte cruzado, confirmação/publicação e recibo abandonado.
- [x] Rodada final completa com race passou após todos os ajustes de código.
- [!] SIGKILL e outages físicos em todas as janelas não cobertos automaticamente.

14. Critérios e diferenciais
- [x] Garantias centrais financeiras e autenticação HTTP efetiva; evidências nos testes.
- [!] Autorização efetiva do broker permanece requisito obrigatório pendente. Simulações adicionais de SIGKILL/outages ampliam a evidência, mas os cenários de recuperação enumerados são exercitados com reinício, abandono de receipt, falha SQL direcionada e leases.
- [-] Partidas dobradas, tracing e testes de carga; não implementados para priorizar requisitos obrigatórios.

15. Entrega reproduzível
- [x] README.md, ARCHITECTURE.md, .env.example local sem segredos reais.
- [x] Migrations up em banco vazio; down/up da migration 4 em banco descartável separado.
- [x] Build Docker e runner de migrations; perfil app documentado para Linux/WSL2 com rede do host.
- [x] Código Go alterado formatado; suíte serial, race, vet, build Go e diff check passaram.
- [x] Infraestrutura recuperada; build Docker final concluiu compilação, exportação e unpack da imagem.
- [!] Perfil da aplicação com rede do host iniciou e verificou dependências nesta máquina, mas localhost do WSL não expôs a porta 8084 do container. Dependente da topologia Desktop; execução Go com infraestrutura publicada é o caminho principal validado.

## Comandos finais após recuperação da infraestrutura

- go test -p 1 ./... -count=1: exit 0 após todos os ajustes atuais.
- go test -race -p 1 ./... -count=1: exit 0 em todos os pacotes.
- Docker Compose build wallet: exit 0, imagem compilada/exportada.
- go vet ./..., build Go, git diff --check e docker compose config --quiet: exit 0.
- Imagem Docker: migrate up conectou ao banco de validação; processo Fx iniciou, health/live e health/ready responderam via wget dentro do container; SIGTERM encerrou com exit 0. O acesso a 8084 via localhost do WSL recusou conexão nesta topologia do Docker Desktop; smoke HTTP do container não comprova exposição dessa porta ao host. Execução Go com infraestrutura publicada foi validada ponta a ponta.
- Migrations em banco novo jungle_wallet_migration_final_test: up 1–4, down 4 e up 4 passaram. Banco da aplicação e volumes preservados.
- Testes utilizam jungle_wallet_test; configuração alternativa exige nome terminado em _test. Nenhum TRUNCATE de teste é direcionado ao banco da aplicação.
- Uma rodada intermediária detectou erro na asserção do novo teste de reconciliação (Money não tem decoding JSON de domínio); corrigida a asserção para conferir o DTO decimal. A resposta real já estava correta.
- Sem commit, stage, push ou merge nesta finalização.

## Classificação final das ressalvas

Obrigatório pendente: demonstrar autorização efetiva com validação de credenciais e políticas em broker capaz. Políticas agora estão versionadas e provisionadas em MiniStack, e o código aceita credenciais AWS; isso não comprova enforcement completo. Documentação oficial: https://docs.localstack.cloud/aws/developer-tools/security-testing/iam-policy-enforcement/ . Ativar enforcement requer edição/plano compatível e ENFORCE_IAM=1. Não contratamos plano, alteramos configuração global ou trocamos componentes funcionando.

Não são lacunas obrigatórias por si: causationId opcional; ausência de OpenTelemetry/dashboards/partidas dobradas/carga; não transformar falhas transitórias em FAILED; suprimir entrega física repetida dentro da janela FIFO; ausência de um teste separado para cada possível ponto de SIGKILL. As limitações de coverage permanecem explicitadas na checklist, sem alegar cobertura de todas as falhas possíveis.

## Avaliação

A implementação financeira está defensável nos cenários verificados. Não declarar atendimento integral nem entrega sem ressalvas enquanto a autorização efetiva do broker permanecer pendente. O bloqueio de disco/infraestrutura foi resolvido. Preservamos a branch codex-finalizacao e a main em 3189efef8617752b65a8315fb304653fc67f98a1.

## Arquivos alterados (git status, sem stage)

```text
 M cmd/wallet/main.go
 M docker-compose.yml
 M internal/application/create_wallet.go
 M internal/application/pending_reference_worker.go
 M internal/application/process_wager.go
 M internal/application/process_wager_integration_test.go
 M internal/config/config.go
 M internal/domain/wager_transaction.go
 M internal/infrastructure/auth/auth.go
 M internal/infrastructure/httpserver/routes.go
 M internal/infrastructure/httpserver/server.go
 M internal/infrastructure/httpserver/wager_handler.go
 M internal/infrastructure/httpserver/wallet_handler.go
 M internal/infrastructure/postgres/database.go
 M internal/infrastructure/postgres/inbox_repository.go
 M internal/infrastructure/postgres/inbox_repository_integration_test.go
 M internal/infrastructure/postgres/transaction.go
 M internal/infrastructure/postgres/wager_transaction_repository.go
 M internal/infrastructure/sqs/client.go
 M internal/infrastructure/sqs/consumer.go
 M internal/infrastructure/sqs/publisher.go
 M internal/infrastructure/sqs/worker.go
?? .dockerignore
?? .env.example
?? .gitignore
?? ARCHITECTURE.md
?? Dockerfile
?? README.md
?? VALIDATION.md
?? cmd/migrate/main.go
?? cmd/wallet/main_integration_test.go
?? deploy/broker/README.md
?? deploy/broker/authorization.cloudformation.json
?? docker-compose.broker-validation.yml
?? internal/application/financial_regression_integration_test.go
?? internal/application/wager_events.go
?? internal/domain/payload_hash.go
?? internal/domain/payload_hash_test.go
?? internal/domain/wager_safety_test.go
?? internal/infrastructure/auth/auth_integration_test.go
?? internal/infrastructure/httpserver/json_body.go
?? internal/infrastructure/httpserver/query_handler.go
?? internal/infrastructure/httpserver/reconciliation_integration_test.go
?? internal/infrastructure/postgres/financial_constraints_integration_test.go
?? internal/infrastructure/sqs/recovery_integration_test.go
?? internal/observability/metrics.go
?? internal/testsupport/database.go
?? migrations/000004_financial_guards.down.sql
?? migrations/000004_financial_guards.up.sql
?? migrations/embed.go
?? scripts/probe-broker-authorization.py
?? scripts/validate-broker-authorization.py
```

## Investigação específica de autorização do broker — 05/10/2026

Escopo: nenhum componente financeiro foi reescrito. Containers de probe usam portas 4567/4568/4569, filas e usuários com UUID próprios, sem ligação com o banco/filas da aplicação. Infraestrutura original, volumes e main preservados.

### Evidência real no LocalStack

Imagem Community 4.14.0, digest 3ebc37595918b8accb852f8048fef2aff047d465167edd655528065b07bc364a. ENFORCE_IAM=1 foi confirmado no container. A política de usuário concedia somente SendMessage ao sender; outsider não tinha grant e recebia Deny explícito na queue policy.

- Sender autorizado SendMessage: ALLOWED.
- Outsider sem grant + Deny explícito SendMessage: ALLOWED (falha).
- Sender ReceiveMessage fora do grant: ALLOWED (falha).
- Access key autorizado com secret incorreto: ALLOWED (falha).
- Access key desconhecido: ALLOWED (falha).

Fila da reprodução Compose: arn:aws:sqs:us-east-1:000000000000:broker-auth-probe-fe3d54e6c905.fifo. O probe retorna resultado não aprovado; não basta ativar a variável para habilitar uma funcionalidade ausente na edição.

### Evidência real no MiniStack

AUTH=true; versões 1.5.18 e 1.5.21. A reprodução final fixa 1.5.21 por digest 7a30ff0670578e10528b5a9a21e795ef494f96bbc35691c9b0742ea35de71808.

- Sender autorizado SendMessage: ALLOWED.
- Outsider sem grant + Deny explícito SendMessage: DENIED (AccessDeniedException).
- Sender ReceiveMessage fora do grant: DENIED (AccessDeniedException).
- Access key desconhecido: DENIED (UnrecognizedClientException).
- Access key autorizado com secret incorreto: ALLOWED (falha).

Fila da reprodução Compose: arn:aws:sqs:us-east-1:000000000000:broker-auth-probe-bf78c145e3bb.fifo. Assim, há enforcement de políticas de usuário, mas não foi demonstrada validação do secret/assinatura. Não alegamos que IAM esteja completamente comprovado.

### Configuração implementada para AWS

deploy/broker/authorization.cloudformation.json: três filas FIFO isoladas, redrive após cinco recebimentos, roles ProducerRole/WalletRole/EventReaderRole e UnauthorizedRole. Trust limitado ao principal IAM fornecido. Grants limitados por ação e ARN; queue policies negam envio/consumo às roles não previstas. WalletRole tem as permissões necessárias ao worker/publisher/readiness/métricas, sem administrar filas/IAM.

O template foi aceito/provisionado pelo CloudFormation do MiniStack (stack jungle-broker-auth-0d528af8d6). O grant ProducerRole e a policy RequestQueue foram consultados e tinham ARNs/ações/conditions resolvidos conforme o template. A validação por roles STS falhou já na chamada autorizada SendMessage com AccessDenied. Não atribuímos isso ao serviço AWS, nem declaramos o template certificado por AWS. Nenhuma policy foi flexibilizada para fazer o emulador passar.

scripts/validate-broker-authorization.py executa chamadas reais com sessões STS das quatro roles. Exige sucesso do envio/consumo/visibility/remoção autorizados e das leituras de metadata; exige recusa para outsiders, ações fora do grant, secret incorreto, chave desconhecida e requisição sem assinatura. Falhas de rede, recursos inexistentes ou erros genéricos não contam como autorização aprovada. Não usa mocks/simulação IAM.

### Limitação objetiva e fechamento

Não foram encontradas credenciais AWS, arquivos de profile/credentials ou LOCALSTACK_AUTH_TOKEN no Windows/WSL. Nenhuma conta AWS foi usada e não houve contratação de plano. Documentação LocalStack confirma enforcement como funcionalidade licenciada; os probes documentam o comportamento exato das versões locais. Uma edição licenciada ainda precisa passar pelo mesmo teste, inclusive secret incorreto.

A alternativa mínima correta é executar o template e a prova em uma conta AWS sandbox autorizada (ou edição de emulador que passe integralmente os checks). Comandos reproduzíveis estão em deploy/broker/README.md. Isso requer acesso externo ainda indisponível, sem necessidade de reescrever a aplicação.

**Todos os requisitos obrigatórios estão atendidos? NÃO.** O requisito de autorização efetiva do broker segue parcialmente implementado/configurado, com evidência limitada de política e falha explícita na prova de credenciais local. Esta investigação eliminou a incerteza da limitação, não eliminou a pendência. Não há uma falsa garantia.

### Validação final após esta investigação

- go test -p 1 ./... -count=1: exit 0, todos os pacotes; cenário de três processos em 63.869s.
- go test -race -p 1 ./... -count=1: exit 0, todos os pacotes; cenário de três processos em 69.049s.
- go vet ./..., build Go e Docker build: exit 0.
- docker compose -f docker-compose.broker-validation.yml config --quiet: exit 0.
- Scripts Python: AST/sintaxe válidos; JSON do template válido.
- Probes locais de autorização: resultado não aprovado conforme as evidências; não registrados como testes de requisito aprovados.
- git diff --check: exit 0.
- Branch codex-finalizacao; main continua 3189efef8617752b65a8315fb304653fc67f98a1; sem stage/commit/push/merge.
- Containers de probe parados ao fim; nenhum volume foi removido/resetado. Infraestrutura original segue em execução, com PostgreSQL e LocalStack healthy.
- Esta rodada acrescentou dois scripts, Compose isolado, template CloudFormation e instruções; atualizou README/ARCHITECTURE/VALIDATION. Nenhum código financeiro Go foi alterado nesta investigação.
