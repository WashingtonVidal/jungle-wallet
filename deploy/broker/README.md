# Autorização do broker: configuração e prova

A solução mínima preserva Go, PostgreSQL, HTTP, SQS e o caso de uso. Autorização é aplicada pelo broker, não por um proxy caseiro ou um filtro de payload no consumidor.

## Resultado local comprovado

LocalStack Community 4.14.0, mesmo com ENFORCE_IAM=1, permite SendMessage para usuário sem grant e com Deny explícito; permite ReceiveMessage fora do grant e secret incorreto.

MiniStack 1.5.18 e 1.5.21 com AUTH=true recusam usuário sem grant/Deny explícito, consumo fora do grant e access key desconhecido. Porém aceitam assinatura com access key autorizado e secret incorreto. Portanto não substituem a comprovação completa da autenticação/autorização AWS. Não consideramos esses probes aprovação do requisito.

Para reproduzir sem tocar nas filas/volumes da aplicação:

    docker compose -f docker-compose.broker-validation.yml up -d
    docker compose -f docker-compose.broker-validation.yml exec -T localstack-probe /opt/code/localstack/.venv/bin/python /validation/probe.py
    docker compose -f docker-compose.broker-validation.yml exec -T ministack-probe python /validation/probe.py

Os comandos de probe devem falhar com código 3 quando qualquer chamada protegida for aceita. Falhas de rede/serviço não são consideradas uma recusa por permissão. Cada execução cria fila FIFO e identidades com UUID próprios. Não faz purge, reset ou remoção de recursos existentes; fixtures ficam disponíveis para inspeção enquanto o container está em execução (estado dos probes é temporário). Não registra chaves/segredos.

Apenas se quiser encerrar os containers de validação:

    docker compose -f docker-compose.broker-validation.yml stop

Não confundir stop com down -v. A infraestrutura original permanece independente.

## Políticas para AWS real

authorization.cloudformation.json cria três filas FIFO separadas para validação, redrive com cinco recebimentos e quatro roles. Trust limita AssumeRole a um principal IAM existente fornecido por parâmetro, sem Principal público.

- ProducerRole: apenas SendMessage na fila de entrada.
- WalletRole: ReceiveMessage/DeleteMessage/ChangeMessageVisibility e leitura de atributos/URL da entrada; envio/leitura de atributos/URL da saída; leitura de atributos/URL da DLQ para métricas.
- EventReaderRole: leitura/remoção/visibility de eventos e DLQ.
- UnauthorizedRole: Deny de SQS nas três filas para o cenário negativo.

As queue policies também negam envio/consumo por roles diferentes das previstas. aws:PrincipalArn usa ARN da role IAM, conforme AWS; não usa ARN da sessão STS. O papel financeiro não pode administrar IAM, criar/purgar filas ou editar policies. Não há dinheiro, credenciais reais ou banco da aplicação no template.

O template foi provisionado pelo CloudFormation em MiniStack; o grant ProducerRole e a queue policy RequestQueue foram consultados e conferidos. A validação completa por roles nesse emulador falhou já no SendMessage autorizado. A causa dessa divergência não foi atribuída à AWS: ainda falta executar validação no serviço real. Isso é evidência limitada, não certificação da semântica AWS.

## Executar a prova em AWS

Requer conta sandbox explicitamente autorizada, profile já configurado, boto3 instalado e principal capaz de provisionar CloudFormation/IAM/SQS e assumir as roles do template. Nenhuma credencial/licença foi encontrada no ambiente desta finalização; nenhum recurso AWS foi criado.

Use um nome/prefixo exclusivo. O principal de trust deve ser ARN de IAM user ou role, nunca ARN de sessão assumida. Exemplo (substituir valores antes de executar):

    aws --profile CHALLENGE_SANDBOX --region us-east-1 cloudformation validate-template --template-body file://deploy/broker/authorization.cloudformation.json
    aws --profile CHALLENGE_SANDBOX --region us-east-1 cloudformation deploy --stack-name jungle-wallet-broker-auth --template-file deploy/broker/authorization.cloudformation.json --capabilities CAPABILITY_IAM --parameter-overrides TrustedPrincipalArn=ARN_DO_PRINCIPAL_IAM QueuePrefix=jungle-wallet-auth-validation-seu-sufixo
    aws --profile CHALLENGE_SANDBOX --region us-east-1 cloudformation describe-stacks --stack-name jungle-wallet-broker-auth > /tmp/jungle-wallet-broker-outputs.json
    python3 scripts/validate-broker-authorization.py --profile CHALLENGE_SANDBOX --region us-east-1 --outputs /tmp/jungle-wallet-broker-outputs.json

Aguarde a propagação de IAM após o deploy; uma falha de configuração/propagação não conta como sucesso. O teste assume as quatro roles usando STS e usa as credenciais temporárias para chamadas reais. Credenciais nunca são impressas.

O namespace jungle-wallet-auth-validation é obrigatório no teste, impedindo execução em filas operacionais. Não execute outros consumidores nessas filas durante a prova. São removidas somente mensagens cujo corpo é o marcador único desta execução; o script recusa remover qualquer mensagem alheia.

Provas exigidas: produtor envia entrada; carteira recebe/ajusta visibility/remove entrada e publica saída; leitor recebe/remove saída; leitura de atributos/URL necessária à aplicação funciona. Negativas: outsider não envia/consome; produtor não consome nem publica eventos; carteira não envia solicitações; leitor não publica; produtor não acessa DLQ; secret errado, chave desconhecida e requisição sem assinatura são recusados. Apenas erros específicos de autenticação/autorização contam como recusa. Todas as etapas devem passar para exit 0.

Para LocalStack com edição licenciada e enforcement, o mesmo script aceita --endpoint-url; provisionamento também deve usar --endpoint-url no AWS CLI. É necessário validar inclusive secret errado: somente anunciar ENFORCE_IAM não basta.

## Conectar a aplicação ao broker AWS

O código existente usa a cadeia AWS do SDK para endpoints externos (profile, credenciais temporárias ou role de workload). Configure AWS_REGION, SQS_ENDPOINT=https://sqs.REGIAO.amazonaws.com, SQS_QUEUE_URL e SQS_OUTBOX_QUEUE_URL com os Outputs, e execute com identidade WalletRole; produtores utilizam ProducerRole e consumidores de eventos EventReaderRole.

Nunca entregue ao serviço as credenciais de bootstrap/admin ou de outras roles. No ambiente real, vincule roles às workloads e restrinja o trust ao mecanismo de execução; o trust deste template é específico para demonstração controlada por um operador. Os ARNs das políticas devem apontar às filas operacionais ao aplicar o mesmo modelo fora da sandbox.

HTTP continua usando Keycloak e a validação de domínio no consumidor permanece intacta. Não temos uma prova de deployment AWS nesta máquina; enquanto a execução completa não ocorrer, o requisito segue parcial.
