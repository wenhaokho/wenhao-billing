// Package mcp holds the tool-registration helper every MCP tool goes
// through. The SDK runs each tool handler in its own goroutine with no
// recover (vendor/.../internal/jsonrpc2/conn.go), so a panic in a tool would
// take the whole API process down. The helper recovers it, refuses a call
// without a principal, and runs the tool body inside a transaction, so a
// refused, failed or panicked tool leaves nothing behind.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LOQATO/gokit/observe"
)

// errUnexpected is the only text a client sees for a failure that is not a
// refusal: a driver or database error, a failed begin/commit, an output that
// does not marshal, or a panic. The cause is logged and reported instead.
const errUnexpected = "the tool failed unexpectedly"

// Refusal is a tool's own refusal of a call: a bad argument or a domain rule
// (Python's {"error": sentence} result). Its message is written for the
// client, so it reaches the client verbatim; never put driver or internal
// error text in one.
type Refusal struct{ Msg string }

func (r *Refusal) Error() string { return r.Msg }

// Refuse is the error a tool body returns to refuse a call. The helper
// rolls the transaction back and answers {"error": msg} (not isError). It
// may be wrapped with %w.
func Refuse(format string, args ...any) error {
	return &Refusal{Msg: fmt.Sprintf(format, args...)}
}

// toolFn is the body of a tool: it runs as the given principal, inside the
// transaction the helper opened (nil for a tool registered withoutTx), and
// returns the tool's output.
type toolFn[In, Out any] func(ctx context.Context, tx pgx.Tx, p principalRef, in In) (Out, error)

// toolOption changes how addTool runs a tool.
type toolOption func(*toolConfig)

type toolConfig struct {
	noTx      bool
	outSchema bool
}

// withoutTx runs the tool body with no transaction: it gets a nil pgx.Tx and
// the helper never touches the pool. Only for a tool that reads no database
// state at all (ping).
func withoutTx() toolOption { return func(c *toolConfig) { c.noTx = true } }

// withOutSchema declares Out's own schema (derived from the struct) as the
// tool's outputSchema instead of the open object. Only for the tools Python
// wraps as {"result": ...} (ping, list and string returns): they never
// refuse, and a refusal's {"error": msg} would not conform to it.
func withOutSchema() toolOption { return func(c *toolConfig) { c.outSchema = true } }

// openObjectSchema is what Python's dict-returning tools declare:
// {"type":"object","additionalProperties":true}. Both a tool's output and a
// refusal's {"error": msg} conform to it.
func openObjectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", AdditionalProperties: &jsonschema.Schema{}}
}

// isCancellation reports whether err is the caller going away rather than a
// fault: it is still logged and answered generically, but not reported.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// addTool registers one tool on the server and returns the handler it
// installed, so a test can call it directly. Every MCP tool goes through it;
// its contract is:
//
//   - No principal on the request (the call did not come through the
//     transport's verifier): isError, and the tool body does not run.
//   - Success (the body returns a nil error): Out is marshalled and
//     validated against the declared outputSchema before the commit (a
//     marshal or schema failure is an unexpected error and rolls back), then
//     the transaction commits and the result carries Out as structured
//     content plus its JSON as a text block. Out must be a struct.
//   - Refusal (the body returns an error matching *Refusal, see Refuse):
//     the transaction rolls back and the result is NOT isError: its
//     structured content is exactly {"error": msg} and its one text block
//     is that object's compact JSON, as FastMCP answers a tool's dict.
//   - Any other error (driver, begin/commit, output marshalling) or a panic
//     anywhere in the handler: the transaction rolls back, the cause is
//     logged with slog (a panic with its stack) and reported with
//     observe.Report (except a context cancellation or deadline, which is
//     only logged), and the client gets a generic isError result ("the
//     tool failed unexpectedly") with no structured content. Raw error text
//     never reaches the client.
//
// The declared outputSchema: by default Python's open object
// {"type":"object","additionalProperties":true}, which every refusable
// tool must declare so a refusal's {"error": msg} conforms to it (clients
// validate structuredContent against outputSchema). A tool Python wraps as
// {"result": ...} declares Out's own schema with withOutSchema() and must
// never refuse; a tool may also set tool.OutputSchema itself.
//
// The tool is registered with the SDK as Out = any, so an isError or
// refusal result never makes the SDK marshal and validate a zero Out.
func addTool[In, Out any](s *mcp.Server, d Deps, tool *mcp.Tool, fn toolFn[In, Out], opts ...toolOption) mcp.ToolHandlerFor[In, any] {
	var cfg toolConfig
	for _, o := range opts {
		o(&cfg)
	}
	rt := reflect.TypeFor[Out]()
	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("addTool: tool %q: Out must be a struct, got %v", tool.Name, rt))
	}
	t := *tool
	switch {
	case t.OutputSchema != nil:
	case cfg.outSchema:
		schema, err := jsonschema.ForType(rt, &jsonschema.ForOptions{})
		if err != nil {
			panic(fmt.Sprintf("addTool: tool %q: output schema: %v", tool.Name, err))
		}
		t.OutputSchema = schema
	default:
		t.OutputSchema = openObjectSchema()
	}
	outSchema, err := resolveSchema(t.OutputSchema)
	if err != nil {
		panic(fmt.Sprintf("addTool: tool %q: output schema: %v", tool.Name, err))
	}

	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in In) (res *mcp.CallToolResult, out any, err error) {
		// First, so a panic anywhere below (principal lookup, begin, the
		// body, output marshalling, commit, result building) is covered.
		defer func() {
			if r := recover(); r != nil {
				logger(ctx).ErrorContext(ctx, "mcp tool panicked", "tool", t.Name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
				observe.Report(fmt.Errorf("mcp tool %s panic: %v", t.Name, r), map[string]string{"component": "mcp"})
				res, out, err = unexpectedResult(), nil, nil
			}
		}()
		p, ok := principalFrom(ctx)
		if !ok {
			// Fail closed: without a principal nothing runs.
			r := &mcp.CallToolResult{}
			r.SetError(fmt.Errorf("tool %s: no authenticated principal", t.Name))
			return r, nil, nil
		}
		raw, err := runTool(ctx, d, cfg, outSchema, p, in, fn)
		if err != nil {
			var ref *Refusal
			if errors.As(err, &ref) {
				return refusalResult(ref.Msg), nil, nil
			}
			logger(ctx).ErrorContext(ctx, "mcp tool failed", "tool", t.Name, "err", err)
			if !isCancellation(err) {
				observe.Report(fmt.Errorf("mcp tool %s: %w", t.Name, err), map[string]string{"component": "mcp"})
			}
			return unexpectedResult(), nil, nil
		}
		return nil, raw, nil
	}
	mcp.AddTool(s, &t, handler)
	return handler
}

