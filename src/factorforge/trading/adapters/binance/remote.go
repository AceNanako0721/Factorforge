package binance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// RemoteProbe keeps exchange credentials inside a native signer in an empty
// network namespace. Its line protocol admits the fixed Broker surface only.
type RemoteProbe struct {
	process   *exec.Cmd
	input     io.WriteCloser
	output    *bufio.Scanner
	done      chan error
	closeOnce sync.Once
	mu        sync.Mutex
}
type probeReply struct {
	OK    json.RawMessage `json:"ok"`
	Error string          `json:"error,omitempty"`
}
type probeMessage struct {
	Operation string            `json:"operation"`
	Args      []json.RawMessage `json:"args"`
}

func StartRemoteProbe(ctx context.Context, binary string, manifest ProbeManifest, stderr io.Writer) (*RemoteProbe, error) {
	process := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--net", binary, "--signer")
	process.Stderr = stderr
	input, err := process.StdinPipe()
	if err != nil {
		return nil, reject("TESTNET_SIGNER_STOPPED", 503)
	}
	output, err := process.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, reject("TESTNET_SIGNER_STOPPED", 503)
	}
	if err = process.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, reject("TESTNET_SIGNER_STOPPED", 503)
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 65536), 64<<20)
	remote := &RemoteProbe{process: process, input: input, output: scanner, done: make(chan error, 1)}
	go func() { remote.done <- process.Wait() }()
	if json.NewEncoder(input).Encode(manifest) != nil {
		remote.Close()
		return nil, reject("TESTNET_SIGNER_STOPPED", 503)
	}
	return remote, nil
}
func (r *RemoteProbe) PID() int { return r.process.Process.Pid }
func (r *RemoteProbe) Close() {
	r.closeOnce.Do(func() {
		r.input.Close()
		r.process.Process.Signal(syscall.SIGTERM)
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			r.process.Process.Kill()
			<-r.done
		}
	})
}
func (r *RemoteProbe) Call(ctx context.Context, operation string, args ...any) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	message := probeMessage{Operation: operation, Args: []json.RawMessage{}}
	for _, value := range args {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		message.Args = append(message.Args, data)
	}
	type response struct {
		data json.RawMessage
		err  error
	}
	done := make(chan response, 1)
	go func() {
		if json.NewEncoder(r.input).Encode(message) != nil || !r.output.Scan() {
			done <- response{err: reject("TESTNET_SIGNER_STOPPED", 503)}
			return
		}
		var reply probeReply
		if json.Unmarshal(r.output.Bytes(), &reply) != nil {
			done <- response{err: reject("TESTNET_PROTOCOL_INVALID", 503)}
			return
		}
		if reply.Error == "AMBIGUOUS_RESULT" {
			done <- response{err: &ports.Ambiguous{}}
			return
		}
		if reply.Error != "" {
			done <- response{err: reject(reply.Error, 423)}
			return
		}
		done <- response{data: reply.OK}
	}()
	select {
	case result := <-done:
		return result.data, result.err
	case <-ctx.Done():
		r.process.Process.Kill()
		<-done
		return nil, reject("TESTNET_SIGNER_STOPPED", 503)
	}
}
func (r *RemoteProbe) Environment() string     { return "LIVE" }
func (r *RemoteProbe) ExecutionAdmitted() bool { return true }
func (r *RemoteProbe) Capabilities() map[string]any {
	return map[string]any{"overlapping_protections": true, "atomic_protection_modify": false, "live_ready": false, "probe_only": true}
}
func remoteValue[T any](ctx context.Context, r *RemoteProbe, operation string, args ...any) (T, error) {
	var value T
	raw, err := r.Call(ctx, operation, args...)
	if err == nil && json.Unmarshal(raw, &value) != nil {
		err = reject("TESTNET_PROTOCOL_INVALID", 503)
	}
	return value, err
}
func (r *RemoteProbe) order(ctx context.Context, op string, args ...any) (*d.Order, []d.Fill, error) {
	raw, err := remoteValue[[]json.RawMessage](ctx, r, op, args...)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) != 2 {
		return nil, nil, reject("TESTNET_PROTOCOL_INVALID", 503)
	}
	var order d.Order
	var fills []d.Fill
	if json.Unmarshal(raw[0], &order) != nil || json.Unmarshal(raw[1], &fills) != nil {
		return nil, nil, reject("TESTNET_PROTOCOL_INVALID", 503)
	}
	return &order, fills, nil
}
func (r *RemoteProbe) SubmitOrder(c context.Context, a *d.Aggregate, o *d.Order) (*d.Order, []d.Fill, error) {
	return r.order(c, "submit_order", a, o)
}
func (r *RemoteProbe) CancelOrder(c context.Context, a *d.Aggregate, o *d.Order) (*d.Order, []d.Fill, error) {
	return r.order(c, "cancel_order", a, o)
}
func (r *RemoteProbe) QueryOrder(c context.Context, a *d.Aggregate, id string) (*d.Order, []d.Fill, error) {
	return r.order(c, "query_order", a, id)
}
func (r *RemoteProbe) SubmitProtection(c context.Context, a *d.Aggregate, p *d.Protection) (*d.Protection, error) {
	return remoteValue[*d.Protection](c, r, "submit_protection", a, p)
}
func (r *RemoteProbe) CancelProtection(c context.Context, a *d.Aggregate, id string) (*d.Protection, error) {
	return remoteValue[*d.Protection](c, r, "cancel_protection", a, id)
}
func (r *RemoteProbe) QueryProtection(c context.Context, a *d.Aggregate, id string) (*d.Protection, error) {
	return remoteValue[*d.Protection](c, r, "query_protection", a, id)
}
func (r *RemoteProbe) ListOpenOrders(c context.Context, a *d.Aggregate) ([]*d.Order, error) {
	return remoteValue[[]*d.Order](c, r, "list_open_orders", a)
}
func (r *RemoteProbe) ListFills(c context.Context, a *d.Aggregate) ([]*d.Fill, error) {
	return remoteValue[[]*d.Fill](c, r, "list_fills", a)
}
func (r *RemoteProbe) GetPositions(c context.Context, a *d.Aggregate) ([]*d.Position, error) {
	return remoteValue[[]*d.Position](c, r, "get_positions", a)
}
func (r *RemoteProbe) GetAccount(c context.Context, a *d.Aggregate) (map[string]any, error) {
	return remoteValue[map[string]any](c, r, "get_account", a)
}
func (r *RemoteProbe) GetIncome(c context.Context, a *d.Aggregate) ([]*d.Income, error) {
	return remoteValue[[]*d.Income](c, r, "get_income", a)
}

