package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jackc/pgx/v5"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/auth"
	"jungle-wallet/internal/infrastructure/postgres"
	queues "jungle-wallet/internal/infrastructure/sqs"
	"jungle-wallet/internal/observability"
)

type QueryHandler struct {
	db  *postgres.DB
	sqs *queues.ClientDependencies
}

func NewQueryHandler(db *postgres.DB, sqs *queues.ClientDependencies) *QueryHandler {
	return &QueryHandler{db: db, sqs: sqs}
}
func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func queryError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrWalletNotFound) || errors.Is(err, postgres.ErrWagerTransactionNotFound) {
		writeJSONError(w, 404, "not found")
		return
	}
	writeJSONError(w, 503, "dependency unavailable")
}
func readWallet(ctx context.Context, q postgres.Querier, id string) (domain.WalletState, error) {
	var s domain.WalletState
	var units int64
	var currency string
	err := q.QueryRow(ctx, `SELECT id,player_id,currency,balance_units,version,created_at,updated_at FROM wallets WHERE id=$1`, id).Scan(&s.ID, &s.PlayerID, &currency, &units, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	s.Balance, err = domain.FromUnits(units, currency)
	return s, err
}
func (h *QueryHandler) Wallet(w http.ResponseWriter, r *http.Request) {
	s, err := readWallet(r.Context(), h.db.Pool, r.PathValue("walletId"))
	if err != nil {
		queryError(w, err)
		return
	}
	sendJSON(w, 200, s)
}

type ledgerCursor struct {
	Time     time.Time `json:"time"`
	ID       string    `json:"id"`
	WalletID string    `json:"walletId"`
}

func (h *QueryHandler) Ledger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("walletId")
	if _, err := readWallet(r.Context(), h.db.Pool, id); err != nil {
		queryError(w, err)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, 400, "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	c := ledgerCursor{Time: time.Unix(0, 0), WalletID: id}
	if v := r.URL.Query().Get("cursor"); v != "" {
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.ID == "" || c.Time.IsZero() || c.WalletID != id {
			writeJSONError(w, 400, "invalid cursor")
			return
		}
	}
	rows, err := h.db.Pool.Query(r.Context(), `SELECT id,transaction_id,direction,amount_units,currency,balance_before_units,balance_after_units,created_at FROM wallet_ledger_entries WHERE wallet_id=$1 AND (created_at,id)>($2,$3) ORDER BY created_at,id LIMIT $4`, id, c.Time, c.ID, limit+1)
	if err != nil {
		queryError(w, err)
		return
	}
	defer rows.Close()
	entries := make([]domain.LedgerState, 0, limit+1)
	for rows.Next() {
		var e domain.LedgerState
		var amount, before, after int64
		var currency string
		e.WalletID = id
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.Direction, &amount, &currency, &before, &after, &e.CreatedAt); err != nil {
			queryError(w, err)
			return
		}
		e.Money, err = domain.FromUnits(amount, currency)
		if err != nil {
			queryError(w, err)
			return
		}
		e.BalanceBefore, _ = domain.FromUnits(before, currency)
		e.BalanceAfter, _ = domain.FromUnits(after, currency)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		queryError(w, err)
		return
	}
	next := ""
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1]
		raw, _ := json.Marshal(ledgerCursor{Time: last.CreatedAt, ID: last.ID, WalletID: id})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	sendJSON(w, 200, struct {
		Entries    []domain.LedgerState `json:"entries"`
		NextCursor string               `json:"nextCursor,omitempty"`
	}{entries, next})
}
func (h *QueryHandler) Transaction(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, 401, "unauthorized")
		return
	}
	provider := r.PathValue("providerId")
	if provider != "" && provider != claims.ClientID {
		writeJSONError(w, 403, "provider is not authorized")
		return
	}
	query := `SELECT id,external_transaction_id,provider_id,wallet_id,player_id,round_id,game_id,kind,amount_units,currency,status,COALESCE(failure_code,''),result_balance_units,COALESCE(reference_external_transaction_id,''),COALESCE(reference_transaction_id,''),created_at,updated_at FROM wager_transactions WHERE provider_id=$1 AND id=$2`
	identity := r.PathValue("transactionId")
	if provider != "" {
		query = `SELECT id,external_transaction_id,provider_id,wallet_id,player_id,round_id,game_id,kind,amount_units,currency,status,COALESCE(failure_code,''),result_balance_units,COALESCE(reference_external_transaction_id,''),COALESCE(reference_transaction_id,''),created_at,updated_at FROM wager_transactions WHERE provider_id=$1 AND external_transaction_id=$2`
		identity = r.PathValue("externalTransactionId")
	}
	var v struct {
		ID                  string        `json:"transactionId"`
		ExternalID          string        `json:"externalTransactionId"`
		ProviderID          string        `json:"providerId"`
		WalletID            string        `json:"walletId"`
		PlayerID            string        `json:"playerId"`
		RoundID             string        `json:"roundId"`
		GameID              string        `json:"gameId"`
		Kind                string        `json:"kind"`
		Money               domain.Money  `json:"money"`
		Status              string        `json:"status"`
		FailureCode         string        `json:"failureCode,omitempty"`
		Balance             *domain.Money `json:"balance,omitempty"`
		ReferenceExternalID string        `json:"referenceExternalTransactionId,omitempty"`
		ReferenceID         string        `json:"referenceTransactionId,omitempty"`
		CreatedAt           time.Time     `json:"createdAt"`
		UpdatedAt           time.Time     `json:"updatedAt"`
	}
	var units int64
	var balance *int64
	var currency string
	err := h.db.Pool.QueryRow(r.Context(), query, claims.ClientID, identity).Scan(&v.ID, &v.ExternalID, &v.ProviderID, &v.WalletID, &v.PlayerID, &v.RoundID, &v.GameID, &v.Kind, &units, &currency, &v.Status, &v.FailureCode, &balance, &v.ReferenceExternalID, &v.ReferenceID, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		queryError(w, err)
		return
	}
	v.Money, _ = domain.FromUnits(units, currency)
	if balance != nil {
		m, _ := domain.FromUnits(*balance, currency)
		v.Balance = &m
	}
	sendJSON(w, 200, v)
}

