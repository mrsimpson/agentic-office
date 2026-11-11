// Command acpdouble is a test double of an agent harness that speaks the Agent Client Protocol
// (ACP) on stdin and stdout. The tests of the agent runner start it instead of Claude Code,
// opencode or Kiro. It calls no model.
//
// It writes the raw JSON-RPC itself, without an ACP library, so it checks the runner against
// the protocol and not against the library that the runner uses.
//
// What it does:
//
//   - initialize: answers with protocol version 1.
//   - session/new: connects to the first MCP server of the request as an MCP client.
//   - session/prompt: records the prompt and follows instructions in its text, in order:
//     `please say "TEXT"` asks for permission for the office tool utterance_say and, if
//     allowed, calls it with TEXT; `please run "CMD"` asks for permission for a shell command
//     (kind execute) and never runs it; `please wait DURATION` sleeps, so the agent is busy.
//     Then it streams a short reply and ends the turn.
//   - session/cancel: ends a waiting prompt with the stop reason cancelled.
//   - End of stdin: records "exit" and exits.
//
// Every step is one JSON line in the file ACP_DOUBLE_RECORD: {"kind": …, …}.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type double struct {
	outMu   sync.Mutex
	out     *os.File
	recMu   sync.Mutex
	rec     *os.File
	nextID  int
	pendMu  sync.Mutex
	pending map[string]chan message
	mcp     *mcp.ClientSession
	cancel  chan struct{} // closed by session/cancel for the running prompt
	cmu     sync.Mutex
	prompts int
}

func main() {
	d := &double{out: os.Stdout, pending: map[string]chan message{}}
	if p := os.Getenv("ACP_DOUBLE_RECORD"); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "acpdouble:", err)
			os.Exit(1)
		}
		d.rec = f
	}
	d.record(map[string]any{"kind": "start", "pid": os.Getpid()})
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			d.record(map[string]any{"kind": "bad_json", "line": sc.Text()})
			continue
		}
		switch {
		case m.Method != "" && m.ID != nil:
			go d.handle(m)
		case m.Method != "":
			d.notify(m)
		case m.ID != nil:
			d.pendMu.Lock()
			ch := d.pending[string(*m.ID)]
			delete(d.pending, string(*m.ID))
			d.pendMu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	if d.mcp != nil {
		_ = d.mcp.Close()
	}
	d.record(map[string]any{"kind": "exit"})
}

func (d *double) record(v map[string]any) {
	if d.rec == nil {
		return
	}
	v["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(v)
	d.recMu.Lock()
	defer d.recMu.Unlock()
	_, _ = d.rec.Write(append(b, '\n'))
}

func (d *double) send(m message) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	d.outMu.Lock()
	defer d.outMu.Unlock()
	_, _ = d.out.Write(append(b, '\n'))
}

func (d *double) reply(id *json.RawMessage, result any) {
	b, _ := json.Marshal(result)
	d.send(message{ID: id, Result: b})
}

func (d *double) replyError(id *json.RawMessage, code int, msg string) {
	m := message{ID: id}
	m.Error = &struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{code, msg}
	d.send(m)
}

// request sends a request to the client and waits for its answer.
func (d *double) request(method string, params any) (message, error) {
	d.pendMu.Lock()
	d.nextID++
	id := json.RawMessage(fmt.Sprintf(`"double-%d"`, d.nextID))
	ch := make(chan message, 1)
	d.pending[string(id)] = ch
	d.pendMu.Unlock()
	b, _ := json.Marshal(params)
	d.send(message{ID: &id, Method: method, Params: b})
	select {
	case m := <-ch:
		if m.Error != nil {
			return m, fmt.Errorf("%s: %d %s", method, m.Error.Code, m.Error.Message)
		}
		return m, nil
	case <-time.After(30 * time.Second):
		return message{}, fmt.Errorf("%s: no answer", method)
	}
}

func (d *double) notify(m message) {
	if m.Method == "session/cancel" {
		d.record(map[string]any{"kind": "cancel"})
		d.cmu.Lock()
		if d.cancel != nil {
			close(d.cancel)
			d.cancel = nil
		}
		d.cmu.Unlock()
	}
}

