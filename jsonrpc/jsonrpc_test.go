package jsonrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"testing"
)

func TestReadRequestAndWriteResponse(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"workspaceRoot":"/tmp/workspace"}}`
	input := bytes.NewBufferString("Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + payload)

	request, err := ReadRequest(bufio.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != "initialize" {
		t.Fatalf("expected initialize method, got %q", request.Method)
	}

	output := &bytes.Buffer{}
	if err := WriteResponse(output, Response{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  map[string]any{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}

	if !bytes.HasPrefix(output.Bytes(), []byte("Content-Length: ")) {
		t.Fatalf("expected content length header, got %q", output.String())
	}

	response, err := ReadResponse(bufio.NewReader(bytes.NewReader(output.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || response.JSONRPC != "2.0" {
		t.Fatalf("unexpected response %+v", response)
	}
}

func TestReadRequestRejectsTruncatedPayload(t *testing.T) {
	input := bytes.NewBufferString("Content-Length: 15\r\n\r\n{\"jsonrpc\":\"2")
	if _, err := ReadRequest(bufio.NewReader(input)); err == nil {
		t.Fatal("expected truncated payload error")
	}
}

func TestReadRequestRejectsMissingContentLength(t *testing.T) {
	input := bytes.NewBufferString("Content-Type: application/json\r\n\r\n{}")
	if _, err := ReadRequest(bufio.NewReader(input)); err == nil {
		t.Fatal("expected missing content length error")
	}
}

func TestReadRequestRoundTripsParams(t *testing.T) {
	payload, err := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      "abc",
		Method:  "diagnostics/file",
		Params:  json.RawMessage(`{"file":"src/example.ts"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	input := bytes.NewBufferString("Content-Length: ")
	input.WriteString(strconv.Itoa(len(payload)))
	input.WriteString("\r\n\r\n")
	input.Write(payload)

	request, err := ReadRequest(bufio.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if string(request.Params) != `{"file":"src/example.ts"}` {
		t.Fatalf("unexpected params %s", string(request.Params))
	}
}
