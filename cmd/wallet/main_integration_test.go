package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"jungle-wallet/internal/testsupport"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/postgres"
	queues "jungle-wallet/internal/infrastructure/sqs"
)

// This helper executes in a separate OS process with its own Fx graph and pool.
func TestInstanceHelper(t *testing.T) {
	if os.Getenv("JUNGLE_TEST_INSTANCE") != "1" {
		t.Skip("subprocess helper")
	}
	app := fx.New(walletModule(), fx.NopLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("INSTANCE_READY")
	<-app.Wait()
	stop, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStop()
	if err := app.Stop(stop); err != nil {
		t.Fatal(err)
	}
}
func testToken(t *testing.T, clientID string) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/keycloak/realm-export.json")
	if err != nil {
		t.Fatal(err)
	}
	var realm struct {
		Clients []struct {
			ClientID string `json:"clientId"`
			Secret   string `json:"secret"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(raw, &realm); err != nil {
		t.Fatal(err)
	}
	secret := ""
	for _, client := range realm.Clients {
		if client.ClientID == clientID {
			secret = client.Secret
		}
	}
	if secret == "" {
		t.Fatal("fixture client missing")
	}
	resp, err := http.PostForm("http://localhost:8081/realms/jungle-wallet/protocol/openid-connect/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var data struct {
		Token string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&data) != nil || data.Token == "" {
		t.Fatal("IdP token request failed")
	}
	return data.Token
}
func TestThreeInstancesHTTPAndSQSIntegration(t *testing.T) {
	internalToken := testToken(t, "wallet-internal")
	providerToken := testToken(t, "provider-a")
	bases := make([]string, 3)
	stops := make([]func(), 3)
	for i := range bases {
		bases[i], stops[i] = startTestInstance(t, "")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(base, method, path, token, key string, body any) (int, map[string]any) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, map[string]any{"error": "request failed"}
		}
		defer resp.Body.Close()
		data, _ = io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}
	expect := func(status, want int) {
		t.Helper()
		if status != want {
			t.Fatalf("HTTP status=%d want=%d", status, want)
		}
	}
	player := uuid.NewString()
	walletBody := map[string]any{"playerId": player, "initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"}}
	status, _ := request(bases[0], "POST", "/wallets", "", "", walletBody)
	expect(status, 401)
	status, _ = request(bases[0], "POST", "/wallets", providerToken+"invalid", "", walletBody)
	expect(status, 401)
	status, _ = request(bases[0], "POST", "/wallets", providerToken, "", walletBody)
	expect(status, 403)
	status, wallet := request(bases[0], "POST", "/wallets", internalToken, "", walletBody)
	expect(status, 201)
	id := wallet["id"].(string)
	status, _ = request(bases[1], "POST", "/wallets", internalToken, "", walletBody)
	expect(status, 409)
	bet := func(external, amount string) map[string]any {
		return map[string]any{"providerId": "provider-a", "externalTransactionId": external, "playerId": player, "walletId": id, "roundId": "round", "gameId": "game", "kind": "BET", "money": map[string]string{"amount": amount, "currency": "BRL"}}
	}
	a, b := bet(uuid.NewString(), "80.00"), bet(uuid.NewString(), "80.00")
	start := make(chan struct{})
	results := make(chan int, 2)
	for i, body := range []map[string]any{a, b} {
		go func(i int, body map[string]any) {
			<-start
			status, _ := request(bases[i], "POST", "/wagering/transactions", providerToken, body["externalTransactionId"].(string), body)
			results <- status
		}(i, body)
	}
	close(start)
	x, y := <-results, <-results
	if !((x == 200 && y == 422) || (x == 422 && y == 200)) {
		t.Fatalf("dispute statuses %d/%d", x, y)
	}
	dup := bet(uuid.NewString(), "5.00")
	key := dup["externalTransactionId"].(string)
	statuses := make(chan int, 50)
	start = make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(i int) {
			<-start
			s, _ := request(bases[i%3], "POST", "/wagering/transactions", providerToken, key, dup)
			statuses <- s
		}(i)
	}
	close(start)
	for i := 0; i < 50; i++ {
		expect(<-statuses, 200)
	}
	status, original := request(bases[2], "POST", "/wagering/transactions", providerToken, key, dup)
	expect(status, 200)
	if original["idempotentReplay"] != true {
		t.Fatal("replay missing")
	}
	transactionID := original["transactionId"].(string)
	status, _ = request(bases[0], "GET", "/wagering/transactions/"+transactionID, internalToken, "", nil)
	expect(status, 404)
	status, _ = request(bases[0], "GET", "/providers/provider-a/wagering/transactions/"+key, internalToken, "", nil)
	expect(status, 403)
	changed := bet(key, "6.00")
	status, _ = request(bases[0], "POST", "/wagering/transactions", providerToken, key, changed)
	expect(status, 409)
	status, _ = request(bases[0], "GET", "/wallets/"+id, providerToken, "", nil)
	expect(status, 403)
	status, state := request(bases[0], "GET", "/wallets/"+id, internalToken, "", nil)
	expect(status, 200)
	if state["balance"].(map[string]any)["amount"] != "15.00" {
		t.Fatal("duplicate movement or lost update")
	}
	status, rec := request(bases[2], "POST", "/wallets/"+id+"/reconciliation", internalToken, "", nil)
	expect(status, 200)
	if rec["consistent"] != true || rec["checkedEntries"] != float64(3) {
		t.Fatal("ledger reconciliation failed")
	}
	status, page := request(bases[2], "GET", "/wallets/"+id+"/ledger?limit=1", internalToken, "", nil)
	expect(status, 200)
	cursor := page["nextCursor"].(string)
	status, page = request(bases[1], "GET", "/wallets/"+id+"/ledger?limit=1&cursor="+url.QueryEscape(cursor), internalToken, "", nil)
	expect(status, 200)
	if len(page["entries"].([]any)) != 1 {
		t.Fatal("cursor pagination failed")
	}
	status, _ = request(bases[2], "GET", "/health/ready", "", "", nil)
	expect(status, 200)
	// SQS uses the same business identity/hash even with an old occurredAt.
	cfg := &config.Config{AWSRegion: "us-east-1", SQSEndpoint: "http://localhost:4566"}
	sqsClient, err := queues.NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	queue := "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo"
	data := map[string]any{}
	for k, v := range dup {
		data[k] = v
	}
	data["idempotencyKey"] = key
	messageID := uuid.NewString()
	body, _ := json.Marshal(map[string]any{"messageId": messageID, "type": "WagerTransactionRequested", "occurredAt": "2026-09-08T12:00:00Z", "data": data})
	bodyString := string(body)
	_, err = sqsClient.SendMessage(context.Background(), &awssqs.SendMessageInput{QueueUrl: &queue, MessageBody: &bodyString, MessageGroupId: &id, MessageDeduplicationId: &messageID})
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.New(context.Background(), testsupport.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for {
		var n int
		if err := db.Pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM inbox WHERE message_id=$1 AND status='PROCESSED'", messageID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("SQS processing timeout")
		}
	}
	status, state = request(bases[2], "GET", "/wallets/"+id, internalToken, "", nil)
	expect(status, 200)
	if state["balance"].(map[string]any)["amount"] != "15.00" {
		t.Fatal("HTTP/SQS financial duplicate")
	}
	// Stop and reconstruct a real process with a fresh pool and memory.
	stops[2]()
	bases[2], stops[2] = startTestInstance(t, bases[2])
	// Independent processes can be restarted without losing identity.
	status, again := request(bases[2], "POST", "/wagering/transactions", providerToken, "alternate-"+key, dup)
	expect(status, 200)
	if again["balance"].(map[string]any)["amount"] != original["balance"].(map[string]any)["amount"] {
		t.Fatal("replay balance changed")
	}
	// Pending response and eventual resolution are visible through the read contract.
	pendingID := uuid.NewString()
	referenceID := uuid.NewString()
	pending := bet(pendingID, "2.00")
	pending["kind"] = "REFUND"
	pending["referenceExternalTransactionId"] = referenceID
	status, result := request(bases[0], "POST", "/wagering/transactions", providerToken, pendingID, pending)
	expect(status, 202)
	pendingTransaction := result["transactionId"].(string)
	ref := bet(referenceID, "2.00")
	status, _ = request(bases[1], "POST", "/wagering/transactions", providerToken, referenceID, ref)
	expect(status, 200)
	status, result = request(bases[2], "GET", "/wagering/transactions/"+pendingTransaction, providerToken, "", nil)
	expect(status, 200)
	if result["status"] != "PROCESSED" {
		t.Fatal("pending reversal not resolved")
	}
	// Same debit cannot be returned twice through another type.
	second := bet(uuid.NewString(), "2.00")
	second["kind"] = "ROLLBACK"
	second["referenceExternalTransactionId"] = referenceID
	status, result = request(bases[2], "POST", "/wagering/transactions", providerToken, second["externalTransactionId"].(string), second)
	expect(status, 422)
	if result["failureCode"] != "ALREADY_REVERSED" {
		t.Fatal("duplicate reversal not rejected")
	}
	badID := uuid.NewString()
	badBody := `{"messageId":"` + badID + `","type":"InvalidTestEnvelope"}`
	_, err = sqsClient.SendMessage(context.Background(), &awssqs.SendMessageInput{QueueUrl: &queue, MessageBody: &badBody, MessageGroupId: &badID, MessageDeduplicationId: &badID})
	if err != nil {
		t.Fatal(err)
	}
	dlqURL := "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions-dlq.fifo"
	dlqDeadline := time.NewTimer(150 * time.Second)
	defer dlqDeadline.Stop()
	dlqPoll := time.NewTicker(100 * time.Millisecond)
	defer dlqPoll.Stop()
	found := false
	for !found {
		output, err := sqsClient.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{QueueUrl: &dlqURL, MaxNumberOfMessages: 10, VisibilityTimeout: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range output.Messages {
			if message.Body != nil && *message.Body == badBody {
				found = true
				if _, err := sqsClient.DeleteMessage(context.Background(), &awssqs.DeleteMessageInput{QueueUrl: &dlqURL, ReceiptHandle: message.ReceiptHandle}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if found {
			break
		}
		select {
		case <-dlqPoll.C:
		case <-dlqDeadline.C:
			t.Fatal("invalid message did not reach real DLQ")
		}
	}

}

func startTestInstance(t *testing.T, base string) (string, func()) {
	t.Helper()
	address := ""
	if base != "" {
		address = strings.TrimPrefix(base, "http://")
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		_ = listener.Close()
		base = "http://" + address
	}
	child := exec.Command(os.Args[0], "-test.run=^TestInstanceHelper$")
	child.Env = append(os.Environ(), "JUNGLE_TEST_INSTANCE=1", "DATABASE_URL="+testsupport.DatabaseURL(), "OIDC_ISSUER_URL=http://localhost:8081/realms/jungle-wallet", "HTTP_ADDRESS="+address)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = child.Process.Signal(syscall.SIGTERM)
			if err := child.Wait(); err != nil {
				t.Errorf("instance shutdown: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	ready := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "INSTANCE_READY" {
				close(ready)
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("instance startup timeout")
	}
	return base, stop
}