// ServeProbe is called by the isolated native signer after its manifest and
// private configuration have been checked. Provider errors are never echoed.
func ServeProbe(ctx context.Context, input io.Reader, output io.Writer, p *ProbeTransport) error {
	broker := &Broker{Transport: p, Admitted: true, Verified: map[string]any{"ordinary_and_conditional_verified": true, "overlapping_protections": true, "probe_only": true, "live_ready": false}}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 65536), 64<<20)
	for scanner.Scan() {
		var message probeMessage
		var result any
		var err error
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			err = reject("TESTNET_PROTOCOL_INVALID", 422)
		} else {
			result, err = dispatchProbe(ctx, message, p, broker)
		}
		reply := probeReply{}
		if err != nil {
			var failure *d.Error
			var ambiguous *ports.Ambiguous
			switch {
			case errors.As(err, &ambiguous):
				reply.Error = "AMBIGUOUS_RESULT"
			case errors.As(err, &failure):
				reply.Error = failure.Code
			default:
				reply.Error = "TESTNET_PROCESS_ERROR"
			}
		} else {
			reply.OK, err = json.Marshal(result)
			if err != nil {
				reply.Error = "TESTNET_PROTOCOL_INVALID"
			}
		}
		if json.NewEncoder(output).Encode(reply) != nil {
			return reject("TESTNET_SIGNER_STOPPED", 503)
		}
	}
	return nil
}
func dispatchProbe(ctx context.Context, m probeMessage, p *ProbeTransport, b *Broker) (any, error) {
	decode := func(i int, value any) error {
		if i >= len(m.Args) || json.Unmarshal(m.Args[i], value) != nil {
			return reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		return nil
	}
	switch m.Operation {
	case "claim":
		return p.Claim(ctx)
	case "drop_response":
		p.DropResponse()
		return true, nil
	case "stats":
		return p.Stats(), nil
	case "fence_probe":
		return true, p.Require(ctx)
	case "raw":
		var path string
		var params Params
		if decode(0, &path) != nil || decode(1, &params) != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		return p.Request(ctx, "GET", path, params, false)
	case "manual_reduce":
		var params Params
		if decode(0, &params) != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		return p.Request(ctx, "POST", Ordinary, params, true)
	}
	if err := p.Renew(ctx); err != nil {
		return nil, err
	}
	var run d.Aggregate
	if decode(0, &run) != nil || run.RunKey != p.Manifest.Key || d.Validate(run) != nil {
		return nil, reject("TESTNET_ACCOUNT_SCOPE", 403)
	}
	if m.Operation == "submit_order" || m.Operation == "cancel_order" {
		var order d.Order
		if decode(1, &order) != nil || d.Validate(order) != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		var result *d.Order
		var fills []d.Fill
		var err error
		if m.Operation == "submit_order" {
			result, fills, err = b.SubmitOrder(ctx, &run, &order)
		} else {
			result, fills, err = b.CancelOrder(ctx, &run, &order)
		}
		return []any{result, fills}, err
	}
	if m.Operation == "submit_protection" {
		var item d.Protection
		if decode(1, &item) != nil || d.Validate(item) != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
		return b.SubmitProtection(ctx, &run, &item)
	}
	var id string
	switch m.Operation {
	case "query_order", "query_protection", "cancel_protection":
		if decode(1, &id) != nil {
			return nil, reject("TESTNET_PROTOCOL_INVALID", 422)
		}
	}
	switch m.Operation {
	case "query_order":
		order, fills, err := b.QueryOrder(ctx, &run, id)
		return []any{order, fills}, err
	case "query_protection":
		return b.QueryProtection(ctx, &run, id)
	case "cancel_protection":
		return b.CancelProtection(ctx, &run, id)
	case "list_open_orders":
		return b.ListOpenOrders(ctx, &run)
	case "list_fills":
		return b.ListFills(ctx, &run)
	case "get_positions":
		return b.GetPositions(ctx, &run)
	case "get_account":
		return b.GetAccount(ctx, &run)
	case "get_income":
		return b.GetIncome(ctx, &run)
	default:
		return nil, reject("TESTNET_OPERATION_FORBIDDEN", 403)
	}
}
