package httpserver

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"jungle-wallet/internal/application"
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
	queues "jungle-wallet/internal/infrastructure/sqs"
	"jungle-wallet/internal/testsupport"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReconciliationReportsDivergenceWithoutChangingBalanceIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := postgres.New(ctx, testsupport.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	money, err := domain.Parse("100.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	service := application.NewCreateWalletService(db, postgres.NewWalletRepository(), postgres.NewWagerTransactionRepository(), postgres.NewLedgerRepository(), postgres.NewOutboxRepository())
	if _, err := service.Execute(ctx, application.CreateWalletInput{ID: id, PlayerID: uuid.NewString(), InitialBalance: money, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// Fault injection touches only this test's wallet in the dedicated test database.
	if _, err := db.Pool.Exec(ctx, "UPDATE wallets SET balance_units=9000 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	before := reconciliationDivergences.Load()
	request := httptest.NewRequest("POST", "/wallets/"+id+"/reconciliation", nil)
	request.SetPathValue("walletId", id)
	response := httptest.NewRecorder()
	NewQueryHandler(db, nil).Reconcile(response, request)
	if response.Code != 200 {
		t.Fatalf("status %d", response.Code)
	}
	var result struct {
		Consistent bool `json:"consistent"`
		Difference struct {
			Amount string `json:"amount"`
		} `json:"difference"`
		CheckedEntries int `json:"checkedEntries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Consistent || result.Difference.Amount != "-10.00" || result.CheckedEntries != 1 {
		t.Fatalf("unexpected divergence result: %s", response.Body.String())
	}
	if reconciliationDivergences.Load() != before+1 {
		t.Fatal("divergence metric not incremented")
	}
	var stored int64
	if err := db.Pool.QueryRow(ctx, "SELECT balance_units FROM wallets WHERE id=$1", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 9000 {
		t.Fatal("reconciliation modified wallet")
	}
}

func TestDLQMetricsReadRealBrokerIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := postgres.New(ctx, testsupport.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client, err := queues.NewClient(ctx, &config.Config{AWSRegion: "us-east-1", SQSEndpoint: "http://localhost:4566"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewQueryHandler(db, &queues.ClientDependencies{Client: client, QueueURL: "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo"})
	response := httptest.NewRecorder()
	handler.Metrics(response, httptest.NewRequest("GET", "/metrics", nil))
	for _, name := range []string{"jungle_wallet_dlq_visible_messages ", "jungle_wallet_dlq_inflight_messages "} {
		if !strings.Contains(response.Body.String(), name) {
			t.Fatalf("missing real broker gauge %s", name)
		}
	}
}
