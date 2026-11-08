package ai_sync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	synccli "catendar-wails/core/ai_sync/cli"
	"catendar-wails/core/calendar"
	"catendar-wails/core/email"
)

func TestCalendarListOmitsManualResultsFromSchedulerWire(t *testing.T) {
	encoded, err := json.Marshal(withoutManualResults([]calendar.Event{{
		ID:     1,
		Title:  "Interview",
		Result: "手动记录\n不要发给调度器",
	}}))
	if err != nil {
		t.Fatalf("encode events: %v", err)
	}
	if strings.Contains(string(encoded), "result") {
		t.Fatalf("scheduler calendar list leaked a manual result: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"title":"Interview"`) {
		t.Fatalf("scheduler calendar list lost event fields: %s", encoded)
	}
}

func TestCalendarBatchMayOnlyBeSubmittedOnce(t *testing.T) {
	service := &Service{calendar: calendar.NewService(nil)}

	payload, err := json.Marshal(synccli.CalendarBatchRequest{Events: []calendar.EmailEventInput{}})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	request := synccli.Request{Command: "calendar.batch-upsert", Payload: payload}

	stateValue := &runState{available: map[string]email.MessageSummary{}}
	if _, err := service.handleCLIRequest(context.Background(), stateValue, request); err != nil {
		t.Fatalf("first batch failed: %v", err)
	}
	if _, err := service.handleCLIRequest(context.Background(), stateValue, request); err == nil {
		t.Fatal("second calendar batch was accepted")
	}
}
