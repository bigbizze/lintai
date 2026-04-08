package jsonrpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *ResponseError `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func ReadRequest(reader *bufio.Reader) (Request, error) {
	payload, err := readPayload(reader)
	if err != nil {
		return Request{}, err
	}

	var request Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func ReadResponse(reader *bufio.Reader) (Response, error) {
	payload, err := readPayload(reader)
	if err != nil {
		return Response{}, err
	}

	var response Response
	if err := json.Unmarshal(payload, &response); err != nil {
		return Response{}, err
	}
	return response, nil
}

func readPayload(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		name, value, found := strings.Cut(strings.TrimRight(line, "\r\n"), ":")
		if !found {
			return nil, fmt.Errorf("jsonrpc: malformed header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("jsonrpc: invalid content length %q", value)
			}
			contentLength = length
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("jsonrpc: missing Content-Length header")
	}

	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func WriteResponse(writer io.Writer, response Response) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}
