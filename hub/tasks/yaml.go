package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
)

// commandsKey is the field of the stored form that holds the command IDs of a task. It is not
// part of the contract.
const commandsKey = "command_ids"

// encode returns the stored form of a task: the fields of the contract in their order, in block
// style, then command_ids. Strings keep their quotes, so an ID such as "123" stays a string; a
// text with line breaks is a literal block if it reads back the same.
func encode(t *taskv1.Task, commands []string) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(t)
	if err != nil {
		return nil, err
	}
	for _, literal := range []bool{true, false} {
		// JSON is YAML: parse it into a node, which keeps the order of the fields.
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return nil, errors.New("the task is not a mapping")
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, c := range commands {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: c, Style: yaml.DoubleQuotedStyle})
		}
		m := doc.Content[0]
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: commandsKey}, seq)
		style(&doc, literal)
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return nil, err
		}
		back, cmds, err := decode(out)
		if err == nil && proto.Equal(back, t) && strings.Join(cmds, "\n") == strings.Join(commands, "\n") {
			return out, nil
		}
	}
	return nil, fmt.Errorf("the YAML form does not read back the same")
}

func style(n *yaml.Node, literal bool) {
	n.Style &^= yaml.FlowStyle
	for i, c := range n.Content {
		switch {
		case n.Kind == yaml.MappingNode && i%2 == 0:
			c.Style = 0
		case literal && c.Kind == yaml.ScalarNode && c.Tag == "!!str" && strings.Contains(c.Value, "\n"):
			c.Style = yaml.LiteralStyle
		}
		style(c, literal)
	}
}

// decode parses the stored form of a task. Unknown fields are ignored, so a newer hub can add
// fields.
func decode(data []byte) (*taskv1.Task, []string, error) {
	var v map[string]any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, nil, err
	}
	var commands []string
	if raw, ok := v[commandsKey].([]any); ok {
		for _, c := range raw {
			s, ok := c.(string)
			if !ok || s == "" {
				return nil, nil, fmt.Errorf("%s holds a value that is not a command ID", commandsKey)
			}
			commands = append(commands, s)
		}
	} else if v[commandsKey] != nil {
		return nil, nil, fmt.Errorf("%s is not a list", commandsKey)
	}
	delete(v, commandsKey)
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	t := &taskv1.Task{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, t); err != nil {
		return nil, nil, err
	}
	return t, commands, nil
}
