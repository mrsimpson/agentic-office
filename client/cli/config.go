package cli

// Configuration files of the client (concept-client-configuration).
//
// A setting has one key, for example "agent.wake.url". It can come from four layers. A higher
// layer overrides a lower one, key by key:
//
//  1. the user file: config.yaml in the configuration folder of the client
//     (coreapi.ClientConfigDir);
//  2. the project file: .office/config.yaml in the start directory;
//  3. environment variables;
//  4. command-line flags (and the harness command after "--").
//
// The files are YAML. The form of a file is fileConfig: every key has a field there, so an
// unknown key or a wrong type is an error that names the file, the line and the full key. A new
// section for another command is a new struct in fileConfig; loadConfig and "office config show"
// need no change.
//
// The keys hub, tenant, ca-cert, key and hub-profile and the mapping hubs (named hub profiles)
// set the connection to a hub. connection.go resolves them, since a profile takes part in the
// order of the layers.
//
// A file never holds a secret. It names the environment variable that holds a key
// (agent.wake.key-env); the CLI reads the key from the environment. The key of the connection
// (key) is the path of a key file, never the key.
//
// A relative path in a file is relative to the root of that file: the start directory for the
// project file, the configuration folder for the user file. A relative path in a flag is relative
// to the current directory, as usual.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// ConfigFileName is the name of the user file and of the project file.
const ConfigFileName = "config.yaml"

// projectConfigDir is the folder of the project file in the start directory. It also holds the
// vocabulary of the ears.
const projectConfigDir = ".office"

// fileConfig is the form of a configuration file, used as a schema only: a struct is a mapping,
// and a field of type string, []string or float64 is a key with a value of that type. The tag
// office:"path" marks a path, which is resolved against the root of the file; office:"duration"
// marks a duration in the form of Go, such as "10s".
type fileConfig struct {
	Hub        string                 `yaml:"hub"`
	Tenant     string                 `yaml:"tenant"`
	CACert     string                 `yaml:"ca-cert" office:"path"`
	Key        string                 `yaml:"key" office:"path"`
	HubProfile string                 `yaml:"hub-profile"`
	Hubs       map[string]profileFile `yaml:"hubs"`
	Agent      agentFile              `yaml:"agent"`
}

// profileFile is one entry of the mapping hubs: a named hub profile.
type profileFile struct {
	Hub    string `yaml:"hub"`
	Tenant string `yaml:"tenant"`
	CACert string `yaml:"ca-cert" office:"path"`
	Key    string `yaml:"key" office:"path"`
}

// agentFile is the mapping agent: the options of "office agent run".
type agentFile struct {
	Session   string   `yaml:"session"`
	Harness   []string `yaml:"harness"`
	Role      string   `yaml:"role"`
	RoleFile  string   `yaml:"role-file" office:"path"`
	Allow     []string `yaml:"allow"`
	Cwd       string   `yaml:"cwd" office:"path"`
	TopicTask string   `yaml:"topic-task"`
	Wake      wakeFile `yaml:"wake"`
}

// wakeFile is the mapping agent.wake: the wake strategy and its model.
type wakeFile struct {
	Strategy  string  `yaml:"strategy"`
	Command   string  `yaml:"command"`
	URL       string  `yaml:"url"`
	Model     string  `yaml:"model"`
	Threshold float64 `yaml:"threshold"`
	Timeout   string  `yaml:"timeout" office:"duration"`
	KeyEnv    string  `yaml:"key-env"`
}

// The keys of the connection. The fields of a profile have the same names under hubs.<name>.
const (
	keyHub        = "hub"
	keyTenant     = "tenant"
	keyCACert     = "ca-cert"
	keyKeyFile    = "key"
	keyHubProfile = "hub-profile"
	keyHubs       = "hubs"
)

// connectionFields are the settings of a hub that a profile holds.
var connectionFields = []string{keyHub, keyTenant, keyCACert, keyKeyFile}

// isConnectionKey reports whether key belongs to the connection (connection.go), not to the
// settings of a command.
func isConnectionKey(key string) bool {
	return key == keyHubProfile || contains(connectionFields, key) || strings.HasPrefix(key, keyHubs+".")
}

