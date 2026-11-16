package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// firstPort is the port assigned to the first tunnel; later tunnels get the
// highest used port plus one. This matches the cloud-sql-proxy Makefile.
const firstPort = 10200

// Tunnel is one proxied Cloud SQL instance.
type Tunnel struct {
	Name     string `json:"name"`
	Instance string `json:"instance"` // connection name, project:region:instance
	Port     int    `json:"port"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
}

func (t *Tunnel) UnmarshalJSON(b []byte) error {
	type plain Tunnel
	aux := struct {
		*plain
		Enabled *bool `json:"enabled"`
	}{plain: (*plain)(t)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	// Generated tunnel files omit "enabled" and only contain enabled tunnels.
	t.Enabled = aux.Enabled == nil || *aux.Enabled
	return nil
}

// ConfigStore holds the tunnel settings and writes every change to disk.
type ConfigStore struct {
	mu       sync.Mutex
	path     string
	projects []string
	tunnels  []Tunnel
	// other keeps top-level keys this app doesn't use, e.g. ssh_tunnels.
	other map[string]json.RawMessage
}

func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cloudsqlproxy.json"), nil
}

func LoadConfig(path string) (*ConfigStore, error) {
	c := &ConfigStore{path: path, other: map[string]json.RawMessage{}}

	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.other); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw, ok := c.other["tunnels"]; ok {
		if err := json.Unmarshal(raw, &c.tunnels); err != nil {
			return nil, fmt.Errorf("parse tunnels in %s: %w", path, err)
		}
	}
	if raw, ok := c.other["projects"]; ok {
		if err := json.Unmarshal(raw, &c.projects); err != nil {
			return nil, fmt.Errorf("parse projects in %s: %w", path, err)
		}
	}
	delete(c.other, "tunnels")
	delete(c.other, "projects")
	return c, nil
}

// save writes the config atomically. The caller must hold c.mu.
func (c *ConfigStore) save() error {
	sort.Slice(c.tunnels, func(i, j int) bool { return c.tunnels[i].Name < c.tunnels[j].Name })

	out := map[string]any{}
	for k, v := range c.other {
		out[k] = v
	}
	out["projects"] = c.projects
	out["tunnels"] = c.tunnels
	if c.projects == nil {
		out["projects"] = []string{}
	}
	if c.tunnels == nil {
		out["tunnels"] = []Tunnel{}
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *ConfigStore) Projects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.projects)
}

func (c *ConfigStore) AddProject(project string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.Contains(c.projects, project) {
		return nil
	}
	c.projects = append(c.projects, project)
	sort.Strings(c.projects)
	return c.save()
}

// RemoveProject removes a project from the list. Its tunnels are kept.
func (c *ConfigStore) RemoveProject(project string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.projects = slices.DeleteFunc(c.projects, func(p string) bool { return p == project })
	return c.save()
}

// Tunnels returns all tunnels sorted by name.
func (c *ConfigStore) Tunnels() []Tunnel {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.tunnels)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *ConfigStore) Enabled() []Tunnel {
	return slices.DeleteFunc(c.Tunnels(), func(t Tunnel) bool { return !t.Enabled })
}

func (c *ConfigStore) Get(instance string) (Tunnel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := c.indexLocked(instance); i >= 0 {
		return c.tunnels[i], true
	}
	return Tunnel{}, false
}

func (c *ConfigStore) indexLocked(instance string) int {
	return slices.IndexFunc(c.tunnels, func(t Tunnel) bool { return t.Instance == instance })
}

// Set updates the port and enabled state of a tunnel, creating it if needed.
func (c *ConfigStore) Set(instance string, port int, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := c.indexLocked(instance); i >= 0 {
		c.tunnels[i].Port = port
		c.tunnels[i].Enabled = enabled
		return c.save()
	}
	name := c.uniqueNameLocked(instance)
	c.tunnels = append(c.tunnels, Tunnel{Name: name, Instance: instance, Port: port, Label: name, Enabled: enabled})
	return c.save()
}

// uniqueNameLocked names a new tunnel after its instance, prefixed with the
// project if another tunnel already uses that name.
func (c *ConfigStore) uniqueNameLocked(instance string) string {
	parts := strings.Split(instance, ":")
	name := parts[len(parts)-1]
	taken := func(n string) bool {
		return slices.ContainsFunc(c.tunnels, func(t Tunnel) bool { return t.Name == n })
	}
	if taken(name) {
		name = parts[0] + "-" + name
	}
	for base, i := name, 2; taken(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// PortConflict returns the name of another enabled tunnel using port, if any.
// Only enabled tunnels conflict; disabled ones may share a port.
func (c *ConfigStore) PortConflict(instance string, port int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.tunnels {
		if t.Instance != instance && t.Enabled && t.Port == port {
			return t.Name
		}
	}
	return ""
}

// PortConflicts describes every port used by more than one enabled tunnel,
// e.g. after the config file was edited by hand.
func (c *ConfigStore) PortConflicts() []string {
	byPort := map[int][]string{}
	for _, t := range c.Enabled() {
		byPort[t.Port] = append(byPort[t.Port], t.Name)
	}
	var out []string
	for port, names := range byPort {
		if len(names) > 1 {
			out = append(out, fmt.Sprintf("Port %d is used by %s", port, strings.Join(names, ", ")))
		}
	}
	sort.Strings(out)
	return out
}

// SuggestPort returns the tunnel's port if it has one, otherwise the next
// port after the highest one in use.
func (c *ConfigStore) SuggestPort(instance string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := c.indexLocked(instance); i >= 0 && c.tunnels[i].Port != 0 {
		return c.tunnels[i].Port
	}
	return c.nextPortLocked(instance)
}

// NextPort returns the port after the highest one used by any other tunnel.
func (c *ConfigStore) NextPort(instance string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextPortLocked(instance)
}

func (c *ConfigStore) nextPortLocked(instance string) int {
	port := firstPort
	for _, t := range c.tunnels {
		if t.Instance != instance && t.Port >= port {
			port = t.Port + 1
		}
	}
	return port
}
