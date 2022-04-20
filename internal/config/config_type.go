package config

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

const defaultGitProtocol = "https"

// This interface describes interacting with some persistent configuration for gh.
type Config interface {
	Get(string, string) (string, error)
	Set(string, string, string) error
	Aliases() (*AliasConfig, error)
	Write() error
}

type NotFoundError struct {
	error
}

type HostConfig struct {
	ConfigMap
	Host string
}

// This type implements a low-level get/set config that is backed by an in-memory tree of Yaml
// nodes. It allows us to interact with a yaml-based config programmatically, preserving any
// comments that were present when the yaml waas parsed.
type ConfigMap struct {
	Root *yaml.Node
}

func (cm *ConfigMap) Empty() bool {
	return cm.Root == nil || len(cm.Root.Content) == 0
}

func (cm *ConfigMap) GetStringValue(key string) (string, error) {
	entry, err := cm.FindEntry(key)
	if err != nil {
		return "", err
	}
	return entry.ValueNode.Value, nil
}

func (cm *ConfigMap) SetStringValue(key, value string) error {
	entry, err := cm.FindEntry(key)

	var notFound *NotFoundError

	valueNode := entry.ValueNode

	if err != nil && errors.As(err, &notFound) {
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: key,
		}
		valueNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: "",
		}

		cm.Root.Content = append(cm.Root.Content, keyNode, valueNode)
	} else if err != nil {
		return err
	}

	valueNode.Value = value

	return nil
}

type ConfigEntry struct {
	KeyNode   *yaml.Node
	ValueNode *yaml.Node
	Index     int
}

func (cm *ConfigMap) FindEntry(key string) (ce *ConfigEntry, err error) {
	err = nil

	ce = &ConfigEntry{}

	topLevelKeys := cm.Root.Content
	for i, v := range topLevelKeys {
		if v.Value == key {
			ce.KeyNode = v
			ce.Index = i
			if i+1 < len(topLevelKeys) {
				ce.ValueNode = topLevelKeys[i+1]
			}
			return
		}
	}

	return ce, &NotFoundError{errors.New("not found")}
}

func (cm *ConfigMap) RemoveEntry(key string) {
	newContent := []*yaml.Node{}

	content := cm.Root.Content
	for i := 0; i < len(content); i++ {
		if content[i].Value == key {
			i++ // skip the next node which is this key's value
		} else {
			newContent = append(newContent, content[i])
		}
	}

	cm.Root.Content = newContent
}

func NewConfig(root *yaml.Node) Config {
	return &fileConfig{
		ConfigMap:    ConfigMap{Root: root.Content[0]},
		documentRoot: root,
	}
}

func NewBlankConfig() Config {
	return NewConfig(&yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{
						HeadComment: "What protocol to use when performing git operations. Supported values: ssh, https",
						Kind:        yaml.ScalarNode,
						Value:       "git_protocol",
					},
					{
						Kind:  yaml.ScalarNode,
						Value: "https",
					},
					{
						HeadComment: "What editor gh should run when creating issues, pull requests, etc. If blank, will refer to environment.",
						Kind:        yaml.ScalarNode,
						Value:       "editor",
					},
					{
						Kind:  yaml.ScalarNode,
						Value: "",
					},
					{
						HeadComment: "Aliases allow you to create nicknames for gh commands",
						Kind:        yaml.ScalarNode,
						Value:       "aliases",
					},
					{
						Kind: yaml.MappingNode,
						Content: []*yaml.Node{
							{
								Kind:  yaml.ScalarNode,
								Value: "co",
							},
							{
								Kind:  yaml.ScalarNode,
								Value: "pr checkout",
							},
						},
					},
				},
			},
		},
	})
}

// This type implements a Config interface and represents a config file on disk.
type fileConfig struct {
	ConfigMap
	documentRoot *yaml.Node
}

func (c *fileConfig) Root() *yaml.Node {
	return c.ConfigMap.Root
}

func (c *fileConfig) Get(hostname, key string) (string, error) {
	if hostname != "" {
		hostCfg, err := c.configForHost(hostname)
		if err != nil {
			return "", err
		}

		hostValue, err := hostCfg.GetStringValue(key)
		var notFound *NotFoundError

		if err != nil && !errors.As(err, &notFound) {
			return "", err
		}

		if hostValue != "" {
			return hostValue, nil
		}
	}

	value, err := c.GetStringValue(key)

	var notFound *NotFoundError

	if err != nil && errors.As(err, &notFound) {
		return defaultFor(key), nil
	} else if err != nil {
		return "", err
	}

	if value == "" {
		return defaultFor(key), nil
	}

	return value, nil
}

func (c *fileConfig) Set(hostname, key, value string) error {
	if hostname == "" {
		return c.SetStringValue(key, value)
	} else {
		hostCfg, err := c.configForHost(hostname)
		var notFound *NotFoundError
		if errors.As(err, &notFound) {
			hostCfg = c.makeConfigForHost(hostname)
		} else if err != nil {
			return err
		}
		return hostCfg.SetStringValue(key, value)
	}
}

