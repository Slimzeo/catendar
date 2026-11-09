package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"catendar-wails/core/calendar"
)

const (
	dateLayout = "2006-01-02"

	maxPlanChanges      = 20
	maxChangesPerThread = 6
	maxTitleRunes       = 200
	maxDescriptionRunes = 2000
	maxResultRunes      = 2000
	minQuoteRunes       = 4
	maxQuoteRunes       = 500
	maxTaskKeyRunes     = 80

	maxPlannerPromptBytes = 2 * 1024 * 1024
	maxPlannerOutputBytes = 1 * 1024 * 1024
)

var (
	// ErrPlannerUnavailable means the local Codex CLI could not run the plan.
	ErrPlannerUnavailable = errors.New("codex conversation planner is unavailable")
	// ErrPlannerOutput means the CLI returned output that does not satisfy the
	// planner contract. Callers must not checkpoint source ids after this error.
	ErrPlannerOutput = errors.New("codex conversation planner returned invalid output")
)

const plannerSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["changes"],
  "properties": {
    "changes": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": [
          "sourceId",
          "quote",
          "taskKey",
          "eventId",
          "workspaceId",
          "title",
          "phase",
          "description",
          "result",
          "needsReview"
        ],
        "properties": {
          "sourceId": {"type": "string"},
          "quote": {"type": "string"},
          "taskKey": {"type": "string"},
          "eventId": {"type": "integer"},
          "workspaceId": {"type": "integer"},
          "title": {"type": "string"},
          "phase": {"type": "string", "enum": ["todo", "progressing", "done"]},
          "description": {"type": "string"},
          "result": {"type": "string"},
          "needsReview": {"type": "boolean"}
        }
      }
    }
  }
}`

type modelChange struct {
	SourceID    *string `json:"sourceId"`
	Quote       *string `json:"quote"`
	TaskKey     *string `json:"taskKey"`
	EventID     *int64  `json:"eventId"`
	WorkspaceID *int64  `json:"workspaceId"`
	Title       *string `json:"title"`
	Phase       *string `json:"phase"`
	Description *string `json:"description"`
	Result      *string `json:"result"`
	NeedsReview *bool   `json:"needsReview"`
}

type modelOutput struct {
	Changes *[]modelChange `json:"changes"`
}

// Plan turns bounded public conversation messages into validated calendar
// changes. The model never writes to catendar; callers apply the returned
// changes and checkpoint source ids only after a successful apply.
func Plan(
	ctx context.Context,
	binary, day string,
	workspaces []calendar.Workspace,
	events []calendar.Event,
	messages []Message,
) ([]calendar.ChatChange, error) {
	parsedDay, err := time.Parse(dateLayout, day)
	if err != nil || parsedDay.Format(dateLayout) != day {
		return nil, errors.New("conversation plan day must use YYYY-MM-DD")
	}
	if strings.TrimSpace(binary) == "" {
		return nil, fmt.Errorf("%w: Codex binary is not configured", ErrPlannerUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return []calendar.ChatChange{}, nil
	}
	if len(workspaces) == 0 {
		return nil, errors.New("conversation planner has no workspaces")
	}

	messageByID, err := indexMessages(messages)
	if err != nil {
		return nil, err
	}
	workspaceIDs, err := indexWorkspaces(workspaces)
	if err != nil {
		return nil, err
	}
	eventsByID, err := indexEvents(events)
	if err != nil {
		return nil, err
	}

	prompt, err := buildPrompt(day, workspaces, events, messages)
	if err != nil {
		return nil, err
	}
	if len(prompt) > maxPlannerPromptBytes {
		return nil, errors.New("conversation batch exceeds the planner input limit")
	}
	raw, err := runPlanner(ctx, binary, prompt)
	if err != nil {
		return nil, err
	}
	proposals, err := decodePlannerOutput(raw)
	if err != nil {
		return nil, err
	}
	changes, err := validateProposals(proposals, messageByID, workspaceIDs, eventsByID, day)
	if err != nil {
		return nil, err
	}
	if len(proposals) > 0 && len(changes) == 0 {
		return nil, fmt.Errorf("%w: no proposal had verifiable evidence and a valid target", ErrPlannerOutput)
	}
	return changes, nil
}

func indexMessages(messages []Message) (map[string]Message, error) {
	byID := make(map[string]Message, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.ID) == "" ||
			strings.TrimSpace(message.ThreadID) == "" ||
			strings.TrimSpace(message.Text) == "" ||
			(message.Role != RoleUser && message.Role != RoleAssistant) ||
			message.At <= 0 {
			return nil, fmt.Errorf("conversation message %d is invalid", index+1)
		}
		if _, exists := byID[message.ID]; exists {
			return nil, errors.New("conversation message ids must be unique")
		}
		byID[message.ID] = message
	}
	return byID, nil
}

func indexWorkspaces(workspaces []calendar.Workspace) (map[int64]struct{}, error) {
	byID := make(map[int64]struct{}, len(workspaces))
	for index, workspace := range workspaces {
		if workspace.ID < 0 {
			return nil, fmt.Errorf("workspace %d has an invalid id", index+1)
		}
		if _, exists := byID[workspace.ID]; exists {
			return nil, errors.New("workspace ids must be unique")
		}
		byID[workspace.ID] = struct{}{}
	}
	return byID, nil
}

func indexEvents(events []calendar.Event) (map[int64]calendar.Event, error) {
	byID := make(map[int64]calendar.Event, len(events))
	for index, event := range events {
		if event.ID <= 0 {
			return nil, fmt.Errorf("calendar event %d has an invalid id", index+1)
		}
		if _, exists := byID[event.ID]; exists {
			return nil, errors.New("calendar event ids must be unique")
		}
		byID[event.ID] = event
	}
	return byID, nil
}

func runPlanner(ctx context.Context, binary, prompt string) ([]byte, error) {
	directory, err := os.MkdirTemp("", "catendar-conversation-plan-")
	if err != nil {
		return nil, fmt.Errorf("%w: could not create a private directory", ErrPlannerUnavailable)
	}
	defer os.RemoveAll(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("%w: could not secure the private directory", ErrPlannerUnavailable)
	}

	schemaPath := filepath.Join(directory, "schema.json")
	resultPath := filepath.Join(directory, "result.json")
	if err := os.WriteFile(schemaPath, []byte(plannerSchema), 0o600); err != nil {
		return nil, fmt.Errorf("%w: could not write the output schema", ErrPlannerUnavailable)
	}

	command := exec.CommandContext(ctx, binary,
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--disable", "shell_tool", "--disable", "unified_exec", "--disable", "multi_agent",
		"--disable", "hooks", "--disable", "plugins", "--disable", "apps",
		"-c", `web_search="disabled"`, "-c", `model_reasoning_effort="low"`,
		"--color", "never",
		"-c", `approval_policy="never"`,
		"-C", directory,
		"--output-schema", schemaPath,
		"-o", resultPath,
		"-",
	)
	stderr := &tailBuffer{limit: 8 * 1024}
	command.Dir = directory
	command.Stdin = strings.NewReader(prompt)
	command.Stdout = io.Discard
	command.Stderr = stderr
	command.WaitDelay = 10 * time.Second
	configureProcess(command)

	waitErr := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if waitErr != nil {
		return nil, plannerFailure(classifyPlannerFailure(stderr.data))
	}

	info, err := os.Stat(resultPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPlannerOutputBytes {
		return nil, fmt.Errorf("%w: result file is missing or too large", ErrPlannerOutput)
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("%w: could not read result file", ErrPlannerOutput)
	}
	return raw, nil
}

type plannerFailureClass string

const (
	plannerFailureAuthentication plannerFailureClass = "authentication"
	plannerFailureQuota          plannerFailureClass = "quota"
	plannerFailureNetwork        plannerFailureClass = "network"
	plannerFailureUnknownFlag    plannerFailureClass = "unknown flag"
	plannerFailureUnknown        plannerFailureClass = "unknown"
)

// classifyPlannerFailure turns noisy CLI stderr into a small sanitized class.
// Raw stderr must never cross this boundary because it can contain prompts,
// local paths, or credentials.
func classifyPlannerFailure(stderr []byte) plannerFailureClass {
	text := strings.ToLower(string(stderr))
	switch {
	case containsAny(text,
		"401", "unauthorized", "authentication", "not logged in", "login required",
		"invalid api key", "api key is invalid", "api key", "missing bearer", "auth token"):
		return plannerFailureAuthentication
	case containsAny(text,
		"429", "quota", "rate limit", "rate_limit", "ratelimit", "usage limit",
		"insufficient quota", "too many requests", "out of credits", "billing"):
		return plannerFailureQuota
	case containsAny(text,
		"network", "connection", "connect ", "dns", "resolve host", "timed out", "timeout",
		"tls", "certificate", "socket", "unreachable", "offline"):
		return plannerFailureNetwork
	case containsAny(text,
		"unknown flag", "unknown option", "unknown argument", "unknown shorthand flag",
		"unrecognized argument", "unrecognized flag", "unexpected argument", "unexpected flag",
		"invalid option", "invalid flag", "flag provided but not defined"):
		return plannerFailureUnknownFlag
	default:
		return plannerFailureUnknown
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func plannerFailure(class plannerFailureClass) error {
	switch class {
	case plannerFailureAuthentication:
		return fmt.Errorf("%w: Codex CLI authentication failed", ErrPlannerUnavailable)
	case plannerFailureQuota:
		return fmt.Errorf("%w: Codex CLI quota or rate limit exceeded", ErrPlannerUnavailable)
	case plannerFailureNetwork:
		return fmt.Errorf("%w: Codex CLI network request failed", ErrPlannerUnavailable)
	case plannerFailureUnknownFlag:
		return fmt.Errorf("%w: Codex CLI rejected an unknown flag", ErrPlannerUnavailable)
	default:
		return fmt.Errorf("%w: Codex CLI failed", ErrPlannerUnavailable)
	}
}

func decodePlannerOutput(raw []byte) ([]modelChange, error) {
	raw = stripJSONFence(bytes.TrimSpace(raw))
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: result is empty", ErrPlannerOutput)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var output modelOutput
	if err := decoder.Decode(&output); err != nil {
		return nil, fmt.Errorf("%w: result is not valid JSON", ErrPlannerOutput)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: result contains trailing data", ErrPlannerOutput)
	}
	if output.Changes == nil {
		return nil, fmt.Errorf("%w: changes is required", ErrPlannerOutput)
	}
	changes := *output.Changes
	if len(changes) > maxPlanChanges {
		return nil, fmt.Errorf("%w: too many proposals", ErrPlannerOutput)
	}
	return changes, nil
}

func stripJSONFence(value []byte) []byte {
	if !bytes.HasPrefix(value, []byte("```")) {
		return value
	}
	lineEnd := bytes.IndexByte(value, '\n')
	if lineEnd < 0 {
		return value
	}
	body := bytes.TrimSpace(value[lineEnd+1:])
	if !bytes.HasSuffix(body, []byte("```")) {
		return value
	}
	return bytes.TrimSpace(body[:len(body)-3])
}

