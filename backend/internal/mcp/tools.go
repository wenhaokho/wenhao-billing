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
	"slices"

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

// ArgError is a bad argument to a tool that cannot answer {"error": msg}
// because its declared outputSchema is not the open object: the list tools,
// which Python wraps as {"result": [...]}. Python raises for a bad argument
// there and FastMCP answers an isError result, so the helper does the same,
// with msg as its text: no structured content, logged at warn level and not
// reported (it is the client's mistake, not a fault). A tool with the open
// object may return either: the helper answers a BadArg from such a tool as
// the refusal {"error": msg} (Go change 11), so no dict tool can leak an
// isError for a bad argument.
type ArgError struct{ Msg string }

func (a *ArgError) Error() string { return a.Msg }

// BadArg is the error a list tool returns for a bad argument.
func BadArg(format string, args ...any) error {
	return &ArgError{Msg: fmt.Sprintf(format, args...)}
}

// toolFn is the body of a tool: it runs as the given principal, inside the
// transaction the helper opened (nil for a tool registered withoutTx), and
// returns the tool's output.
type toolFn[In, Out any] func(ctx context.Context, tx pgx.Tx, p principalRef, in In) (Out, error)

// toolOption changes how addTool runs a tool.
type toolOption func(*toolConfig)

type toolConfig struct {
	name      string
	noTx      bool
	outSchema bool
}

// withoutTx runs the tool body with no transaction: it gets a nil pgx.Tx and
// the helper never touches the pool. For a tool that reads no database state
// at all (ping), or one that commits its own work (trigger_recurring). Such a
// tool must register after-commit hooks (afterCommit) only after its own
// commit has succeeded: the helper runs every registered hook once the body
// returns, whatever it returns, so a hook registered earlier would run for
// work that never committed.
func withoutTx() toolOption { return func(c *toolConfig) { c.noTx = true } }

// withOutSchema declares Out's own schema (derived from the struct) as the
// tool's outputSchema instead of the open object. Only for the tools Python
// wraps as {"result": ...} (ping, list and string returns): they never
// refuse, and a refusal's {"error": msg} would not conform to it, so the
// helper turns a Refusal from such a tool into the generic unexpected error
// (a bug, logged and reported). The derived schema is shaped as FastMCP
// writes the wrapper: no additionalProperties on the wrapper object, nullable
// properties (Go's nil slices) declared plain, and the
// "x-fastmcp-wrap-result" marker its clients use to unwrap the result. An
// Object field is an open object, as Python's list[dict] items are.
func withOutSchema() toolOption { return func(c *toolConfig) { c.outSchema = true } }

// wrapSchema is the outputSchema FastMCP declares for a tool returning rt
// wrapped as {"result": ...}.
func wrapSchema(rt reflect.Type) (*jsonschema.Schema, error) {
	schema, err := jsonschema.ForType(rt, &jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[Object](): openObjectSchema()},
	})
	if err != nil {
		return nil, err
	}
	schema.AdditionalProperties = nil
	for _, p := range schema.Properties {
		if len(p.Types) == 2 && slices.Contains(p.Types, "null") {
			p.Type = p.Types[0]
			if p.Type == "null" {
				p.Type = p.Types[1]
			}
			p.Types = nil
		}
	}
	schema.Extra = map[string]any{"x-fastmcp-wrap-result": true}
	return schema, nil
}

// openObjectSchema is what Python's dict-returning tools declare:
// {"type":"object","additionalProperties":true}. Both a tool's output and a
// refusal's {"error": msg} conform to it.
func openObjectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", AdditionalProperties: &jsonschema.Schema{}}
}

