package cli

// The commands that manage the hub profiles in the user file (concept-client-configuration):
// office hub add, office hub list and office hub remove.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"go.yaml.in/yaml/v3"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// hubCertDir is the folder of the certificates that "office hub add" stores, in the
// configuration folder of the client. A certificate is <name>.crt there.
const hubCertDir = "hubs"

// hubCertTimeout is how long "office hub add" waits for the hub.
const hubCertTimeout = 15 * time.Second

// userConfigPath is the path of the user file.
func userConfigPath() string { return filepath.Join(coreapi.ClientConfigDir(), ConfigFileName) }

// managedCertPath is where "office hub add" stores the certificate of the profile name.
func managedCertPath(name string) string {
	return filepath.Join(coreapi.ClientConfigDir(), hubCertDir, name+".crt")
}

// hubAddress is a parsed hub address.
type hubAddress struct {
	URL  string // the address without a path, for the file
	Host string
	Port string
}

// parseHubAddress checks the address of "office hub add": https, a host and an optional port.
func parseHubAddress(s string) (hubAddress, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return hubAddress{}, usagef("the address of a hub is https://host or https://host:port, got %q", s)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return hubAddress{}, usagef("the address of a hub has no path, query or user name, got %q", s)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return hubAddress{URL: "https://" + u.Host, Host: u.Hostname(), Port: port}, nil
}

// hubCertificate is what a hub presents.
type hubCertificate struct {
	PEM         []byte
	Fingerprint string // SHA-256 of the certificate, lower-case hexadecimal, as the hub logs it
	Subject     string
	Names       []string
	NotAfter    time.Time
	// TrustedErr is nil when the system roots verify the certificate for the host name.
	TrustedErr error
}

// fetchHubCertificate connects to the hub and returns the certificate it presents. It does not
// trust the certificate: the user confirms its fingerprint.
func fetchHubCertificate(ctx context.Context, a hubAddress) (hubCertificate, error) {
	ctx, cancel := context.WithTimeout(ctx, hubCertTimeout)
	defer cancel()
	conf := &tls.Config{InsecureSkipVerify: true} // only to read the certificate, never to send data
	if net.ParseIP(a.Host) == nil {
		conf.ServerName = a.Host
	}
	conn, err := (&tls.Dialer{Config: conf}).DialContext(ctx, "tcp", net.JoinHostPort(a.Host, a.Port))
	if err != nil {
		return hubCertificate{}, fmt.Errorf("cannot connect to %s: %v", a.URL, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return hubCertificate{}, fmt.Errorf("%s presented no certificate", a.URL)
	}
	leaf := certs[0]
	sum := sha256.Sum256(leaf.Raw)
	hc := hubCertificate{
		PEM:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}),
		Fingerprint: hex.EncodeToString(sum[:]),
		Subject:     leaf.Subject.String(),
		NotAfter:    leaf.NotAfter,
	}
	hc.Names = append(hc.Names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		hc.Names = append(hc.Names, ip.String())
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, hc.TrustedErr = leaf.Verify(x509.VerifyOptions{DNSName: a.Host, Intermediates: inter})
	return hc, nil
}

// normalizeFingerprint turns "SHA256:AB:cd:…" or "abcd…" into lower-case hexadecimal.
func normalizeFingerprint(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 && strings.EqualFold(s[:7], "sha256:") {
		s = s[7:]
	}
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(s))
}

