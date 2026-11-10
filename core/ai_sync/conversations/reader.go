package conversations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	threadPageSize  = 100
	maxThreadPages  = 50
	turnPageSize    = 20
	maxTurnPages    = 100
	maxResponseLine = 16 * 1024 * 1024
)

var (
	// ErrSourceUnavailable means the local app-server could not be started or
	// stopped responding. It does not mean the conversation history is empty.
	ErrSourceUnavailable = errors.New("codex conversation source is unavailable")
	// ErrSourceUnsupported means the local app-server speaks a protocol that
	// does not expose the read-only conversation APIs we require.
	ErrSourceUnsupported = errors.New("codex conversation source is unsupported")
)

var errResponseLineTooLong = errors.New("app-server response line is too long")

// oversizedResponseError marks a response that cannot be read without
// exceeding the reader's line limit while still matching both exported error
// sentinels for callers.
type oversizedResponseError struct{}

func (oversizedResponseError) Error() string {
	return ErrSourceUnavailable.Error() + ": app-server response exceeds the size limit"
}

func (oversizedResponseError) Is(target error) bool {
	return target == ErrSourceUnavailable || target == errResponseLineTooLong
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	nextID int64
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	if b.limit <= 0 {
		return len(value), nil
	}
	b.data = append(b.data, value...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(value), nil
}

func startRPC(ctx context.Context, binary string) (*rpcClient, error) {
	command := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	configureProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: could not open app-server input", ErrSourceUnavailable)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: could not open app-server output", ErrSourceUnavailable)
	}
	command.Stderr = &tailBuffer{limit: 8 * 1024}
	command.WaitDelay = 5 * time.Second
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("%w: could not start app-server", ErrSourceUnavailable)
	}
	return &rpcClient{
		cmd:    command,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
	}, nil
}

func (c *rpcClient) close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
}

func (c *rpcClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "catendar",
			"title":   "catendar",
			"version": "1.0.0",
		},
		"capabilities": map[string]any{"experimentalApi": false},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *rpcClient) notify(method string, params any) error {
	if err := c.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}); err != nil {
		return fmt.Errorf("%w: could not send %s notification", ErrSourceUnavailable, method)
	}
	return nil
}

func (c *rpcClient) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID
	c.nextID++
	if err := c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return fmt.Errorf("%w: could not send %s request", ErrSourceUnavailable, method)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := readBoundedLine(c.reader, maxResponseLine)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, errResponseLineTooLong) {
				return oversizedResponseError{}
			}
			return fmt.Errorf("%w: app-server stopped during %s", ErrSourceUnavailable, method)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			// Diagnostics on stdout are not part of the JSON-RPC stream.
			continue
		}
		var envelope rpcEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return fmt.Errorf("%w: app-server sent an invalid response", ErrSourceUnavailable)
		}
		if len(envelope.Method) > 0 && len(envelope.ID) > 0 && !isNullJSON(envelope.ID) {
			// We only read history. Refuse unsupported server-initiated calls
			// instead of leaving an approval request hanging.
			_ = c.write(map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"error": map[string]any{
					"code":    -32601,
					"message": "catendar conversation reader does not accept server calls",
				},
			})
			continue
		}
		if !rpcIDMatches(envelope.ID, id) {
			continue
		}
		if envelope.Error != nil {
			return classifyRPCError(method, envelope.Error)
		}
		if len(envelope.Result) == 0 {
			return fmt.Errorf("%w: app-server returned an empty %s response", ErrSourceUnavailable, method)
		}
		if out != nil {
			if err := json.Unmarshal(envelope.Result, out); err != nil {
				return fmt.Errorf("%w: app-server returned an invalid %s response", ErrSourceUnavailable, method)
			}
		}
		return nil
	}
}

func (c *rpcClient) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = c.stdin.Write(encoded)
	return err
}

