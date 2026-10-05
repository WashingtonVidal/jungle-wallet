package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"jungle-wallet/internal/application"
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
	"jungle-wallet/internal/testsupport"
	"testing"
	"time"
)

func recoveryClient(t *testing.T) (context.Context, *awssqs.Client, string) {
	t.Helper()
	ctx := context.Background()
	client, err := NewClient(ctx, &config.Config{AWSRegion: "us-east-1", SQSEndpoint: "http://localhost:4566"})
	if err != nil {
		t.Fatal(err)
	}
	name := "test-recovery-" + uuid.NewString() + ".fifo"
	queue, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: &name, Attributes: map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := client.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: queue.QueueUrl})
		if err != nil {
			t.Error(err)
		}
	})
	return ctx, client, *queue.QueueUrl
}
func recoveryDB(t *testing.T) *postgres.DB {
	t.Helper()
	db, err := postgres.New(context.Background(), testsupport.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}
func serviceFor(db *postgres.DB) *application.ProcessWagerService {
	return application.NewProcessWagerService(db, postgres.NewWalletRepository(), postgres.NewWagerTransactionRepository(), postgres.NewLedgerRepository(), postgres.NewOutboxRepository())
}
func TestConsumerReentryAfterCommitBeforeDeleteIntegration(t *testing.T) {
	ctx, client, queue := recoveryClient(t)
	db := recoveryDB(t)
	wallet, player := uuid.NewString(), uuid.NewString()
	initial, _ := domain.Parse("100.00", "BRL")
	create := application.NewCreateWalletService(db, postgres.NewWalletRepository(), postgres.NewWagerTransactionRepository(), postgres.NewLedgerRepository(), postgres.NewOutboxRepository())
	if _, err := create.Execute(ctx, application.CreateWalletInput{ID: wallet, PlayerID: player, InitialBalance: initial, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	envelope := WagerTransactionEnvelope{MessageID: id, Type: "WagerTransactionRequested", OccurredAt: time.Now().UTC(), Data: WagerTransactionData{ProviderID: "provider-a", ExternalTransactionID: id, IdempotencyKey: id, WalletID: wallet, PlayerID: player, RoundID: "round", GameID: "game", Kind: "BET", Money: domain.MoneyInput{Amount: "25.00", Currency: "BRL"}}}
	body, _ := json.Marshal(envelope)
	text := string(body)
	if _, err := client.SendMessage(ctx, &awssqs.SendMessageInput{QueueUrl: &queue, MessageBody: &text, MessageGroupId: &wallet, MessageDeduplicationId: &id}); err != nil {
		t.Fatal(err)
	}
	receive := func() types.Message {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			output, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: &queue, MaxNumberOfMessages: 1, VisibilityTimeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(output.Messages) > 0 {
				return output.Messages[0]
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("reentry timeout")
			}
		}
	}
	first := receive()
	consumer := NewConsumer(db, postgres.NewInboxRepository(), serviceFor(db))
	if err := consumer.ProcessMessage(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Deliberately abandon the committed receipt. Reconstruct the consumer with
	// a new pool; broker visibility expiry is the recovery condition.
	db2 := recoveryDB(t)
	consumer2 := NewConsumer(db2, postgres.NewInboxRepository(), serviceFor(db2))
	redelivered := receive()
	if err := consumer2.ProcessMessage(ctx, redelivered); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: &queue, ReceiptHandle: redelivered.ReceiptHandle}); err != nil {
		t.Fatal(err)
	}
	var balance int64
	var entries int
	if err := db2.Pool.QueryRow(ctx, "SELECT balance_units FROM wallets WHERE id=$1", wallet).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := db2.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id=$1", wallet).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if balance != 7500 || entries != 2 {
		t.Fatalf("duplicate movement: %d/%d", balance, entries)
	}
	envelope.Data.Money.Amount = "26.00"
	changed, _ := json.Marshal(envelope)
	changedString := string(changed)
	if err := consumer2.ProcessMessage(ctx, types.Message{Body: &changedString}); !errors.Is(err, postgres.ErrInboxPayloadConflict) {
		t.Fatalf("inbox conflict: %v", err)
	}
}
func TestOutboxRepublishAfterSendBeforeConfirmationIntegration(t *testing.T) {
	ctx, client, queue := recoveryClient(t)
	db := recoveryDB(t)
	// The suite is serial and this is exclusively the local test database.
	if _, err := db.Pool.Exec(ctx, "TRUNCATE outbox"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	outbox := postgres.NewOutboxRepository()
	if err := outbox.Insert(ctx, db.Pool, postgres.OutboxMessage{ID: id, AggregateType: "WAGER_TRANSACTION", AggregateID: id, EventType: "WAGER_TRANSACTION_PROCESSED", Payload: []byte(`{"transactionId":"` + id + `","status":"PROCESSED"}`), AvailableAt: now, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	sql := fmt.Sprintf(`CREATE FUNCTION test_fail_publish_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='%s' AND NEW.published_at IS NOT NULL THEN RAISE EXCEPTION 'test injected confirmation failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER test_fail_publish_confirmation BEFORE UPDATE ON outbox FOR EACH ROW EXECUTE FUNCTION test_fail_publish_confirmation();`, id)
	if _, err := db.Pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	clean := func() {
		if _, err := db.Pool.Exec(ctx, "DROP TRIGGER IF EXISTS test_fail_publish_confirmation ON outbox; DROP FUNCTION IF EXISTS test_fail_publish_confirmation();"); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(clean)
	first := NewPublisher(client, db, outbox, queue)
	if err := first.publishNext(ctx); err == nil {
		t.Fatal("expected confirmation failure after broker accepted")
	}
	output, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{QueueUrl: &queue, MaxNumberOfMessages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Messages) != 1 {
		t.Fatal("send did not precede confirmation failure")
	}
	var envelope outboxEnvelope
	if err := json.Unmarshal([]byte(*output.Messages[0].Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.EventID != id || envelope.Version != 1 || envelope.CorrelationID != id {
		t.Fatal("event identity changed")
	}
	if _, err := client.DeleteMessage(ctx, &awssqs.DeleteMessageInput{QueueUrl: &queue, ReceiptHandle: output.Messages[0].ReceiptHandle}); err != nil {
		t.Fatal(err)
	}
	clean()
	// Advance only the test row's persisted lease to simulate expiry; no sleep.
	if _, err := db.Pool.Exec(ctx, "UPDATE outbox SET available_at=NOW() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	db2 := recoveryDB(t)
	second := NewPublisher(client, db2, postgres.NewOutboxRepository(), queue)
	if err := second.publishNext(ctx); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var published *time.Time
	if err := db2.Pool.QueryRow(ctx, "SELECT attempts,published_at FROM outbox WHERE id=$1", id).Scan(&attempts, &published); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || published == nil {
		t.Fatalf("outbox recovery %d/%v", attempts, published)
	}
	// SQS FIFO may suppress the second delivery within its 5-minute window;
	// retry identity and the second application publish attempt remain durable.
}