func (d *double) handle(m message) {
	switch m.Method {
	case "initialize":
		var p map[string]any
		_ = json.Unmarshal(m.Params, &p)
		d.record(map[string]any{"kind": "initialize", "params": p})
		d.reply(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{},
			"agentInfo": map[string]any{"name": "acpdouble", "version": "1"}, "authMethods": []any{}})
	case "session/new":
		var p struct {
			Cwd        string `json:"cwd"`
			McpServers []struct {
				Name    string   `json:"name"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Env     []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			d.replyError(m.ID, -32602, err.Error())
			return
		}
		var raw map[string]any
		_ = json.Unmarshal(m.Params, &raw)
		d.record(map[string]any{"kind": "session/new", "params": raw})
		if len(p.McpServers) > 0 {
			s := p.McpServers[0]
			cmd := exec.Command(s.Command, s.Args...)
			cmd.Env = os.Environ()
			for _, e := range s.Env {
				cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
			}
			cmd.Stderr = os.Stderr
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "acpdouble", Version: "1"}, nil).
				Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
			if err != nil {
				d.record(map[string]any{"kind": "mcp_error", "error": err.Error()})
				d.replyError(m.ID, -32603, "MCP server: "+err.Error())
				return
			}
			d.mcp = cs
		}
		d.reply(m.ID, map[string]any{"sessionId": "double-session"})
	case "session/prompt":
		d.prompt(m)
	default:
		d.replyError(m.ID, -32601, "method not found: "+m.Method)
	}
}

var instruction = regexp.MustCompile(`please (say|run) "([^"]*)"|please wait ([0-9a-z.]+)`)

func (d *double) prompt(m message) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var text strings.Builder
	for _, b := range p.Prompt {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	d.cmu.Lock()
	d.prompts++
	n := d.prompts
	cancel := make(chan struct{})
	d.cancel = cancel
	d.cmu.Unlock()
	d.record(map[string]any{"kind": "prompt", "n": n, "session": p.SessionID, "text": text.String()})

	stop := "end_turn"
	for _, in := range instruction.FindAllStringSubmatch(text.String(), -1) {
		switch {
		case in[1] == "say":
			d.tool(n, "mcp__office__utterance_say", "other", map[string]any{"text": in[2]})
		case in[1] == "run":
			d.tool(n, "Run "+in[2], "execute", map[string]any{"command": in[2]})
		case in[3] != "":
			dur, err := time.ParseDuration(in[3])
			if err != nil {
				continue
			}
			select {
			case <-time.After(dur):
			case <-cancel:
				stop = "cancelled"
			}
		}
	}
	d.sendUpdate(p.SessionID, map[string]any{"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{"type": "text", "text": fmt.Sprintf("Prompt %d read.", n)}})
	d.record(map[string]any{"kind": "prompt_end", "n": n, "stop": stop})
	d.reply(m.ID, map[string]any{"stopReason": stop})
}

func (d *double) sendUpdate(sid string, update map[string]any) {
	b, _ := json.Marshal(map[string]any{"sessionId": sid, "update": update})
	d.send(message{Method: "session/update", Params: b})
}

// tool announces a tool call, asks for permission and, for an allowed office tool, calls it.
func (d *double) tool(n int, title, kind string, input map[string]any) {
	id := fmt.Sprintf("call-%d-%d", n, time.Now().UnixNano())
	d.sendUpdate("double-session", map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": title,
		"kind": kind, "status": "pending", "rawInput": input})
	res, err := d.request("session/request_permission", map[string]any{
		"sessionId": "double-session",
		"toolCall":  map[string]any{"toolCallId": id, "title": title, "kind": kind, "rawInput": input},
		"options": []map[string]any{
			{"optionId": "allow", "name": "Allow once", "kind": "allow_once"},
			{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		},
	})
	outcome := ""
	if err != nil {
		outcome = "error: " + err.Error()
	} else {
		var r struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(res.Result, &r)
		outcome = r.Outcome.Outcome + ":" + r.Outcome.OptionID
	}
	rec := map[string]any{"kind": "permission", "n": n, "title": title, "toolKind": kind, "outcome": outcome}
	if outcome == "selected:allow" && kind != "execute" && d.mcp != nil {
		name := title[strings.LastIndex(title, "__")+2:]
		r, err := d.mcp.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: input})
		switch {
		case err != nil:
			rec["toolError"] = err.Error()
		case r.IsError:
			rec["toolError"] = r.Content[0].(*mcp.TextContent).Text
		default:
			rec["toolResult"] = r.Content[0].(*mcp.TextContent).Text
		}
	}
	d.record(rec)
	status := "completed"
	if outcome != "selected:allow" {
		status = "failed"
	}
	d.sendUpdate("double-session", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status})
}