func (c *fileConfig) configForHost(hostname string) (*HostConfig, error) {
	hosts, err := c.hostEntries()
	if err != nil {
		return nil, fmt.Errorf("failed to parse hosts config: %w", err)
	}

	for _, hc := range hosts {
		if hc.Host == hostname {
			return hc, nil
		}
	}
	return nil, &NotFoundError{fmt.Errorf("could not find config entry for %q", hostname)}
}

func (c *fileConfig) Write() error {
	mainData := yaml.Node{Kind: yaml.MappingNode}
	hostsData := yaml.Node{Kind: yaml.MappingNode}

	nodes := c.documentRoot.Content[0].Content
	for i := 0; i < len(nodes)-1; i += 2 {
		if nodes[i].Value == "hosts" {
			hostsData.Content = append(hostsData.Content, nodes[i+1].Content...)
		} else {
			mainData.Content = append(mainData.Content, nodes[i], nodes[i+1])
		}
	}

	mainBytes, err := yaml.Marshal(&mainData)
	if err != nil {
		return err
	}

	filename := ConfigFile()
	err = WriteConfigFile(filename, yamlNormalize(mainBytes))
	if err != nil {
		return err
	}

	hostsBytes, err := yaml.Marshal(&hostsData)
	if err != nil {
		return err
	}

	return WriteConfigFile(hostsConfigFile(filename), yamlNormalize(hostsBytes))
}

func yamlNormalize(b []byte) []byte {
	if bytes.Equal(b, []byte("{}\n")) {
		return []byte{}
	}
	return b
}

func (c *fileConfig) Aliases() (*AliasConfig, error) {
	// The complexity here is for dealing with either a missing or empty aliases key. It's something
	// we'll likely want for other config sections at some point.
	entry, err := c.FindEntry("aliases")
	var nfe *NotFoundError
	notFound := errors.As(err, &nfe)
	if err != nil && !notFound {
		return nil, err
	}

	toInsert := []*yaml.Node{}

	keyNode := entry.KeyNode
	valueNode := entry.ValueNode

	if keyNode == nil {
		keyNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "aliases",
		}
		toInsert = append(toInsert, keyNode)
	}

	if valueNode == nil || valueNode.Kind != yaml.MappingNode {
		valueNode = &yaml.Node{
			Kind:  yaml.MappingNode,
			Value: "",
		}
		toInsert = append(toInsert, valueNode)
	}

	if len(toInsert) > 0 {
		newContent := []*yaml.Node{}
		if notFound {
			newContent = append(c.Root().Content, keyNode, valueNode)
		} else {
			for i := 0; i < len(c.Root().Content); i++ {
				if i == entry.Index {
					newContent = append(newContent, keyNode, valueNode)
					i++
				} else {
					newContent = append(newContent, c.Root().Content[i])
				}
			}
		}
		c.Root().Content = newContent
	}

	return &AliasConfig{
		Parent:    c,
		ConfigMap: ConfigMap{Root: valueNode},
	}, nil
}

func (c *fileConfig) hostEntries() ([]*HostConfig, error) {
	entry, err := c.FindEntry("hosts")
	if err != nil {
		return nil, fmt.Errorf("could not find hosts config: %w", err)
	}

	hostConfigs, err := c.parseHosts(entry.ValueNode)
	if err != nil {
		return nil, fmt.Errorf("could not parse hosts config: %w", err)
	}

	return hostConfigs, nil
}

func (c *fileConfig) makeConfigForHost(hostname string) *HostConfig {
	hostRoot := &yaml.Node{Kind: yaml.MappingNode}
	hostCfg := &HostConfig{
		Host:      hostname,
		ConfigMap: ConfigMap{Root: hostRoot},
	}

	var notFound *NotFoundError
	hostsEntry, err := c.FindEntry("hosts")
	if errors.As(err, &notFound) {
		hostsEntry.KeyNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "hosts",
		}
		hostsEntry.ValueNode = &yaml.Node{Kind: yaml.MappingNode}
		root := c.Root()
		root.Content = append(root.Content, hostsEntry.KeyNode, hostsEntry.ValueNode)
	} else if err != nil {
		panic(err)
	}

	hostsEntry.ValueNode.Content = append(hostsEntry.ValueNode.Content,
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: hostname,
		}, hostRoot)

	return hostCfg
}

func (c *fileConfig) parseHosts(hostsEntry *yaml.Node) ([]*HostConfig, error) {
	hostConfigs := []*HostConfig{}

	for i := 0; i < len(hostsEntry.Content)-1; i = i + 2 {
		hostname := hostsEntry.Content[i].Value
		hostRoot := hostsEntry.Content[i+1]
		hostConfig := HostConfig{
			ConfigMap: ConfigMap{Root: hostRoot},
			Host:      hostname,
		}
		hostConfigs = append(hostConfigs, &hostConfig)
	}

	if len(hostConfigs) == 0 {
		return nil, errors.New("could not find any host configurations")
	}

	return hostConfigs, nil
}

func defaultFor(key string) string {
	// we only have a set default for one setting right now
	switch key {
	case "git_protocol":
		return defaultGitProtocol
	default:
		return ""
	}
}