// runTool runs the body inside its transaction and marshals its output
// before the commit, so an output that does not marshal rolls back too. The
// transaction commits only on success; a refusal, an error or a panic rolls
// it back (the deferred rollback runs while a panic unwinds, before the
// handler's recover).
func runTool[In, Out any](ctx context.Context, d Deps, cfg toolConfig, schema *jsonschema.Resolved, p principalRef, in In, fn toolFn[In, Out]) (json.RawMessage, error) {
	if cfg.noTx {
		o, err := fn(ctx, nil, p, in)
		if err != nil {
			return nil, err
		}
		return marshalOut(o, schema)
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			logger(ctx).ErrorContext(ctx, "mcp tool rollback failed", "err", rbErr)
			observe.Report(fmt.Errorf("mcp tool rollback: %w", rbErr), map[string]string{"component": "mcp"})
		}
	}()
	o, err := fn(ctx, tx, p, in)
	if err != nil {
		return nil, err
	}
	raw, err := marshalOut(o, schema)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return raw, nil
}

// marshalOut marshals the tool's output and validates the JSON (not the Go
// value, so a field with a custom JSON form such as decimal.Decimal is
// checked as the client sees it) against the declared outputSchema. The SDK
// would validate it too, but only after the commit; failing here rolls back.
func marshalOut(o any, schema *jsonschema.Resolved) (json.RawMessage, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode output: %w", err)
	}
	if err := schema.Validate(v); err != nil {
		return nil, fmt.Errorf("output does not match its schema: %w", err)
	}
	return raw, nil
}

// resolveSchema resolves a declared outputSchema, given as a
// *jsonschema.Schema or as any value that marshals to one.
func resolveSchema(declared any) (*jsonschema.Resolved, error) {
	s, ok := declared.(*jsonschema.Schema)
	if !ok {
		b, err := json.Marshal(declared)
		if err != nil {
			return nil, err
		}
		s = new(jsonschema.Schema)
		if err := json.Unmarshal(b, s); err != nil {
			return nil, err
		}
	}
	return s.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
}

// refusalResult is FastMCP's answer to a tool that returned {"error": msg}:
// not isError, structured content the object, and one text block holding
// its compact JSON (no HTML escaping, as Python's JSON does not escape).
func refusalResult(msg string) *mcp.CallToolResult {
	obj := map[string]string{"error": msg}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(obj) // a map of strings always encodes
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(bytes.TrimRight(b.Bytes(), "\n"))}},
		StructuredContent: obj,
	}
}

// unexpectedResult is the generic isError answer to any failure that is not
// a refusal. It carries no structured content and no cause text.
func unexpectedResult() *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(errors.New(errUnexpected))
	return res
}