// The keys of the settings.
const (
	keySession   = "agent.session"
	keyHarness   = "agent.harness"
	keyRole      = "agent.role"
	keyRoleFile  = "agent.role-file"
	keyAllow     = "agent.allow"
	keyCwd       = "agent.cwd"
	keyTopicTask = "agent.topic-task"
	keyStrategy  = "agent.wake.strategy"
	keyCommand   = "agent.wake.command"
	keyURL       = "agent.wake.url"
	keyModel     = "agent.wake.model"
	keyThreshold = "agent.wake.threshold"
	keyTimeout   = "agent.wake.timeout"
	keyKeyEnv    = "agent.wake.key-env"
)

// alternatives are keys that exclude each other. One layer may set only one key of a group. A
// higher layer that sets one key of a group removes the others of lower layers.
var alternatives = [][]string{{keyRole, keyRoleFile}, {keyCommand, keyURL}}

// wakeKeys are the keys of agent.wake that each strategy uses. key-env goes with every
// strategy, since it only names a variable.
var wakeKeys = map[string][]string{
	WakeTurnEnd:       nil,
	WakeLLM:           {keyCommand, keyURL, keyModel, keyTimeout},
	WakeDecisionModel: {keyURL, keyModel, keyThreshold, keyTimeout},
}

// The defaults of the settings that have one.
var configDefaults = map[string]any{
	keyStrategy:  WakeTurnEnd,
	keyThreshold: 0.5,
	keyTimeout:   10 * time.Second,
	keyKeyEnv:    EnvDecideKey,
}

// Kinds of a source, from the lowest layer to the highest.
const (
	sourceDefault = "default"
	sourceUser    = "user"
	sourceProject = "project"
	sourceEnv     = "env"
	sourceFlag    = "flag"
	// sourceProfile marks a value that a hub profile gives.
	sourceProfile = "profile"
	// sourceDefinition marks a value that the definition of a team agent gives.
	sourceDefinition = "definition"
)

// source says where a setting comes from: the kind of layer and the file, the variable or the
// flag. A value from a hub profile has the kind sourceProfile, the name of the profile and the
// file that defines the value.
type source struct {
	Kind    string `json:"source"`
	From    string `json:"from,omitempty"`
	Profile string `json:"profile,omitempty"`
}

func (s source) String() string {
	switch s.Kind {
	case sourceUser:
		return "user file " + s.From
	case sourceProject:
		return "project file " + s.From
	case sourceEnv:
		return "environment variable " + s.From
	case sourceFlag:
		return s.From
	case sourceProfile:
		return "hub profile " + s.Profile + " (" + s.From + ")"
	case sourceDefinition:
		return s.From
	}
	return "default"
}

// setting is a value with its source.
type setting struct {
	Value  any
	Source source
}

// settings are the effective settings by key.
type settings map[string]setting

// layer is the set of values of one source.
type layer struct {
	src    source
	values map[string]any
	// srcs gives the source of single values, when they differ from src: the layers of the
	// environment and of the flags have one source per value.
	srcs map[string]source
}

// configFileInfo describes one configuration file.
type configFileInfo struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Found bool   `json:"found"`
}

// configFiles returns the user file and the project file of the start directory start.
func configFiles(start string) []configFileInfo {
	return []configFileInfo{
		{Kind: sourceUser, Path: filepath.Join(coreapi.ClientConfigDir(), ConfigFileName)},
		{Kind: sourceProject, Path: filepath.Join(start, projectConfigDir, ConfigFileName)},
	}
}

// readLayers reads the user file and the project file of the start directory start, in this
// order from low to high. A file that does not exist gives no layer. It also returns the files
// it looked for.
func readLayers(start string) ([]layer, []configFileInfo, error) {
	files := configFiles(start)
	var layers []layer
	for i, f := range files {
		root := filepath.Dir(f.Path) // the configuration folder
		if f.Kind == sourceProject {
			root = start
		}
		l, found, err := readConfigFile(f.Path, root, source{Kind: f.Kind, From: f.Path})
		if err != nil {
			return nil, nil, err
		}
		files[i].Found = found
		if found {
			layers = append(layers, l)
		}
	}
	return layers, files, nil
}

