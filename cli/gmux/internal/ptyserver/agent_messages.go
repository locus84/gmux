package ptyserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gmuxapp/gmux/packages/adapter"
)

const (
	maxAgentMessageBytes = 1 << 20
	maxAgentResultBytes  = 256 << 10
	maxAgentRecords      = 32
	agentDispatchLease   = 5 * time.Second
)

type agentMessageState string

const (
	agentMessageQueued    agentMessageState = "queued"
	agentMessageDispatch  agentMessageState = "dispatching"
	agentMessageDelivered agentMessageState = "delivered"
	agentMessageRunning   agentMessageState = "running"
	agentMessageSettled   agentMessageState = "settled"
	agentMessageFailed    agentMessageState = "failed"
	agentMessageReplaced  agentMessageState = "replaced"
	agentMessageInDoubt   agentMessageState = "in_doubt"
)

type agentMessageRecord struct {
	RequestID    string            `json:"request_id"`
	RunnerEpoch  string            `json:"runner_epoch"`
	RuntimeEpoch string            `json:"runtime_epoch,omitempty"`
	Sequence     uint64            `json:"sequence"`
	State        agentMessageState `json:"state"`
	Outcome      string            `json:"outcome,omitempty"`
	Result       string            `json:"result,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
	Error        string            `json:"error,omitempty"`
	Text         string            `json:"-"`
	createdAt    time.Time
	dispatchedAt time.Time
}

type agentMessageDelivery struct {
	RequestID    string `json:"request_id"`
	RunnerEpoch  string `json:"runner_epoch"`
	RuntimeEpoch string `json:"runtime_epoch"`
	Sequence     uint64 `json:"sequence"`
	Text         string `json:"text"`
}

type agentMessageBroker struct {
	mu           sync.Mutex
	runnerEpoch  string
	runtimeEpoch string
	nextSequence uint64
	latest       string
	records      map[string]*agentMessageRecord
	order        []string
	notify       chan struct{}
	closed       bool
}

func newAgentMessageBroker() *agentMessageBroker {
	return &agentMessageBroker{
		runnerEpoch: randomEpoch(),
		records:     make(map[string]*agentMessageRecord),
		notify:      make(chan struct{}),
	}
}

func randomEpoch() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

func (b *agentMessageBroker) signalLocked() {
	close(b.notify)
	b.notify = make(chan struct{})
}

func terminalAgentMessageState(state agentMessageState) bool {
	switch state {
	case agentMessageSettled, agentMessageFailed, agentMessageReplaced, agentMessageInDoubt:
		return true
	default:
		return false
	}
}

var (
	errAgentMessageConflict = errors.New("request id already used with different text")
	errAgentMessageFull     = errors.New("semantic message broker is full")
	errAgentRuntimeStale    = errors.New("pi runtime was replaced")
)

func (b *agentMessageBroker) enqueue(requestID, text string) (agentMessageRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing := b.records[requestID]; existing != nil {
		if existing.Text != text {
			return agentMessageRecord{}, errAgentMessageConflict
		}
		return *existing, nil
	}
	b.trimLocked(maxAgentRecords - 1)
	if len(b.records) >= maxAgentRecords {
		return agentMessageRecord{}, errAgentMessageFull
	}
	b.nextSequence++
	record := &agentMessageRecord{
		RequestID:   requestID,
		RunnerEpoch: b.runnerEpoch,
		Sequence:    b.nextSequence,
		State:       agentMessageQueued,
		Text:        text,
		createdAt:   time.Now(),
	}
	b.records[requestID] = record
	b.order = append(b.order, requestID)
	b.latest = requestID
	b.signalLocked()
	return *record, nil
}

func (b *agentMessageBroker) bindRuntime(epoch string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if epoch == "" || epoch == b.runtimeEpoch {
		return
	}
	if b.runtimeEpoch != "" {
		hadActive := false
		for _, id := range b.order {
			record := b.records[id]
			if record == nil || terminalAgentMessageState(record.State) {
				continue
			}
			hadActive = true
			if record.State == agentMessageQueued {
				record.State = agentMessageReplaced
			} else {
				record.State = agentMessageInDoubt
			}
			record.Error = "pi runtime was replaced"
		}
		if !hadActive {
			latest := b.records[b.latest]
			if latest == nil || (latest.State != agentMessageReplaced && latest.State != agentMessageInDoubt) {
				b.latest = ""
			}
		}
	}
	b.runtimeEpoch = epoch
	b.signalLocked()
}

func (b *agentMessageBroker) unbindRuntime(epoch string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if epoch == "" || epoch != b.runtimeEpoch {
		return
	}
	for _, id := range b.order {
		record := b.records[id]
		if record == nil || terminalAgentMessageState(record.State) {
			continue
		}
		if record.State == agentMessageQueued {
			record.State = agentMessageReplaced
		} else {
			record.State = agentMessageInDoubt
		}
		record.Error = "pi runtime shut down"
	}
	b.runtimeEpoch = ""
	b.signalLocked()
}

func (b *agentMessageBroker) next(ctx context.Context, epoch string) (agentMessageDelivery, error) {
	for {
		b.mu.Lock()
		if b.closed || epoch == "" || epoch != b.runtimeEpoch {
			b.mu.Unlock()
			return agentMessageDelivery{}, errAgentRuntimeStale
		}
		now := time.Now()
		for _, id := range b.order {
			record := b.records[id]
			if record == nil {
				continue
			}
			if record.State == agentMessageDispatch && now.Sub(record.dispatchedAt) >= agentDispatchLease {
				record.State = agentMessageQueued
			}
			if record.State != agentMessageQueued {
				continue
			}
			record.State = agentMessageDispatch
			record.RuntimeEpoch = epoch
			record.dispatchedAt = now
			delivery := agentMessageDelivery{
				RequestID: record.RequestID, RunnerEpoch: record.RunnerEpoch,
				RuntimeEpoch: epoch, Sequence: record.Sequence, Text: record.Text,
			}
			b.mu.Unlock()
			return delivery, nil
		}
		notify := b.notify
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return agentMessageDelivery{}, ctx.Err()
		case <-notify:
		}
	}
}

func (b *agentMessageBroker) update(epoch, requestID string, state agentMessageState, outcome, result, eventErr string, truncated bool) (agentMessageRecord, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if epoch == "" || epoch != b.runtimeEpoch {
		return agentMessageRecord{}, errAgentRuntimeStale
	}
	record := b.records[requestID]
	if record == nil || record.RuntimeEpoch != epoch {
		return agentMessageRecord{}, errors.New("unknown message request")
	}
	if terminalAgentMessageState(record.State) {
		return *record, nil
	}
	switch state {
	case agentMessageDelivered, agentMessageRunning, agentMessageSettled, agentMessageFailed:
	default:
		return agentMessageRecord{}, errors.New("invalid message state")
	}
	if state != agentMessageFailed && agentMessageStateRank(state) < agentMessageStateRank(record.State) {
		return *record, nil
	}
	if len(result) > maxAgentResultBytes {
		result = result[:maxAgentResultBytes]
		for !utf8.ValidString(result) {
			result = result[:len(result)-1]
		}
		record.Truncated = true
	}
	record.State = state
	record.Outcome = outcome
	record.Result = result
	record.Error = eventErr
	record.Truncated = record.Truncated || truncated
	b.signalLocked()
	return *record, nil
}

func agentMessageStateRank(state agentMessageState) int {
	switch state {
	case agentMessageQueued:
		return 0
	case agentMessageDispatch:
		return 1
	case agentMessageDelivered:
		return 2
	case agentMessageRunning:
		return 3
	case agentMessageSettled, agentMessageFailed, agentMessageReplaced, agentMessageInDoubt:
		return 4
	default:
		return -1
	}
}

func (b *agentMessageBroker) beginTurn() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, record := range b.records {
		if !terminalAgentMessageState(record.State) {
			return
		}
	}
	b.latest = ""
}

func (b *agentMessageBroker) get(requestID string) (agentMessageRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if requestID == "" {
		requestID = b.latest
	}
	record := b.records[requestID]
	if record == nil {
		return agentMessageRecord{}, false
	}
	return *record, true
}

func (b *agentMessageBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, record := range b.records {
		if !terminalAgentMessageState(record.State) {
			record.State = agentMessageInDoubt
			record.Error = "runner exited"
		}
	}
	b.signalLocked()
}

func (b *agentMessageBroker) trimLocked(limit int) {
	if len(b.records) <= limit {
		return
	}
	kept := b.order[:0]
	for _, id := range b.order {
		record := b.records[id]
		if len(b.records) > limit && record != nil && terminalAgentMessageState(record.State) {
			delete(b.records, id)
			continue
		}
		kept = append(kept, id)
	}
	b.order = kept
}

type enqueueAgentMessageRequest struct {
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
}

type agentMessageEventRequest struct {
	RuntimeEpoch string            `json:"runtime_epoch"`
	RequestID    string            `json:"request_id"`
	State        agentMessageState `json:"state"`
	Outcome      string            `json:"outcome,omitempty"`
	Result       string            `json:"result,omitempty"`
	Error        string            `json:"error,omitempty"`
	Truncated    bool              `json:"truncated,omitempty"`
}

func (s *Server) handleAgentMessage(w http.ResponseWriter, r *http.Request) {
	if s.state.Kind != "pi" {
		http.Error(w, "semantic messages are only supported for pi sessions", http.StatusNotImplemented)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req enqueueAgentMessageRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentMessageBytes*6+4096))
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.RequestID = strings.TrimSpace(req.RequestID)
		if req.RequestID == "" || len(req.RequestID) > 128 || req.Text == "" || len(req.Text) > maxAgentMessageBytes {
			http.Error(w, "request_id and bounded non-empty text are required", http.StatusBadRequest)
			return
		}
		record, err := s.agentMessages.enqueue(req.RequestID, req.Text)
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, errAgentMessageFull) {
				status = http.StatusTooManyRequests
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeAgentMessageJSON(w, http.StatusAccepted, record)
	case http.MethodGet:
		record, ok := s.agentMessages.get(strings.TrimSpace(r.URL.Query().Get("request_id")))
		if !ok {
			http.Error(w, "message request not found", http.StatusNotFound)
			return
		}
		writeAgentMessageJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentMessagesNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	epoch := strings.TrimSpace(r.URL.Query().Get("runtime_epoch"))
	delivery, err := s.agentMessages.next(r.Context(), epoch)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(delivery)
}

func (s *Server) handleAgentMessageEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req agentMessageEventRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentResultBytes*6+4096)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	record, err := s.agentMessages.update(req.RuntimeEpoch, req.RequestID, req.State, req.Outcome, req.Result, req.Error, req.Truncated)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if req.State == agentMessageRunning {
		s.state.SetStatus(&adapter.Status{Working: true})
	}
	writeAgentMessageJSON(w, http.StatusOK, record)
}

func writeAgentMessageJSON(w http.ResponseWriter, status int, record agentMessageRecord) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(record)
}