// cmdHubAdd adds a hub profile: office hub add <name> <url> [--fingerprint F] [--default]
// [--tenant T] [--key FILE]. It reads the certificate of the hub, shows its fingerprint and asks
// for confirmation. With --fingerprint, it compares instead of asking. A certificate that a
// public authority signed is not stored. The profile goes to the user file, and the certificate
// to the folder hubs in the configuration folder.
func cmdHubAdd(e *env, fs *flag.FlagSet) func([]string) error {
	fingerprint := fs.String("fingerprint", "", "the SHA-256 fingerprint of the hub certificate, from the log of the hub; compared instead of asking")
	makeDefault := fs.Bool("default", false, "make this profile the default (key hub-profile)")
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office hub add <name> <url> [--fingerprint F] [--default] [--tenant T] [--key FILE]")
		}
		given := visitedFlags(fs, connectionFlagNames)
		for _, f := range []string{"hub", "ca-cert", "hub-profile"} {
			if _, ok := given[f]; ok {
				return usagef("office hub add does not take --%s: the address is the second argument, and the certificate comes from the hub", f)
			}
		}
		name := args[0]
		if !profileName.MatchString(name) {
			return usagef("the name of a hub profile has letters, digits, \"-\" and \"_\" (at most 64, not starting with a dash or underscore), got %q", name)
		}
		addr, err := parseHubAddress(args[1])
		if err != nil {
			return err
		}
		keyFile := given["key"]
		if keyFile != "" {
			if keyFile, err = filepath.Abs(keyFile); err != nil {
				return err
			}
		}
		hc, err := fetchHubCertificate(e.ctx, addr)
		if err != nil {
			return err
		}
		e.progress(fmt.Sprintf("the hub %s presents a certificate: subject %s, names %s, valid until %s, SHA-256 fingerprint %s",
			addr.URL, hc.Subject, strings.Join(hc.Names, ", "), hc.NotAfter.Format("2006-01-02"), hc.Fingerprint))
		if want := normalizeFingerprint(*fingerprint); *fingerprint != "" && want != hc.Fingerprint {
			return fmt.Errorf("the fingerprint does not match: the hub presents %s, you expected %s; nothing was written", hc.Fingerprint, want)
		}
		store := hc.TrustedErr != nil
		if store && *fingerprint == "" {
			fmt.Fprintf(e.stderr, "This computer does not trust the certificate (%v).\n"+
				"Compare the SHA-256 fingerprint above with the one in the log of the hub.\n"+
				"Trust this certificate for the hub profile %q? [y/N] ", hc.TrustedErr, name)
			answer, _ := bufio.NewReader(e.stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				return errors.New("the certificate was not confirmed; nothing was written")
			}
		}

		path := userConfigPath()
		certFile := ""
		if store {
			certFile = managedCertPath(name)
			if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(certFile, hc.PEM, 0o644); err != nil {
				return err
			}
		}
		replaced := false
		err = editConfigFile(path, func(root *yaml.Node) error {
			hubs := mapEnsure(root, keyHubs)
			_, replaced = mapGet(hubs, name)
			p := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapSet(p, keyHub, scalar(addr.URL))
			if t := given["tenant"]; t != "" {
				mapSet(p, keyTenant, scalar(t))
			}
			if store {
				mapSet(p, keyCACert, scalar(filepath.ToSlash(filepath.Join(hubCertDir, name+".crt"))))
			}
			if keyFile != "" {
				mapSet(p, keyKeyFile, scalar(keyFile))
			}
			mapSet(hubs, name, p)
			if *makeDefault {
				mapSet(root, keyHubProfile, scalar(name))
			}
			return nil
		})
		if err != nil {
			if certFile != "" {
				os.Remove(certFile)
			}
			return err
		}
		if !store && replaced {
			os.Remove(managedCertPath(name)) // the new profile needs no certificate
		}
		if e.json {
			e.printJSON(map[string]any{"profile": name, "hub": addr.URL, "fingerprint": hc.Fingerprint,
				"publicCA": !store, "certificate": certFile, "default": *makeDefault, "replaced": replaced, "file": path})
			return nil
		}
		verb := "added"
		if replaced {
			verb = "replaced"
		}
		fmt.Fprintf(e.stdout, "%s the hub profile %s for %s in %s\n", verb, name, addr.URL, path)
		if store {
			fmt.Fprintf(e.stdout, "stored the certificate in %s\n", certFile)
		} else {
			fmt.Fprintln(e.stdout, "the certificate is valid with the system roots; no certificate is stored")
		}
		if *makeDefault {
			fmt.Fprintf(e.stdout, "%s is the default profile\n", name)
		}
		return nil
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// cmdHubList lists the hub profiles of the user file and the project file: office hub list
// [--json]. The active profile has a star.
func cmdHubList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		start, err := os.Getwd()
		if err != nil {
			return err
		}
		layers, _, err := readLayers(start)
		if err != nil {
			return err
		}
		profiles, err := buildProfiles(layers)
		if err != nil {
			return err
		}
		active := ""
		if c, err := resolveConnection(layers, visitedFlags(fs, connectionFlagNames)); err == nil {
			active = c.Settings.str(keyHubProfile)
		}
		if e.json {
			type entry struct {
				Name   string   `json:"name"`
				Hub    string   `json:"hub"`
				Tenant string   `json:"tenant,omitempty"`
				CACert string   `json:"caCert,omitempty"`
				Key    string   `json:"key,omitempty"`
				Active bool     `json:"active"`
				Files  []string `json:"files"`
			}
			out := struct {
				Active   string  `json:"active"`
				Profiles []entry `json:"profiles"`
			}{Active: active, Profiles: []entry{}}
			for _, n := range sortedNames(profiles) {
				p := profiles[n]
				out.Profiles = append(out.Profiles, entry{Name: n, Hub: p.Values[keyHub], Tenant: p.Values[keyTenant],
					CACert: p.Values[keyCACert], Key: p.Values[keyKeyFile], Active: n == active, Files: p.files()})
			}
			e.printJSON(out)
			return nil
		}
		if len(profiles) == 0 {
			fmt.Fprintln(e.stdout, "no hub profiles; add one with: office hub add <name> <url>")
			return nil
		}
		w := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tHUB\tTENANT\tCERTIFICATE\tKEY\tDEFINED IN")
		for _, n := range sortedNames(profiles) {
			p := profiles[n]
			star := ""
			if n == active {
				star = "*"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", star, n, p.Values[keyHub], dash(p.Values[keyTenant]),
				dash(p.Values[keyCACert]), dash(p.Values[keyKeyFile]), strings.Join(p.files(), ", "))
		}
		return w.Flush()
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cmdHubRemove removes a hub profile from the user file: office hub remove <name>. It also
// removes the default profile if it is this one, and the certificate that "office hub add"
// stored for it.
func cmdHubRemove(e *env, _ *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office hub remove <name>")
		}
		name := args[0]
		path := userConfigPath()
		start, err := os.Getwd()
		if err != nil {
			return err
		}
		layers, _, err := readLayers(start)
		if err != nil {
			return err
		}
		profiles, err := buildProfiles(layers)
		if err != nil {
			return err
		}
		other, inUser := "", false
		for _, l := range layers {
			if _, ok := l.values[keyHubs+"."+name]; ok {
				if l.src.Kind == sourceProject {
					other = l.src.From
				} else {
					inUser = true
				}
			}
		}
		removed, wasDefault := false, false
		if inUser {
			err = editConfigFile(path, func(root *yaml.Node) error {
				if hubs, ok := mapGet(root, keyHubs); ok && hubs.Kind == yaml.MappingNode {
					removed = mapDel(hubs, name)
					if len(hubs.Content) == 0 {
						mapDel(root, keyHubs)
					}
				}
				if v, ok := mapGet(root, keyHubProfile); ok && v.Value == name {
					wasDefault = mapDel(root, keyHubProfile)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if !removed {
			if other != "" {
				return usagef("the hub profile %q is defined in the project file %s, not in the user file; edit that file", name, other)
			}
			return usagef("no hub profile %q in the user file %s%s", name, path, knownProfiles(profiles))
		}
		certRemoved := false
		if p := managedCertPath(name); fileExists(p) && !certUsedElsewhere(profiles, name, p) {
			certRemoved = os.Remove(p) == nil
		}
		if e.json {
			e.printJSON(map[string]any{"profile": name, "wasDefault": wasDefault, "certificateRemoved": certRemoved, "file": path})
			return nil
		}
		fmt.Fprintf(e.stdout, "removed the hub profile %s from %s\n", name, path)
		if wasDefault {
			fmt.Fprintf(e.stdout, "%s was the default profile; there is no default now\n", name)
		}
		if certRemoved {
			fmt.Fprintf(e.stdout, "removed the certificate %s\n", managedCertPath(name))
		}
		if other != "" {
			fmt.Fprintf(e.stderr, "note: the project file %s also defines %s\n", other, name)
		}
		return nil
	}
}

// certUsedElsewhere reports whether a profile other than name still uses the certificate file p.
func certUsedElsewhere(profiles map[string]*hubProfile, name, p string) bool {
	for n, o := range profiles {
		if n != name && o.Values[keyCACert] != "" && filepath.Clean(o.Values[keyCACert]) == filepath.Clean(p) {
			return true
		}
	}
	return false
}

// editConfigFile reads the user file (a missing file is an empty file), lets edit change its
// top-level mapping, checks the result against the schema and writes it back. Comments and the
// order of the keys stay.
func editConfigFile(path string, edit func(root *yaml.Node) error) error {
	root := filepath.Dir(path)
	src := source{Kind: sourceUser, From: path}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return usagef("configuration file %s: %v", path, err)
	}
	var doc yaml.Node
	if len(b) > 0 {
		if _, err := parseConfig(b, path, root, src); err != nil {
			return err // an invalid file is not changed
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return usagef("configuration file %s: %s", path, strings.TrimPrefix(err.Error(), "yaml: "))
		}
	}
	if doc.Kind != yaml.DocumentNode {
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	top := doc.Content[0]
	if top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		*top = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if err := edit(top); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if _, err := parseConfig(buf.Bytes(), path, root, src); err != nil {
		return fmt.Errorf("the change would make the configuration file invalid: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// mapGet returns the value of key in the mapping m.
func mapGet(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], true
		}
	}
	return nil, false
}

// mapSet sets key in the mapping m, in place if it exists, else at the end.
func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

// mapEnsure returns the mapping under key, and creates it if it is missing or empty.
func mapEnsure(m *yaml.Node, key string) *yaml.Node {
	if v, ok := mapGet(m, key); ok && v.Kind == yaml.MappingNode {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(m, key, v)
	return v
}

// mapDel removes key from the mapping m and reports whether it was there.
func mapDel(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}