func validateProposals(
	proposals []modelChange,
	messages map[string]Message,
	workspaces map[int64]struct{},
	events map[int64]calendar.Event,
	day string,
) ([]calendar.ChatChange, error) {
	changes := make([]calendar.ChatChange, 0, len(proposals))
	taskKeys := make(map[string]struct{}, len(proposals))
	eventIDs := make(map[int64]struct{}, len(proposals))
	perThread := make(map[string]int, len(proposals))

	messageValues := make([]Message, 0, len(messages))
	for _, message := range messages {
		messageValues = append(messageValues, message)
	}
	references := promptReferences(messageValues)

	for index, proposal := range proposals {
		sourceID, err := requiredString(proposal.SourceID, "sourceId", index)
		if err != nil {
			return nil, err
		}
		quoteValue, err := requiredString(proposal.Quote, "quote", index)
		if err != nil {
			return nil, err
		}
		taskKeyValue, err := requiredString(proposal.TaskKey, "taskKey", index)
		if err != nil {
			return nil, err
		}
		titleValue, err := requiredString(proposal.Title, "title", index)
		if err != nil {
			return nil, err
		}
		phaseValue, err := requiredString(proposal.Phase, "phase", index)
		if err != nil {
			return nil, err
		}
		descriptionValue, err := requiredString(proposal.Description, "description", index)
		if err != nil {
			return nil, err
		}
		resultValue, err := requiredString(proposal.Result, "result", index)
		if err != nil {
			return nil, err
		}
		if proposal.EventID == nil {
			return nil, missingField(index, "eventId")
		}
		if proposal.WorkspaceID == nil {
			return nil, missingField(index, "workspaceId")
		}
		if proposal.NeedsReview == nil {
			return nil, missingField(index, "needsReview")
		}
		eventID := *proposal.EventID
		workspaceID := *proposal.WorkspaceID
		if eventID < 0 || workspaceID < 0 {
			return nil, fmt.Errorf("%w: change %d has a negative id", ErrPlannerOutput, index+1)
		}

		phase := strings.ToLower(strings.TrimSpace(phaseValue))
		switch phase {
		case calendar.PhaseTodo, calendar.PhaseProgressing, calendar.PhaseDone:
		default:
			return nil, fmt.Errorf("%w: change %d has an invalid phase", ErrPlannerOutput, index+1)
		}
		title := strings.TrimSpace(titleValue)
		description := strings.TrimSpace(descriptionValue)
		result := strings.TrimSpace(resultValue)
		if utf8.RuneCountInString(title) > maxTitleRunes {
			return nil, fmt.Errorf("%w: change %d title is too long", ErrPlannerOutput, index+1)
		}
		if utf8.RuneCountInString(description) > maxDescriptionRunes {
			return nil, fmt.Errorf("%w: change %d description is too long", ErrPlannerOutput, index+1)
		}
		if utf8.RuneCountInString(result) > maxResultRunes {
			return nil, fmt.Errorf("%w: change %d result is too long", ErrPlannerOutput, index+1)
		}
		if result != "" && phase == calendar.PhaseTodo {
			return nil, fmt.Errorf("%w: change %d has a result before work started", ErrPlannerOutput, index+1)
		}
		taskKey := normalizeTaskKey(taskKeyValue)
		if taskKey == "" || utf8.RuneCountInString(taskKey) > maxTaskKeyRunes {
			return nil, fmt.Errorf("%w: change %d has an invalid taskKey", ErrPlannerOutput, index+1)
		}

		sourceKey := strings.TrimSpace(sourceID)
		if original, ok := references[sourceKey]; ok {
			sourceKey = original
		}
		message, ok := messages[sourceKey]
		if !ok {
			continue
		}
		if message.ContextOnly && eventID == 0 {
			// Context-only messages explain follow-ups but cannot justify a
			// brand-new task on their own.
			continue
		}
		quote, ok := evidenceQuote(message.Text, quoteValue)
		if !ok {
			continue
		}
		if workspaceID <= 0 {
			continue
		}
		if _, ok := workspaces[workspaceID]; !ok {
			continue
		}

		baseUpdated := ""
		existingResult := ""
		if eventID > 0 {
			event, ok := events[eventID]
			if !ok {
				continue
			}
			baseUpdated = event.UpdatedAt.Format(time.RFC3339Nano)
			existingResult = strings.TrimSpace(event.Result)
			if _, duplicate := eventIDs[eventID]; duplicate {
				return nil, fmt.Errorf("%w: change %d targets the same event twice", ErrPlannerOutput, index+1)
			}
		} else if phase == calendar.PhaseDone && dayString(message.At) != day {
			// A delayed scan must not turn yesterday's completed work into a
			// new done card on today's board.
			continue
		}
		if phase == calendar.PhaseDone && result == "" && existingResult == "" {
			return nil, fmt.Errorf("%w: change %d completes without a result", ErrPlannerOutput, index+1)
		}
		if title == "" {
			continue
		}
		if _, duplicate := taskKeys[taskKey]; duplicate {
			return nil, fmt.Errorf("%w: change %d repeats taskKey", ErrPlannerOutput, index+1)
		}
		if perThread[message.ThreadID] >= maxChangesPerThread {
			return nil, fmt.Errorf("%w: too many proposals for one conversation", ErrPlannerOutput)
		}
		taskKeys[taskKey] = struct{}{}
		if eventID > 0 {
			eventIDs[eventID] = struct{}{}
		}
		perThread[message.ThreadID]++

		changes = append(changes, calendar.ChatChange{
			Source: calendar.ChatEvidence{
				ID:          message.ID,
				ThreadID:    message.ThreadID,
				ThreadTitle: message.ThreadTitle,
				At:          message.At,
				Quote:       quote,
			},
			TaskKey:     taskKey,
			EventID:     eventID,
			WorkspaceID: workspaceID,
			Title:       title,
			Phase:       phase,
			Description: description,
			Result:      result,
			NeedsReview: *proposal.NeedsReview,
			BaseUpdated: baseUpdated,
		})
	}
	return changes, nil
}

