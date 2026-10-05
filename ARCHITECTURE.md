# Arquitetura e decisões

## Composição e transação

Uber Fx compõe configuração, pgx, OIDC, repositórios, casos de uso, handlers e workers no módulo wallet. Domínio não depende de Fx, transporte ou persistência. SQL é explícito, tornando locks e constraints verificáveis. Money é imutável, com int64 de unidades mínimas e moeda persistidos como BIGINT/VARCHAR.

`DB.WithTx` compartilha um pgx.Tx entre repositórios. Não existe commit intermediário de PENDING nas operações síncronas. No consumo SQS, inbox e conclusão durável compartilham essa mesma transação. Pendências PENDING_REFERENCE ficam no banco e podem ser retomadas por outra instância.

Lock pessimista por carteira e condição de versão impedem lost updates. Releitura da identidade depois do lock evita aplicar um reenvio que aguardou outra transação. Constraints financeiras são uma segunda proteção. Índices adicionais impedem abertura repetida e reversões repetidas; não alteram migrations históricas.

## Reversões e rejeições

Referência precisa coincidir em provedor, jogador, carteira, moeda e rodada e estar processada. REFUND só aceita BET; ROLLBACK aceita BET, WIN ou REFUND. Reversão sempre integral.

Uma BET só admite **uma** devolução, por REFUND **ou** ROLLBACK. Após REFUND, pode-se desfazer esse REFUND por ROLLBACK referenciando-o diretamente; não se permite devolver a BET novamente. É uma política conservadora, explícita e protegida por índice único.

Pendência com referência ainda não terminal entra no mesmo retry limitado das referências ausentes. Referência rejeitada/falha produz rejeição definitiva. Uma pendência inválida é rejeitada sem impedir o commit da original. Rejeição não movimenta saldo nem cria ledger; produz evento e mantém resultado idempotente auditável.

Códigos estáveis:
- `INSUFFICIENT_BALANCE`: BET sem saldo.
- `REVERSAL_INSUFFICIENT_BALANCE`: reversão de crédito sem saldo para débito.
- `REVERSAL_AMOUNT_MISMATCH`: reversão não integral.
- `REFERENCE_MISMATCH`: identidades, moeda ou rodada incompatíveis.
- `REFERENCE_REJECTED`: original terminal sem sucesso.
- `REFERENCE_NOT_FOUND`: tentativas esgotadas para original ausente/não concluída.
- `INVALID_REFERENCE_KIND`: tipo original não reversível/ incompatível.
- `ALREADY_REVERSED`: devolução já confirmada.
- `BALANCE_OVERFLOW`, `INVALID_OPERATION`, `INVALID_REVERSAL`: operação financeira inválida.

PENDING → PROCESSED / PENDING_REFERENCE / REJECTED. PENDING_REFERENCE → PENDING ao resolver referência, depois PROCESSED / REJECTED. FAILED existe no modelo, mas erros transitórios de I/O fazem rollback e retry; não há commit de FAILED para indisponibilidade recuperável. Estados terminais não podem transicionar.

## Hash e contratos

SHA-256 sobre JSON dos campos de negócio com chaves ordenadas lexicograficamente: providerId, externalTransactionId, playerId, walletId, roundId, gameId, kind, money e referenceExternalTransactionId (omitido quando vazio). Money tem amount/currency ordenados e escala estrita, sem normalização ou arredondamento. Idempotency-Key, messageId e occurredAt não entram. A representação é equivalente entre HTTP e SQS. Replays também reconhecem o hash histórico de ordem fixa dos campos para preservar operações confirmadas antes desta finalização; novas operações persistem o hash ordenado.

O evento persistido é um snapshot serializado. Nomes internos históricos em snake case maiúsculo são traduzidos no publisher para WagerTransactionProcessed, WagerTransactionRejected, WagerTransactionPendingReference e WalletBalanceChanged. Envelope contém eventId, eventType, aggregateType, aggregateId, correlationId, occurredAt UTC, version=1 e data. A correlação local usa transactionId do snapshot, com fallback para aggregateId em registros históricos; não propaga um correlationId externo nem causationId de entrada.

