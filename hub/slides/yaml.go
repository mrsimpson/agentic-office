package slides

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// outputFields are the fields of a deck that are not in its YAML file: the rendered output is
// the HTML file next to it.
var outputFields = map[string]bool{"media_type": true, "content": true}

// encode returns the YAML file of a deck: the fields of the contract in their order, in block
// style, without the output. Strings keep their quotes, so a title such as "2027" stays a string;
// a text with line breaks is a literal block if it reads back the same.
func encode(d *slidesv1.Deck) ([]byte, error) {
	input := proto.Clone(d).(*slidesv1.Deck)
	input.MediaType, input.Content = "", nil
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(input)
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
			return nil, errors.New("the deck is not a mapping")
		}
		m := doc.Content[0]
		var kept []*yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			if !outputFields[m.Content[i].Value] {
				kept = append(kept, m.Content[i], m.Content[i+1])
			}
		}
		m.Content = kept
		style(&doc, literal)
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return nil, err
		}
		if back, err := decode(out); err == nil && proto.Equal(back, input) {
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

// decode parses the YAML file of a deck. The output fields and unknown fields are ignored, so a
// newer hub can add fields.
func decode(data []byte) (*slidesv1.Deck, error) {
	var v map[string]any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	for k := range outputFields {
		delete(v, k)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := &slidesv1.Deck{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, d); err != nil {
		return nil, err
	}
	return d, nil
}
