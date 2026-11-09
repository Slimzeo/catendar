package conversations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("CONVERSATIONS_TEST_HELPER") {
	case "server":
		runFakeServer()
		os.Exit(0)
	case "planner":
		runFakePlanner()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type fakeServerThreads struct {
	Active   []json.RawMessage `json:"active"`
	Archived []json.RawMessage `json:"archived"`
}

type fakeServerFixture struct {
	Threads       fakeServerThreads            `json:"threads"`
	Turns         map[string][]json.RawMessage `json:"turns"`
	Errors        map[string]fakeRPCError      `json:"errors"`
	DelayMillis   int                          `json:"delayMillis"`
	Notifications []json.RawMessage            `json:"notifications"`
}

type fakeRPCRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func runFakeServer() {
	fixturePath := os.Getenv("CONVERSATIONS_TEST_SERVER_FIXTURE")
	rawFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		helperFail("read server fixture: %v", err)
	}
	var fixture fakeServerFixture
	if err := json.Unmarshal(rawFixture, &fixture); err != nil {
		helperFail("decode server fixture: %v", err)
	}
	requestLog := os.Getenv("CONVERSATIONS_TEST_REQUESTS_FILE")
	activeIndex, archivedIndex := 0, 0
	turnIndex := make(map[string]int)

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		var request fakeRPCRequest
		if err := json.Unmarshal(line, &request); err != nil {
			continue
		}
		if requestLog != "" {
			if file, err := os.OpenFile(requestLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				file.Write(line)
				file.Close()
			}
		}
		if len(request.ID) == 0 || request.Method == "" {
			continue
		}
		if fixture.DelayMillis > 0 {
			time.Sleep(time.Duration(fixture.DelayMillis) * time.Millisecond)
		}
		for _, notification := range fixture.Notifications {
			os.Stdout.Write(notification)
			os.Stdout.Write([]byte("\n"))
		}
		if rpcErr, ok := fixture.Errors[request.Method]; ok {
			writeFakeRPCError(request.ID, rpcErr)
			continue
		}
		switch request.Method {
		case "initialize":
			writeFakeRPCResult(request.ID, json.RawMessage(`{"userAgent":"catendar-test"}`))
		case "thread/list":
			var params struct {
				Archived bool `json:"archived"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Archived {
				writeFakeRPCResult(request.ID, nextFakePage(fixture.Threads.Archived, &archivedIndex))
			} else {
				writeFakeRPCResult(request.ID, nextFakePage(fixture.Threads.Active, &activeIndex))
			}
		case "thread/turns/list":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			index := turnIndex[params.ThreadID]
			writeFakeRPCResult(request.ID, nextFakePage(fixture.Turns[params.ThreadID], &index))
			turnIndex[params.ThreadID] = index
		default:
			writeFakeRPCError(request.ID, fakeRPCError{Code: -32601, Message: "method not found"})
		}
	}
}

func nextFakePage(pages []json.RawMessage, index *int) json.RawMessage {
	if *index >= len(pages) {
		return json.RawMessage(`{"data":[],"nextCursor":null}`)
	}
	page := pages[*index]
	*index = *index + 1
	return page
}

func writeFakeRPCResult(id json.RawMessage, result json.RawMessage) {
	fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, result)
}

func writeFakeRPCError(id json.RawMessage, rpcErr fakeRPCError) {
	encoded, _ := json.Marshal(rpcErr)
	fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"error":%s}`+"\n", id, encoded)
}

type fakePlannerFixture struct {
	Output      string `json:"output"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exitCode"`
	DelayMillis int    `json:"delayMillis"`
}

func runFakePlanner() {
	fixturePath := os.Getenv("CONVERSATIONS_TEST_PLANNER_FIXTURE")
	rawFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		helperFail("read planner fixture: %v", err)
	}
	var fixture fakePlannerFixture
	if err := json.Unmarshal(rawFixture, &fixture); err != nil {
		helperFail("decode planner fixture: %v", err)
	}

	args := os.Args[1:]
	if path := os.Getenv("CONVERSATIONS_TEST_ARGS_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(args, "\n")), 0o600)
	}
	if schemaPath := argumentValue(args, "--output-schema"); schemaPath != "" {
		if path := os.Getenv("CONVERSATIONS_TEST_SCHEMA_FILE"); path != "" {
			if data, err := os.ReadFile(schemaPath); err == nil {
				_ = os.WriteFile(path, data, 0o600)
			}
		}
	}
	if path := os.Getenv("CONVERSATIONS_TEST_STDIN_FILE"); path != "" {
		if input, err := io.ReadAll(os.Stdin); err == nil {
			_ = os.WriteFile(path, input, 0o600)
		}
	} else {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	if fixture.DelayMillis > 0 {
		time.Sleep(time.Duration(fixture.DelayMillis) * time.Millisecond)
	}
	if fixture.Stderr != "" {
		fmt.Fprint(os.Stderr, fixture.Stderr)
	}
	if fixture.ExitCode == 0 {
		if resultPath := argumentValue(args, "-o"); resultPath != "" {
			_ = os.WriteFile(resultPath, []byte(fixture.Output), 0o600)
		}
	}
	os.Exit(fixture.ExitCode)
}

func argumentValue(args []string, name string) string {
	for index, arg := range args {
		if arg == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func helperFail(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "conversation helper: "+format+"\n", values...)
	os.Exit(1)
}