// loadConfig reads the configuration files for the start directory start and returns the
// settings of a command from the defaults and the files, and the files it looked for. env and
// flags are applied on top with apply. The connection is not part of it (see loadConnection).
func loadConfig(start string) (settings, []configFileInfo, error) {
	s := settings{}
	for k, v := range configDefaults {
		s[k] = setting{Value: v, Source: source{Kind: sourceDefault}}
	}
	layers, files, err := readLayers(start)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range layers {
		s.apply(l)
	}
	return s, files, nil
}

// readConfigFile reads one file into a layer. A missing file is no error (found is false). Paths
// are resolved against root.
func readConfigFile(path, root string, src source) (layer, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return layer{}, false, nil
	}
	if err != nil {
		return layer{}, false, usagef("configuration file %s: %v", path, err)
	}
	l, err := parseConfig(b, path, root, src)
	return l, err == nil, err
}

// parseConfig checks the content b of the file path and returns its layer.
func parseConfig(b []byte, path, root string, src source) (layer, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return layer{}, usagef("configuration file %s: %s", path, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	l := layer{src: src, values: map[string]any{}}
	if len(doc.Content) > 0 { // an empty file or one with comments only has no content
		if err := walkConfig(doc.Content[0], reflect.TypeOf(fileConfig{}), "", root, l.values); err != nil {
			return layer{}, usagef("configuration file %s, %v", path, err)
		}
	}
	if err := l.check(); err != nil {
		return layer{}, usagef("configuration file %s: %v", path, err)
	}
	return l, nil
}

// walkConfig checks the mapping n against the schema t and puts the values it sets into out,
// by their full key. An unknown key, a wrong type or a missing value is an error with the line
// and the full key. The check is as strict as decoding with KnownFields, but names the full key.
func walkConfig(n *yaml.Node, t reflect.Type, prefix, root string, out map[string]any) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil // an empty mapping, such as "agent:" without keys
	}
	if n.Kind != yaml.MappingNode {
		if prefix == "" {
			return fmt.Errorf("line %d: want a mapping of keys", n.Line)
		}
		return fmt.Errorf("line %d, key %s: want a mapping of keys", n.Line, strings.TrimSuffix(prefix, "."))
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		key := prefix + k.Value
		where := fmt.Sprintf("line %d, key %s", k.Line, key)
		f, ok := schemaField(t, k.Value)
		if !ok {
			return fmt.Errorf("line %d: unknown key %s", k.Line, key)
		}
		switch f.Type.Kind() {
		case reflect.Map:
			if err := walkProfiles(v, f.Type.Elem(), key, root, where, out); err != nil {
				return err
			}
		case reflect.Struct:
			if err := walkConfig(v, f.Type, key+".", root, out); err != nil {
				return err
			}
		case reflect.String:
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
				if f.Tag.Get("office") == "duration" {
					return fmt.Errorf("%s: want a duration such as \"10s\"", where)
				}
				return fmt.Errorf("%s: want a text", where)
			}
			val := v.Value
			if f.Tag.Get("office") == "path" && val != "" && !filepath.IsAbs(val) {
				val = filepath.Join(root, val)
			}
			out[key] = val
		case reflect.Float64:
			var x float64
			if v.Kind != yaml.ScalarNode || (v.Tag != "!!float" && v.Tag != "!!int") || v.Decode(&x) != nil {
				return fmt.Errorf("%s: want a number", where)
			}
			out[key] = x
		case reflect.Slice:
			if v.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s: want a list of texts", where)
			}
			list := []string{}
			for _, e := range v.Content {
				if e.Kind != yaml.ScalarNode || e.Tag != "!!str" {
					return fmt.Errorf("line %d, key %s: want a list of texts", e.Line, key)
				}
				list = append(list, e.Value)
			}
			out[key] = list
		}
	}
	return nil
}