func classifyRPCError(method string, rpcErr *rpcError) error {
	if rpcErr == nil {
		return fmt.Errorf("%w: %s request failed", ErrSourceUnavailable, method)
	}
	switch rpcErr.Code {
	case -32601, -32602:
		return fmt.Errorf("%w: %s is not supported", ErrSourceUnsupported, method)
	default:
		return fmt.Errorf("%w: %s request failed", ErrSourceUnavailable, method)
	}
}

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	line := make([]byte, 0, 4096)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > limit {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}
			return nil, errResponseLineTooLong
		}
		line = append(line, fragment...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return line, nil
		}
		return nil, err
	}
}

func isNullJSON(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func rpcIDMatches(raw json.RawMessage, id int64) bool {
	if len(raw) == 0 || isNullJSON(raw) {
		return false
	}
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number == id
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text == strconv.FormatInt(id, 10)
	}
	return false
}

type epochSeconds int64

func (value *epochSeconds) UnmarshalJSON(data []byte) error {
	parsed, err := parseEpochJSON(data)
	if err != nil {
		return err
	}
	*value = epochSeconds(parsed)
	return nil
}

func parseEpochJSON(data []byte) (int64, error) {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		return 0, nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return 0, err
		}
		return parseEpochString(value)
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	return normalizeEpoch(int64(number)), nil
}

func parseEpochString(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return normalizeEpoch(number), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Unix(), nil
		}
	}
	return 0, errors.New("invalid conversation timestamp")
}

func normalizeEpoch(value int64) int64 {
	if value <= 0 {
		return 0
	}
	switch {
	case value >= 1_000_000_000_000_000_000:
		return value / 1_000_000_000
	case value >= 1_000_000_000_000_000:
		return value / 1_000_000
	case value >= 1_000_000_000_000:
		return value / 1_000
	default:
		return value
	}
}

// The reader uses the read-only subset of the official app-server protocol v2
// schema:
// https://github.com/openai/codex/blob/main/codex-rs/app-server-protocol/src/protocol/v2.rs
// Some fields also accept the snake_case spelling used by older app-servers.
type threadInfo struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	UpdatedAt           epochSeconds    `json:"updatedAt"`
	UpdatedAtSnake      epochSeconds    `json:"updated_at"`
	CreatedAt           epochSeconds    `json:"createdAt"`
	Cwd                 string          `json:"cwd"`
	Ephemeral           bool            `json:"ephemeral"`
	ParentThreadID      string          `json:"parentThreadId"`
	ParentThreadIDSnake string          `json:"parent_thread_id"`
	Source              json.RawMessage `json:"source"`
	Originator          string          `json:"originator"`
}

func (thread threadInfo) updated() int64 {
	if value := int64(thread.UpdatedAt); value > 0 {
		return value
	}
	return int64(thread.UpdatedAtSnake)
}

type threadListResponse struct {
	Data       []threadInfo `json:"data"`
	Threads    []threadInfo `json:"threads"`
	NextCursor *string      `json:"nextCursor"`
}

type turnInfo struct {
	ID               string       `json:"id"`
	StartedAt        epochSeconds `json:"startedAt"`
	StartedAtSnake   epochSeconds `json:"started_at"`
	CompletedAt      epochSeconds `json:"completedAt"`
	CompletedAtSnake epochSeconds `json:"completed_at"`
	Timestamp        epochSeconds `json:"timestamp"`
	CreatedAt        epochSeconds `json:"createdAt"`
	Status           string       `json:"status"`
	Items            []threadItem `json:"items"`
}