// isCancellation reports whether err is the caller going away rather than a
// fault: it is still logged and answered generically, but not reported. The
// request's own context decides: a Canceled or DeadlineExceeded that
// surfaces while the request is still live (a database or upstream timeout)
// is a fault.
func isCancellation(ctx context.Context, err error) bool {
	return (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil
}

// isOpenObject reports whether a declared outputSchema is Python's open
// object, the only schema a refusal's {"error": msg} conforms to.
func isOpenObject(declared any) bool {
	a, err := json.Marshal(declared)
	if err != nil {
		return false
	}
	b, _ := json.Marshal(openObjectSchema())
	return bytes.Equal(a, b)
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
//   - Refusal (the body returns an error matching *Refusal, see Refuse),
//     from a tool that declares the open object (below): the transaction
//     rolls back and the result is NOT isError: its
//     structured content is exactly {"error": msg} and its one text block
//     is that object's compact JSON, as FastMCP answers a tool's dict. A
//     Refusal from a tool that declares any other schema (a withOutSchema
//     tool: it must never refuse) is a bug and counts as any other error.
//   - Bad argument (BadArg) from a tool that declares the open object: the
//     same {"error": msg} refusal.
//   - Bad argument to a list tool (BadArg from a withOutSchema tool): an
//     isError result whose text is the sentence, as Python's raise: not
//     reported.
//   - After-commit hooks (afterCommit) run once the transaction committed,
//     never after a refusal, an error or a panic, and never fail the result.
//     A withoutTx tool commits its own work and registers a hook only after
//     it, so its registered hooks run whatever it returns (not on a panic).
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
// The tool is registered with the SDK as Out = any, and the handler builds
// every result itself, so an isError or refusal result never makes the SDK
// marshal and validate a zero Out, and the SDK does not re-encode the output
// (which would sort its keys).
func addTool[In, Out any](s *mcp.Server, d Deps, tool *mcp.Tool, fn toolFn[In, Out], opts ...toolOption) mcp.ToolHandlerFor[In, any] {
	cfg := toolConfig{name: tool.Name}
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
		schema, err := wrapSchema(rt)
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
	// Only the open object admits a refusal's {"error": msg}.
	canRefuse := isOpenObject(t.OutputSchema)

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
			var arg *ArgError
			switch {
			case errors.As(err, &ref) && canRefuse:
				return refusalResult(ref.Msg), nil, nil
			case errors.As(err, &arg) && canRefuse:
				// A dict tool answers every bad argument as {"error": msg}
				// (Go change 11): the helper makes it uniform, so no dict tool
				// can leak an isError for a bad argument.
				return refusalResult(arg.Msg), nil, nil
			case errors.As(err, &arg):
				logger(ctx).WarnContext(ctx, "mcp tool bad argument", "tool", t.Name, "msg", arg.Msg)
				res := &mcp.CallToolResult{}
				res.SetError(errors.New(arg.Msg))
				return res, nil, nil
			case ref != nil:
				// A refusal from a tool whose schema cannot carry it is a bug:
				// the client gets the generic error and the cause is reported.
				err = fmt.Errorf("tool %s refused (%s) but its outputSchema is not the open object: %w", t.Name, ref.Msg, err)
			}
			logger(ctx).ErrorContext(ctx, "mcp tool failed", "tool", t.Name, "err", err)
			if !isCancellation(ctx, err) {
				observe.Report(fmt.Errorf("mcp tool %s: %w", t.Name, err), map[string]string{"component": "mcp"})
			}
			return unexpectedResult(), nil, nil
		}
		// The result is built here, with no Out for the SDK: it would decode
		// the validated output into a map and write it back with its keys
		// sorted, losing the order the tool built them in (Python's dict
		// order). The output was already validated against the schema.
		return &mcp.CallToolResult{
			StructuredContent: raw,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		}, nil, nil
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
	hooks := &commitHooks{}
	ctx = context.WithValue(ctx, commitHooksKey{}, hooks)
	if cfg.noTx {
		// A tool registered withoutTx commits its own work (trigger_recurring:
		// TriggerAndSummarise) and registers a hook only once that work is
		// committed, so a registered hook always stands for committed rows:
		// it runs even when building the result fails afterwards (pick inside
		// the body, or marshalOut here). A panic still runs none.
		o, err := fn(ctx, nil, p, in)
		var raw json.RawMessage
		if err == nil {
			raw, err = marshalOut(o, schema)
		}
		hooks.run(ctx, cfg.name)
		if err != nil {
			return nil, err
		}
		return raw, nil
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
	// The body has finished, and may have acted outside the database (an
	// email sent): a request cancelled now must not roll that back, so the
	// commit ignores the request's cancellation.
	if err := tx.Commit(context.WithoutCancel(ctx)); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	hooks.run(ctx, cfg.name)
	return raw, nil
}

// commitHooksKey carries a call's commitHooks in its context.
type commitHooksKey struct{}

// commitHooks are the functions a tool body asked to run once its
// transaction has committed.
type commitHooks struct{ fns []func(context.Context) }

// afterCommit registers fn to run after the tool's transaction committed
// (after marshalling, for a tool registered withoutTx, which has none): a
// refusal, an error or a panic before the commit runs none of them. For the
// side effects that must see the committed rows and must never fail a write
// that already stands, such as the hosting restore after a payment clears.
// fn gets the call's context without its cancellation; a panic in it is
// logged and reported and never reaches the client. Only a tool body calls it.
func afterCommit(ctx context.Context, fn func(context.Context)) {
	h, ok := ctx.Value(commitHooksKey{}).(*commitHooks)
	if !ok {
		panic("mcp: afterCommit outside a tool call")
	}
	h.fns = append(h.fns, fn)
}

// run calls each hook in order, each one shielded from a panic.
func (h *commitHooks) run(ctx context.Context, tool string) {
	ctx = context.WithoutCancel(ctx)
	for _, fn := range h.fns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger(ctx).ErrorContext(ctx, "mcp after-commit hook panicked", "tool", tool, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
					observe.Report(fmt.Errorf("mcp tool %s after-commit hook panic: %v", tool, r), map[string]string{"component": "mcp"})
				}
			}()
			fn(ctx)
		}()
	}
}

// marshalOut marshals the tool's output and validates the JSON (not the Go
// value, so a field with a custom JSON form such as decimal.Decimal is
// checked as the client sees it) against the declared outputSchema. The SDK
// would validate it too, but only after the commit; failing here rolls back.
func marshalOut(o any, schema *jsonschema.Resolved) (json.RawMessage, error) {
	// No HTML escaping and no trailing newline: Python's JSON writes < > and &
	// as they are (refusalResult does the same).
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(o); err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}
	raw := json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
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
