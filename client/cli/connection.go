package cli

// The connection of the client to a hub, and the named hub profiles
// (concept-client-configuration).
//
// A connection has four values: the hub address, the tenant, the certificate of the hub and the
// path of the private key. Each can come from the layers of configuration.go, with one addition:
// a layer can select a hub profile (key hub-profile, variable OFFICE_HUB_PROFILE, flag
// --hub-profile). A profile is a named set of the four values. The profiles of the user file and
// of the project file are merged key by key, like all settings.
//
// A profile takes part in the order of the layers. The profile that a layer selects gives its
// values at that layer, and the single values of the same layer (hub, tenant, ca-cert, key, or
// --hub, --tenant, --ca-cert, --key) override them. A higher layer overrides a lower one, and
// the highest layer that selects a profile decides which profile is active.
//
// A profile is a whole hub, so it replaces the hub, the tenant and the certificate of the lower
// layers: a value that the profile leaves out is empty (no tenant, the system roots). The key
// is the exception: a profile without a key uses the key that is set without a profile (a single
// key value, or the default key), since one person normally uses one key at several hubs.

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// hubProfile is a named hub profile, merged from the files.
type hubProfile struct {
	Name    string
	Values  map[string]string // by field: keyHub, keyTenant, keyCACert, keyKeyFile
	Sources map[string]source // the file that gives each value
	defined []string          // the files that have the profile, with or without values
}

// profileFiles returns the files that define the profile, in order of the layers.
func (p *hubProfile) files() []string {
	var out []string
	for _, f := range connectionFields {
		if s, ok := p.Sources[f]; ok && !contains(out, s.From) {
			out = append(out, s.From)
		}
	}
	return out
}

// connection is the result of the resolution.
type connection struct {
	// Settings holds the effective values by key: keyHub, keyTenant, keyCACert, keyKeyFile and,
	// when a profile is active, keyHubProfile. A tenant or a certificate that is not set is
	// missing.
	Settings settings
	// Profiles are all profiles of the files by name.
	Profiles map[string]*hubProfile
}

// Config returns the connection as part of the configuration of the client core. rest has the
// other values, such as the runtime directory.
func (c connection) Config(rest coreapi.Config) coreapi.Config {
	rest.Hub = c.Settings.str(keyHub)
	rest.Tenant = c.Settings.str(keyTenant)
	rest.CACert = c.Settings.str(keyCACert)
	rest.KeyFile = c.Settings.str(keyKeyFile)
	return rest
}

// checkConnection checks the values of the connection in one file: the form of the hub
// addresses and of the name of a profile.
func (l layer) checkConnection() error {
	for k, v := range l.values {
		text, ok := v.(string)
		switch {
		case !ok:
		case k == keyHub || (strings.HasPrefix(k, keyHubs+".") && strings.HasSuffix(k, "."+keyHub)):
			if err := checkHubURL(text); err != nil {
				return fmt.Errorf("key %s: %v", k, err)
			}
		case k == keyHubProfile && text != "" && !profileName.MatchString(text):
			return fmt.Errorf("key %s: want the name of a hub profile, got %q", k, text)
		}
	}
	return nil
}

// checkHubURL checks that s is the address of a hub: https or http, with a host.
func checkHubURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("want the address of a hub, such as https://hub.example.org:8740, got %q", s)
	}
	return nil
}

// buildProfiles merges the profiles of the file layers. A merged profile needs a hub address.
func buildProfiles(layers []layer) (map[string]*hubProfile, error) {
	profiles := map[string]*hubProfile{}
	for _, l := range layers {
		for k, v := range l.values {
			rest, ok := strings.CutPrefix(k, keyHubs+".")
			if !ok {
				continue
			}
			name, field, hasField := strings.Cut(rest, ".")
			p := profiles[name]
			if p == nil {
				p = &hubProfile{Name: name, Values: map[string]string{}, Sources: map[string]source{}}
				profiles[name] = p
			}
			if !hasField {
				p.defined = append(p.defined, l.src.From)
			} else {
				p.Values[field], _ = v.(string)
				p.Sources[field] = l.src
			}
		}
	}
	for _, name := range sortedNames(profiles) {
		if p := profiles[name]; p.Values[keyHub] == "" {
			return nil, usagef("configuration file %s: hub profile %q has no hub address: set key %s.%s.%s",
				strings.Join(p.defined, ", "), name, keyHubs, name, keyHub)
		}
	}
	return profiles, nil
}

func sortedNames(m map[string]*hubProfile) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// envConnectionLayer is the layer of the environment variables. A variable that is set but empty
// means: no tenant, no certificate (the system roots), no profile. For the hub and the key it
// means: not set.
func envConnectionLayer() layer {
	l := layer{values: map[string]any{}, srcs: map[string]source{}}
	for _, v := range []struct{ name, key string }{{coreapi.EnvHub, keyHub}, {coreapi.EnvTenant, keyTenant},
		{coreapi.EnvCACert, keyCACert}, {coreapi.EnvKey, keyKeyFile}, {EnvHubProfile, keyHubProfile}} {
		val, ok := os.LookupEnv(v.name)
		if !ok || (val == "" && (v.key == keyHub || v.key == keyKeyFile)) {
			continue
		}
		l.values[v.key] = val
		l.srcs[v.key] = source{Kind: sourceEnv, From: v.name}
	}
	return l
}