// profileName is the form of the name of a hub profile. It also names the certificate file of
// the profile, so it has no dot and no separator.
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// walkProfiles checks the mapping n of named entries against the schema t. A name is a free
// key. out gets the marker prefix.<name> = true for each entry and the values as prefix.<name>.<field>.
func walkProfiles(n *yaml.Node, t reflect.Type, prefix, root, where string, out map[string]any) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: want a mapping of names", where)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !profileName.MatchString(k.Value) {
			return fmt.Errorf("line %d, key %s.%s: a name has letters, digits, \"-\" and \"_\" (at most 64, not starting with a dash or underscore)", k.Line, prefix, k.Value)
		}
		out[prefix+"."+k.Value] = true
		if err := walkConfig(v, t, prefix+"."+k.Value+".", root, out); err != nil {
			return err
		}
	}
	return nil
}

// schemaField returns the field of the struct t with the YAML name name.
func schemaField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.Tag.Get("yaml") == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// check checks the values of one file: their form, the connection, the alternatives, and that the keys of
// agent.wake fit the strategy of the same file.
func (l layer) check() error {
	if err := l.checkConnection(); err != nil {
		return err
	}
	for _, g := range alternatives {
		var set []string
		for _, k := range g {
			if _, ok := l.values[k]; ok {
				set = append(set, k)
			}
		}
		if len(set) > 1 {
			return fmt.Errorf("give %s, not both", strings.Join(set, " or "))
		}
	}
	if v, ok := l.values[keyStrategy].(string); ok {
		fit, known := wakeKeys[v]
		if !known {
			return fmt.Errorf("key %s: must be %s, %s or %s", keyStrategy, WakeTurnEnd, WakeLLM, WakeDecisionModel)
		}
		for _, k := range []string{keyCommand, keyURL, keyModel, keyThreshold, keyTimeout} {
			if _, ok := l.values[k]; ok && !contains(fit, k) {
				return fmt.Errorf("key %s does not go with %s = %q", k, keyStrategy, v)
			}
		}
	}
	if v, ok := l.values[keyThreshold].(float64); ok && (v <= 0 || v > 1) {
		return fmt.Errorf("key %s: must be above 0 and at most 1", keyThreshold)
	}
	if v, ok := l.values[keyTimeout].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("key %s: want a positive duration such as \"10s\", got %q", keyTimeout, v)
		}
		l.values[keyTimeout] = d
	}
	if v, ok := l.values[keyKeyEnv].(string); ok && !envName.MatchString(v) {
		return fmt.Errorf("key %s: want the name of an environment variable, such as %q (never the key itself)", keyKeyEnv, EnvDecideKey)
	}
	return nil
}

// apply puts the values of l over s. A key of a group of alternatives removes the other keys of
// its group.
func (s settings) apply(l layer) {
	for k, v := range l.values {
		if isConnectionKey(k) {
			continue
		}
		for _, g := range alternatives {
			if contains(g, k) {
				for _, other := range g {
					if other != k {
						delete(s, other)
					}
				}
			}
		}
		s[k] = setting{Value: v, Source: l.src}
	}
}

// str returns the string value of key, or "".
func (s settings) str(key string) string { v, _ := s[key].Value.(string); return v }

// list returns the list value of key, or nil.
func (s settings) list(key string) []string { v, _ := s[key].Value.([]string); return v }

// has reports whether key is set.
func (s settings) has(key string) bool { _, ok := s[key]; return ok }

// keys returns the keys of s in order.
func (s settings) keys() []string {
	ks := make([]string, 0, len(s))
	for k := range s {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// unused returns the keys of agent.wake that the effective strategy does not use, except for
// defaults.
func (s settings) unused() []string {
	fit := wakeKeys[s.str(keyStrategy)]
	var out []string
	for _, k := range []string{keyCommand, keyURL, keyModel, keyThreshold, keyTimeout} {
		if s.has(k) && s[k].Source.Kind != sourceDefault && !contains(fit, k) {
			out = append(out, k)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// configValue returns a value for output: a duration as text, everything else as it is.
func configValue(v any) any {
	if d, ok := v.(time.Duration); ok {
		return d.String()
	}
	return v
}
