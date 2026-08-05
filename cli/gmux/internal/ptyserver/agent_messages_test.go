package ptyserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gmuxapp/gmux/cli/gmux/internal/session"
)

func TestAgentMessageBrokerLifecycle(t *testing.T) {
	broker := newAgentMessageBroker()
	record, err := broker.enqueue("req-1", "do the work")
	if err != nil || record.State != agentMessageQueued {
		t.Fatalf("enqueue record=%+v err=%v", record, err)
	}
	broker.bindRuntime("runtime-1")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	delivery, err := broker.next(ctx, "runtime-1")
	if err != nil || delivery.RequestID != "req-1" || delivery.Text != "do the work" {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	if _, err := broker.update("runtime-1", "req-1", agentMessageDelivered, "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.update("runtime-1", "req-1", agentMessageRunning, "", "", "", false); err != nil {
		t.Fatal(err)
	}
	settled, err := broker.update("runtime-1", "req-1", agentMessageSettled, "completed", "final answer", "", false)
	if err != nil || settled.Result != "final answer" || settled.Outcome != "completed" {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
	latest, ok := broker.get("")
	if !ok || latest.RequestID != "req-1" || latest.State != agentMessageSettled {
		t.Fatalf("latest=%+v ok=%v", latest, ok)
	}
}

func TestAgentMessageBrokerDeduplicatesAndQueuesConcurrentSteering(t *testing.T) {
	broker := newAgentMessageBroker()
	first, err := broker.enqueue("same", "text")
	if err != nil {
		t.Fatal(err)
	}
	again, err := broker.enqueue("same", "text")
	if err != nil || again.Sequence != first.Sequence {
		t.Fatalf("dedup=%+v err=%v", again, err)
	}
	if _, err := broker.enqueue("same", "different"); !errors.Is(err, errAgentMessageConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	other, err := broker.enqueue("other", "text")
	if err != nil || other.Sequence != first.Sequence+1 {
		t.Fatalf("queued steering=%+v err=%v", other, err)
	}
}

func TestAgentMessageBrokerBoundsActiveRequests(t *testing.T) {
	broker := newAgentMessageBroker()
	for i := 0; i < maxAgentRecords; i++ {
		id := fmt.Sprintf("req-%d", i)
		if _, err := broker.enqueue(id, "text"); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := broker.enqueue("overflow", "text"); !errors.Is(err, errAgentMessageFull) {
		t.Fatalf("overflow err=%v", err)
	}
}

func TestAgentMessageBrokerFencesRuntimeReplacement(t *testing.T) {
	broker := newAgentMessageBroker()
	broker.bindRuntime("old")
	if _, err := broker.enqueue("queued", "text"); err != nil {
		t.Fatal(err)
	}
	broker.bindRuntime("new")
	record, _ := broker.get("queued")
	if record.State != agentMessageReplaced {
		t.Fatalf("queued replacement state=%q", record.State)
	}

	broker = newAgentMessageBroker()
	broker.bindRuntime("old")
	if _, err := broker.enqueue("sent", "text"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := broker.next(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	broker.bindRuntime("new")
	record, _ = broker.get("sent")
	if record.State != agentMessageInDoubt {
		t.Fatalf("dispatched replacement state=%q", record.State)
	}
	if _, err := broker.update("old", "sent", agentMessageSettled, "completed", "late", "", false); !errors.Is(err, errAgentRuntimeStale) {
		t.Fatalf("stale update err=%v", err)
	}
}

func TestAgentMessageHandlersExposeOnlyPiRuntimeBroker(t *testing.T) {
	server := &Server{state: session.New(session.Config{ID: "sess-pi", Kind: "pi"}), agentMessages: newAgentMessageBroker()}
	server.agentMessages.bindRuntime("runtime")

	req := httptest.NewRequest(http.MethodPost, "/agent/message", strings.NewReader(`{"request_id":"req","text":"hello"}`))
	w := httptest.NewRecorder()
	server.handleAgentMessage(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("enqueue status=%d body=%s", w.Code, w.Body.String())
	}
	var enqueued agentMessageRecord
	if err := json.NewDecoder(w.Body).Decode(&enqueued); err != nil || enqueued.State != agentMessageQueued {
		t.Fatalf("enqueued=%+v err=%v", enqueued, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := server.agentMessages.next(ctx, "runtime"); err != nil {
		t.Fatal(err)
	}
	event := httptest.NewRequest(http.MethodPost, "/hook/message-event", strings.NewReader(`{"runtime_epoch":"runtime","request_id":"req","state":"running"}`))
	ew := httptest.NewRecorder()
	server.handleAgentMessageEvent(ew, event)
	if ew.Code != http.StatusOK || server.state.StatusSnapshot() == nil || !server.state.StatusSnapshot().Working {
		t.Fatalf("event status=%d session=%+v body=%s", ew.Code, server.state.StatusSnapshot(), ew.Body.String())
	}
}

func TestAgentMessageBrokerCloseMarksActiveInDoubt(t *testing.T) {
	broker := newAgentMessageBroker()
	if _, err := broker.enqueue("req", "text"); err != nil {
		t.Fatal(err)
	}
	broker.close()
	record, _ := broker.get("req")
	if record.State != agentMessageInDoubt || record.Error != "runner exited" {
		t.Fatalf("closed record=%+v", record)
	}
}