func (turn turnInfo) started() int64 {
	for _, value := range []int64{
		int64(turn.StartedAt),
		int64(turn.StartedAtSnake),
		int64(turn.Timestamp),
		int64(turn.CreatedAt),
		int64(turn.CompletedAt),
		int64(turn.CompletedAtSnake),
	} {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (turn turnInfo) completed() int64 {
	if value := int64(turn.CompletedAt); value > 0 {
		return value
	}
	return int64(turn.CompletedAtSnake)
}

func (turn turnInfo) isCompleted() bool {
	switch strings.ToLower(strings.TrimSpace(turn.Status)) {
	case "completed", "complete":
		return true
	case "":
		return turn.completed() > 0
	default:
		return false
	}
}

type turnListResponse struct {
	Data       []turnInfo `json:"data"`
	Turns      []turnInfo `json:"turns"`
	NextCursor *string    `json:"nextCursor"`
}

type threadItem struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	ItemID  string          `json:"itemId"`
	Text    string          `json:"text"`
	Phase   string          `json:"phase"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ReadRecent reads public user and final assistant text from local Codex
// conversations whose individual turns fall inside [since, until]. It never
// reads Codex private storage files directly.
func ReadRecent(ctx context.Context, binary string, since, until time.Time) ([]Message, error) {
	if strings.TrimSpace(binary) == "" {
		return nil, fmt.Errorf("%w: Codex binary is not configured", ErrSourceUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !until.After(since) {
		return nil, errors.New("conversation window must end after it starts")
	}

	client, err := startRPC(ctx, binary)
	if err != nil {
		return nil, err
	}
	defer client.close()

	if err := client.initialize(ctx); err != nil {
		return nil, err
	}
	threads, err := client.listThreads(ctx, since)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(threads, func(left, right int) bool {
		leftUpdated, rightUpdated := threads[left].updated(), threads[right].updated()
		if leftUpdated != rightUpdated {
			return leftUpdated > rightUpdated
		}
		return threads[left].ID < threads[right].ID
	})

	messages := make([]Message, 0)
	seen := make(map[string]struct{})
	for _, thread := range threads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !usableThread(thread) {
			continue
		}
		// Do not spend RPC calls on threads the server already told us are
		// older than the requested window.
		if updated := thread.updated(); updated > 0 && updated < since.Unix() {
			continue
		}
		turns, err := client.listTurns(ctx, thread.ID, since)
		if err != nil {
			return nil, err
		}
		title := cleanThreadTitle(thread.Name)
		project := safeProject(thread.Cwd)
		for _, turn := range turns {
			startedAt := turn.started()
			assistantAt := turn.completed()
			if assistantAt <= 0 {
				assistantAt = startedAt
			}
			completed := turn.isCompleted()
			for _, item := range turn.Items {
				switch normalizeItemType(item.Type) {
				case "usermessage":
					if startedAt < since.Unix() || startedAt > until.Unix() {
						continue
					}
					appendMessage(&messages, seen, thread.ID, turn.ID, item, title, project, RoleUser, userItemTexts(item), startedAt)
				case "agentmessage", "assistantmessage":
					if !completed || !finalAgentPhase(item.Phase) {
						continue
					}
					if assistantAt < since.Unix() || assistantAt > until.Unix() {
						continue
					}
					texts := []string{item.Text}
					if strings.TrimSpace(item.Text) == "" {
						texts = userItemTexts(item)
					}
					appendAssistantMessage(&messages, seen, thread.ID, turn.ID, item, title, project, texts, assistantAt)
				case "message":
					switch strings.ToLower(strings.TrimSpace(item.Role)) {
					case RoleUser:
						if startedAt < since.Unix() || startedAt > until.Unix() {
							continue
						}
						appendMessage(&messages, seen, thread.ID, turn.ID, item, title, project, RoleUser, userItemTexts(item), startedAt)
					case RoleAssistant, "agent":
						if !completed || !finalAgentPhase(item.Phase) {
							continue
						}
						if assistantAt < since.Unix() || assistantAt > until.Unix() {
							continue
						}
						texts := []string{item.Text}
						if strings.TrimSpace(item.Text) == "" {
							texts = userItemTexts(item)
						}
						appendAssistantMessage(&messages, seen, thread.ID, turn.ID, item, title, project, texts, assistantAt)
					}
				}
			}
		}
	}

	sort.SliceStable(messages, func(left, right int) bool {
		if messages[left].At != messages[right].At {
			return messages[left].At < messages[right].At
		}
		if messages[left].ThreadID != messages[right].ThreadID {
			return messages[left].ThreadID < messages[right].ThreadID
		}
		if messages[left].ThreadTitle != messages[right].ThreadTitle {
			return messages[left].ThreadTitle < messages[right].ThreadTitle
		}
		if messages[left].Role != messages[right].Role {
			return messages[left].Role == RoleUser
		}
		return messages[left].ID < messages[right].ID
	})
	return messages, nil
}

func (c *rpcClient) listThreads(ctx context.Context, since time.Time) ([]threadInfo, error) {
	threads := make([]threadInfo, 0)
	seen := make(map[string]struct{})
	for _, archived := range []bool{false, true} {
		cursor := ""
		previousCursor := ""
		for page := 0; page < maxThreadPages; page++ {
			params := map[string]any{
				"limit":         threadPageSize,
				"sortKey":       "updated_at",
				"sortDirection": "desc",
				"sourceKinds":   []string{"cli", "vscode", "appServer"},
				"archived":      archived,
			}
			if cursor != "" {
				params["cursor"] = cursor
			}
			var response threadListResponse
			if err := c.call(ctx, "thread/list", params, &response); err != nil {
				return nil, err
			}
			pageThreads := response.Data
			if pageThreads == nil && response.Threads != nil {
				pageThreads = response.Threads
			}
			for _, thread := range pageThreads {
				if _, ok := seen[thread.ID]; ok || strings.TrimSpace(thread.ID) == "" {
					continue
				}
				seen[thread.ID] = struct{}{}
				threads = append(threads, thread)
			}
			if pageOlderThan(pageThreads, since) {
				break
			}
			next := ""
			if response.NextCursor != nil {
				next = strings.TrimSpace(*response.NextCursor)
			}
			if next == "" {
				break
			}
			if next == previousCursor || next == cursor {
				return nil, fmt.Errorf("%w: thread/list cursor did not advance", ErrSourceUnavailable)
			}
			previousCursor = cursor
			cursor = next
		}
	}
	return threads, nil
}

func (c *rpcClient) listTurns(ctx context.Context, threadID string, since time.Time) ([]turnInfo, error) {
	turns := make([]turnInfo, 0)
	cursor := ""
	previousCursor := ""
	limit := turnPageSize
	itemsView := "full"
	for page := 0; page < maxTurnPages; page++ {
		params := map[string]any{
			"threadId":      threadID,
			"sortDirection": "desc",
			"limit":         limit,
			"itemsView":     itemsView,
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var response turnListResponse
		if err := c.call(ctx, "thread/turns/list", params, &response); err != nil {
			if errors.Is(err, errResponseLineTooLong) && limit > 1 {
				// A page of full turn items can be very large. Retry the
				// same cursor with fewer turns before giving up on it.
				limit /= 2
				page--
				continue
			}
			if errors.Is(err, errResponseLineTooLong) && itemsView == "full" {
				// A single tool-heavy turn can exceed the transport limit.
				// The official summary view retains public message excerpts.
				itemsView = "summary"
				page--
				continue
			}
			return nil, err
		}
		itemsView = "full"
		pageTurns := response.Data
		if pageTurns == nil && response.Turns != nil {
			pageTurns = response.Turns
		}
		turns = append(turns, pageTurns...)
		if pageTurnsOlderThan(pageTurns, since) {
			break
		}
		next := ""
		if response.NextCursor != nil {
			next = strings.TrimSpace(*response.NextCursor)
		}
		if next == "" {
			break
		}
		if next == previousCursor || next == cursor {
			return nil, fmt.Errorf("%w: thread/turns/list cursor did not advance", ErrSourceUnavailable)
		}
		previousCursor = cursor
		cursor = next
	}
	return turns, nil
}

func pageOlderThan(threads []threadInfo, since time.Time) bool {
	if len(threads) == 0 {
		return false
	}
	for _, thread := range threads {
		updated := thread.updated()
		if updated == 0 || updated >= since.Unix() {
			return false
		}
	}
	return true
}

func pageTurnsOlderThan(turns []turnInfo, since time.Time) bool {
	if len(turns) == 0 {
		return false
	}
	for _, turn := range turns {
		at := turn.started()
		if completed := turn.completed(); completed > at {
			at = completed
		}
		if at == 0 || at >= since.Unix() {
			return false
		}
	}
	return true
}

func usableThread(thread threadInfo) bool {
	if strings.TrimSpace(thread.ID) == "" || thread.Ephemeral {
		return false
	}
	if strings.TrimSpace(thread.ParentThreadID) != "" || strings.TrimSpace(thread.ParentThreadIDSnake) != "" {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(threadSource(thread.Source)))
	if source != "" && !interactiveSource(source) {
		return false
	}
	if strings.Contains(strings.ToLower(thread.Originator), "catendar") ||
		strings.Contains(strings.ToLower(threadSource(thread.Source)), "catendar") {
		return false
	}
	return true
}

func interactiveSource(source string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(source))
	switch normalized {
	case "cli", "vscode", "appserver":
		return true
	default:
		return false
	}
}

func threadSource(raw json.RawMessage) string {
	if len(raw) == 0 || isNullJSON(raw) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	for _, key := range []string{"custom", "type", "kind", "name"} {
		value, ok := object[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(value, &text); err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func normalizeItemType(value string) string {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "usermessage", "usermsg":
		return "usermessage"
	case "agentmessage", "assistantmessage":
		return normalized
	default:
		return normalized
	}
}

func finalAgentPhase(value string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "", "final", "finalanswer":
		return true
	default:
		return false
	}
}

func userItemTexts(item threadItem) []string {
	if len(item.Content) == 0 || isNullJSON(item.Content) {
		if strings.TrimSpace(item.Text) != "" {
			return []string{item.Text}
		}
		return nil
	}
	var single string
	if err := json.Unmarshal(item.Content, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil
		}
		return []string{single}
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(item.Content, &parts); err != nil {
		return nil
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		kind := normalizeItemType(part.Type)
		if (kind == "text" || kind == "inputtext") && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func appendMessage(
	messages *[]Message,
	seen map[string]struct{},
	threadID, turnID string,
	item threadItem,
	title, project, role string,
	texts []string,
	at int64,
) {
	text := cleanPublicText(strings.Join(texts, "\n"))
	if text == "" {
		return
	}
	appendPublicMessage(messages, seen, threadID, turnID, item, title, project, role, text, at)
}

func appendAssistantMessage(
	messages *[]Message,
	seen map[string]struct{},
	threadID, turnID string,
	item threadItem,
	title, project string,
	texts []string,
	at int64,
) {
	text := cleanAssistantText(strings.Join(texts, "\n"))
	if text == "" {
		return
	}
	appendPublicMessage(messages, seen, threadID, turnID, item, title, project, RoleAssistant, text, at)
}

func appendPublicMessage(
	messages *[]Message,
	seen map[string]struct{},
	threadID, turnID string,
	item threadItem,
	title, project, role, text string,
	at int64,
) {
	itemID := strings.TrimSpace(item.ID)
	if itemID == "" {
		itemID = strings.TrimSpace(item.ItemID)
	}
	id := messageDigest(threadID, turnID, itemID, role, text)
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	*messages = append(*messages, Message{
		ID:          id,
		ThreadID:    threadID,
		ThreadTitle: title,
		Project:     project,
		Role:        role,
		Text:        plannerMessageText(text),
		At:          at,
	})
}