WalletBalanceChanged tem walletId, transactionId, direction, money, balanceBefore, balanceAfter e walletVersion. LOSS produz somente evento de transação. A abertura usa dados internos sem metadados externos inventados.

## Autenticação e recursos

Keycloak real provisiona clientes locais para client credentials. O token é validado por assinatura, issuer e expiração; providerId precisa corresponder à identidade autenticada. Consultas filtram provedor na própria query. A carteira e reconciliação só são acessíveis ao cliente interno configurado.

A verificação de audience está desabilitada para os access tokens de serviço atuais; a autorização depende de identidade assinada do cliente e issuer configurado. Em produção, restrinja audiences e configure a política SQS/IAM do broker. O cliente utiliza a cadeia padrão de credenciais AWS para endpoints externos; o fallback test/test só se aplica ao endpoint local sem credenciais explícitas. LocalStack Community não impõe políticas IAM. As políticas estão versionadas e foram provisionadas no MiniStack, mas falta comprovar sua aplicação efetiva com validação de credenciais; veja a investigação abaixo. Isso é um requisito obrigatório parcial, não um diferencial. A documentação oficial de enforcement exige um plano compatível e ENFORCE_IAM=1 (https://docs.localstack.cloud/aws/developer-tools/security-testing/iam-policy-enforcement/). Não habilitamos plano pago nem substituímos o broker nesta finalização.

Fx registra e cancela workers, espera seu término e fecha o pool depois. O servidor valida bind na inicialização, interrompe novas entradas e aguarda/fecha conexões no shutdown. Transações não confirmadas são revertidas; mensagens não removidas voltam pela visibilidade. Publisher pode publicar e morrer antes de marcar a outbox, portanto reenvios preservam identidade.

## Reconciliação e observabilidade

Reconciliação usa transação read-only em REPEATABLE READ e soma NUMERIC no banco; conversão final int64 detecta overflow. Inclui OPENING, informa divergência sem corrigir saldo e usa métrica/log.

Logs de aplicação usam slog JSON sem tokens ou payloads financeiros completos. Métricas locais incluem resultados HTTP/SQS por status, duplicatas, duração, retries e candidatos à DLQ, mais conflitos de concorrência, atraso da outbox e divergências de reconciliação. DLQ também expõe gauges visible/inflight consultados no broker. O log de conclusão SQS reúne os identificadores disponíveis. Consulte VALIDATION.md para os limites restantes.

## Investigação e configuração da autorização SQS

A camada financeira e o consumidor foram preservados. A solução correta é IAM/SQS no broker; não foi introduzido proxy nem substituída a autorização por validação de payload. O template deploy/broker/authorization.cloudformation.json versiona roles Producer/Wallet/EventReader e uma role negativa, trust restrito, grants por ação/ARN e queue policies de negação para identidades fora das roles previstas. A cadeia de credenciais AWS já é suportada pelo cliente Go.

Probes em containers separados demonstraram que LocalStack Community 4.14.0 com ENFORCE_IAM=1 não aplica as restrições. MiniStack 1.5.18 e 1.5.21 com AUTH=true aplicam as políticas de usuário, mas aceitam assinatura com secret errado. O template foi provisionado por CloudFormation no MiniStack; a prova por roles falhou na operação autorizada de envio. Não flexibilizamos as policies AWS para contornar diferenças do emulador. O teste completo exige resultados positivos e negativos reais, incluindo secret errado, chave desconhecida e chamada sem assinatura.

Os scripts recusam validar filas operacionais fora do namespace exclusivo, não registram credenciais e removem somente mensagens marcadas pela própria execução. Instruções e limites em deploy/broker/README.md. Sem credenciais AWS/licença disponíveis, configuração preparada não equivale a prova de enforcement.