var reconciliationDivergences atomic.Uint64

func (h *QueryHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := h.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		queryError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	s, err := readWallet(ctx, tx, r.PathValue("walletId"))
	if err != nil {
		queryError(w, err)
		return
	}
	var text string
	var count int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE direction WHEN 'CREDIT' THEN amount_units::numeric ELSE -amount_units::numeric END),0)::text, COUNT(*) FROM wallet_ledger_entries WHERE wallet_id=$1`, s.ID).Scan(&text, &count)
	if err != nil {
		queryError(w, err)
		return
	}
	units, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		writeJSONError(w, 500, "ledger total overflow")
		return
	}
	calculated, _ := domain.FromUnits(units, s.Balance.Currency())
	difference, err := s.Balance.Sub(calculated)
	if err != nil {
		writeJSONError(w, 500, "difference overflow")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		queryError(w, err)
		return
	}
	consistent := difference.Units() == 0
	if !consistent {
		reconciliationDivergences.Add(1)
		slog.Error("reconciliation divergence", "walletId", s.ID)
	}
	sendJSON(w, 200, struct {
		WalletID          string       `json:"walletId"`
		StoredBalance     domain.Money `json:"storedBalance"`
		CalculatedBalance domain.Money `json:"calculatedBalance"`
		Difference        domain.Money `json:"difference"`
		Consistent        bool         `json:"consistent"`
		CheckedEntries    int64        `json:"checkedEntries"`
	}{s.ID, s.Balance, calculated, difference, consistent, count})
}
func (h *QueryHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Pool.Ping(ctx); err != nil {
		queryError(w, err)
		return
	}
	for _, url := range []string{h.sqs.QueueURL, h.sqs.OutboxQueueURL} {
		if _, err := h.sqs.Client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: &url}); err != nil {
			queryError(w, err)
			return
		}
	}
	sendJSON(w, 200, map[string]string{"status": "ready"})
}
func (h *QueryHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "jungle_wallet_reconciliation_divergences_total %d\n", reconciliationDivergences.Load())
	observability.Write(w)
	h.writeDLQMetrics(w, r.Context())
	var lag float64
	if err := h.db.Pool.QueryRow(r.Context(), "SELECT COALESCE(EXTRACT(EPOCH FROM (NOW()-MIN(created_at))),0)::double precision FROM outbox WHERE published_at IS NULL").Scan(&lag); err == nil {
		if lag < 0 {
			lag = 0
		}
		_, _ = fmt.Fprintf(w, "jungle_wallet_outbox_lag_seconds %g\n", lag)
	}
}

// Read the actual redrive destination rather than treating retry candidates as DLQ arrivals.
func (h *QueryHandler) writeDLQMetrics(w http.ResponseWriter, ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	attrs, err := h.sqs.Client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: &h.sqs.QueueURL, AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameRedrivePolicy}})
	if err != nil {
		return
	}
	var policy struct {
		ARN string `json:"deadLetterTargetArn"`
	}
	if json.Unmarshal([]byte(attrs.Attributes["RedrivePolicy"]), &policy) != nil {
		return
	}
	parts := strings.Split(policy.ARN, ":")
	if len(parts) != 6 {
		return
	}
	queue, err := h.sqs.Client.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: &parts[5], QueueOwnerAWSAccountId: &parts[4]})
	if err != nil {
		return
	}
	depth, err := h.sqs.Client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: queue.QueueUrl, AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages, types.QueueAttributeNameApproximateNumberOfMessagesNotVisible}})
	if err != nil {
		return
	}
	for attr, name := range map[string]string{"ApproximateNumberOfMessages": "visible", "ApproximateNumberOfMessagesNotVisible": "inflight"} {
		count, err := strconv.ParseUint(depth.Attributes[attr], 10, 64)
		if err == nil {
			_, _ = fmt.Fprintf(w, "jungle_wallet_dlq_%s_messages %d\n", name, count)
		}
	}
}