// flagConnectionLayer is a layer of the flags that were given, with their values by flag name.
func flagConnectionLayer(values map[string]string) layer {
	l := layer{values: map[string]any{}, srcs: map[string]source{}}
	for flagName, key := range connectionFlags {
		if v, ok := values[flagName]; ok {
			l.values[key] = v
			l.srcs[key] = source{Kind: sourceFlag, From: "--" + flagName}
		}
	}
	return l
}

// connectionFlags maps the flags of the connection to their keys.
var connectionFlags = map[string]string{"hub": keyHub, "tenant": keyTenant, "ca-cert": keyCACert,
	"key": keyKeyFile, "hub-profile": keyHubProfile}

// EnvHubProfile is the environment variable that selects a hub profile.
const EnvHubProfile = "OFFICE_HUB_PROFILE"

// resolveConnection resolves the connection from the file layers, the environment and the
// flags that were given (flagValues, by flag name).
func resolveConnection(files []layer, flagValues map[string]string) (connection, error) {
	profiles, err := buildProfiles(files)
	if err != nil {
		return connection{}, err
	}
	env := envConnectionLayer()
	flags := flagConnectionLayer(flagValues)

	layers := append(append([]layer{}, files...), env, flags)
	s, err := replayLayers(layers, profiles, false)
	if err != nil {
		return connection{}, err
	}
	return connection{Settings: s, Profiles: profiles}, nil
}

// layerSourceOf returns the source of the value key in layer l: the source of that value if the
// layer has one (the variable or the flag), else the source of the layer (the file).
func layerSourceOf(l layer, key string) source {
	if s, ok := l.srcs[key]; ok {
		return s
	}
	return l.src
}

func layerSource(l layer, key string) string { return layerSourceOf(l, key).String() }

func knownProfiles(profiles map[string]*hubProfile) string {
	if len(profiles) == 0 {
		return "; there are no hub profiles (add one with \"office hub add\")"
	}
	return "; known: " + strings.Join(sortedNames(profiles), ", ")
}

// loadConnection reads the files for the start directory start and resolves the connection with
// the environment and the flags that were given.
func loadConnection(start string, flagValues map[string]string) (connection, []configFileInfo, error) {
	layers, files, err := readLayers(start)
	if err != nil {
		return connection{}, nil, err
	}
	c, err := resolveConnection(layers, flagValues)
	return c, files, err
}

// replayLayers applies the layers from the defaults up. With noProfiles, a selection of a profile
// is left out.
func replayLayers(layers []layer, profiles map[string]*hubProfile, noProfiles bool) (settings, error) {
	s := settings{
		keyHub:     {Value: coreapi.DefaultHub, Source: source{Kind: sourceDefault}},
		keyKeyFile: {Value: coreapi.DefaultKeyFile(), Source: source{Kind: sourceDefault}},
	}
	if p := coreapi.DefaultCACert(); p != "" {
		s[keyCACert] = setting{Value: p, Source: source{Kind: sourceDefault}}
	}
	base := s[keyKeyFile] // the key without a profile
	for i, l := range layers {
		if v, ok := l.values[keyHubProfile]; ok && !noProfiles {
			name := v.(string)
			if name == "" {
				// No profile: drop what the profile of a lower layer gave.
				var err error
				if s, err = replayLayers(layers[:i], profiles, true); err != nil {
					return nil, err
				}
				base = s[keyKeyFile]
			} else {
				p, ok := profiles[name]
				if !ok {
					return nil, usagef("hub profile %q (%s) is not defined%s", name, layerSource(l, keyHubProfile), knownProfiles(profiles))
				}
				s[keyHubProfile] = setting{Value: name, Source: layerSourceOf(l, keyHubProfile)}
				for _, f := range connectionFields {
					val, set := p.Values[f]
					if !set || (val == "" && f != keyHub) {
						if f == keyKeyFile {
							s[f] = base // no key in the profile: the key of no profile
						} else {
							delete(s, f)
						}
						continue
					}
					src := p.Sources[f]
					s[f] = setting{Value: val, Source: source{Kind: sourceProfile, From: src.From, Profile: name}}
				}
			}
		}
		for _, f := range connectionFields {
			v, ok := l.values[f]
			if !ok {
				continue
			}
			if text, _ := v.(string); text == "" && (f == keyTenant || f == keyCACert) {
				delete(s, f) // explicitly none
				continue
			}
			s[f] = setting{Value: v, Source: layerSourceOf(l, f)}
			if f == keyKeyFile {
				base = s[f]
			}
		}
	}
	return s, nil
}
