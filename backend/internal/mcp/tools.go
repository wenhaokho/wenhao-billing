// Package mcp holds the tool-registration helper every MCP tool goes
// through. The SDK runs each tool handler in its own goroutine with no
// recover (vendor/.../internal/jsonrpc2/conn.go), so a panic in a tool would
// take the whole API process down. The helper recovers it, refuses a call
// without a principal, and runs the tool body inside a transaction, so a
// failed or panicked tool leaves nothing behind.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"
)

// toolFn is the body of a tool: it runs as the given principal, inside the
// transaction the helper opened, and returns the tool's output.
type toolFn[In, Out any] func(ctx context.Context, tx pgx.Tx, p principalRef, in In) (Out, error)

// addTool registers one tool on the server and returns the handler it
// installed, so a test can call it directly.
//
// The handler:
//   - refuses the call when the request carries no principal (the transport's
//     verifier is the only way in, so this means the call did not come
//     through it) — the tool body runs nothing;
//   - opens a transaction on the pool and hands it to the tool body, so the
//     body's writes commit with the tool and roll back on any error;
//   - recovers a panic in the tool body, logs it with its stack, reports it
//     to Sentry and answers a generic isError result. The panic's text never
//     reaches the client.
//
// The error the tool body returns is the tool's own refusal (Python's
// {"error": sentence} shape, in the later tasks) and is passed through
// unchanged; a panic becomes a generic message.
func addTool[In, Out any](s *mcp.Server, d Deps, tool *mcp.Tool, fn toolFn[In, Out]) mcp.ToolHandlerFor[In, Out] {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var out Out
		p, ok := principalFrom(ctx)
		if !ok {
			// Fail closed: without a principal nothing runs.
			var zero Out
			res := &mcp.CallToolResult{}
			res.SetError(fmt.Errorf("tool %s: no authenticated principal", tool.Name))
			return res, zero, nil
		}
		// The tool body runs inside a transaction: its writes commit with
		// the tool and roll back on any error or panic.
		err := db.InTx(ctx, d.Pool, func(tx pgx.Tx) error {
			var callErr error
			out, callErr = callTool(ctx, tx, p, in, fn)
			return callErr
		})
		if err != nil {
			return toolError(ctx, tool.Name, err), out, nil
		}
		return nil, out, nil
	}
	mcp.AddTool(s, tool, handler)
	return handler
}

// callTool runs the tool body, turning a panic into an error. The stack is
// logged here, where it still exists; the error the caller sees carries no
// stack text.
func callTool[In, Out any](ctx context.Context, tx pgx.Tx, p principalRef, in In, fn toolFn[In, Out]) (out Out, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			logger(ctx).ErrorContext(ctx, "mcp tool panicked", "panic", fmt.Sprint(r), "stack", stack)
			observe.Report(fmt.Errorf("mcp tool panic: %v", r), map[string]string{"component": "mcp"})
			var zero Out
			out = zero
			err = errors.New("the tool failed unexpectedly")
		}
	}()
	return fn(ctx, tx, p, in)
}

// toolError is the answer to a failed tool: an isError result carrying the
// error's text. The SDK turns a returned error into an isError result too,
// but building it here keeps the message out of the structured content.
func toolError(ctx context.Context, name string, err error) *mcp.CallToolResult {
	logger(ctx).ErrorContext(ctx, "mcp tool failed", "tool", name, "err", err)
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}