func requiredString(value *string, name string, index int) (string, error) {
	if value == nil {
		return "", missingField(index, name)
	}
	return *value, nil
}

func missingField(index int, name string) error {
	return fmt.Errorf("%w: change %d is missing %s", ErrPlannerOutput, index+1, name)
}

func evidenceQuote(text, value string) (string, bool) {
	if strings.Contains(text, value) && strings.TrimSpace(value) != "" {
		if utf8.RuneCountInString(value) >= minQuoteRunes && utf8.RuneCountInString(value) <= maxQuoteRunes {
			return value, true
		}
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !strings.Contains(text, trimmed) {
		return "", false
	}
	if utf8.RuneCountInString(trimmed) < minQuoteRunes || utf8.RuneCountInString(trimmed) > maxQuoteRunes {
		return "", false
	}
	return trimmed, true
}

func normalizeTaskKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var normalized strings.Builder
	separator := false
	for _, current := range value {
		switch {
		case unicode.IsMark(current):
			// Combining marks belong to the preceding letter and must not
			// introduce a word break.
			if normalized.Len() > 0 && !separator {
				normalized.WriteRune(current)
			}
		case unicode.IsLetter(current) || unicode.IsDigit(current):
			normalized.WriteRune(current)
			separator = false
		case current == '-' || current == '_' || current == '.':
			if normalized.Len() > 0 && !separator {
				normalized.WriteByte('-')
				separator = true
			}
		default:
			if normalized.Len() > 0 && !separator {
				normalized.WriteByte('-')
				separator = true
			}
		}
	}
	key := strings.Trim(normalized.String(), "-.")
	if utf8.RuneCountInString(key) > maxTaskKeyRunes {
		key = truncateRunes(key, maxTaskKeyRunes)
	}
	return key
}
