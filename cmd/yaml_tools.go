package main

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type YAMLOperations struct{}

func NewYAMLOperations() *YAMLOperations {
	return &YAMLOperations{}
}

func (y *YAMLOperations) ExtractImageReposFromYAML(yamlContent string) []string {
	return y.ExtractMainApplicationRepos(yamlContent)
}

func (y *YAMLOperations) ExtractMainApplicationRepos(yamlContent string) []string {
	var root yaml.Node
	if yaml.Unmarshal([]byte(yamlContent), &root) != nil {
		return nil
	}

	var repos []string
	repoSet := make(map[string]bool)

	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			var containerName string
			for i := 0; i < len(n.Content)-1; i += 2 {
				key, val := n.Content[i], n.Content[i+1]
				if key.Value == "name" && val.Kind == yaml.ScalarNode {
					containerName = val.Value
				} else if key.Value == "image" {
					repo := y.extractRepo(val)
					if repo != "" && y.IsMainContainer(containerName, repo) && !repoSet[repo] {
						repos = append(repos, repo)
						repoSet[repo] = true
					}
				}
				walk(val)
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				walk(item)
			}
		}
	}

	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		walk(root.Content[0])
	} else {
		walk(&root)
	}
	return repos
}

func (y *YAMLOperations) extractRepo(node *yaml.Node) string {
	if node.Kind == yaml.ScalarNode {
		if idx := strings.Index(node.Value, ":"); idx > 0 {
			return node.Value[:idx]
		}
	} else if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == "repo" && node.Content[i+1].Kind == yaml.ScalarNode {
				return node.Content[i+1].Value
			}
		}
	}
	return ""
}

func (y *YAMLOperations) IsMainContainer(containerName, imageRepo string) bool {
	containerName = strings.ToLower(containerName)
	imageRepo = strings.ToLower(imageRepo)

	knownMainRepos := map[string]bool{
		"parity/polkadot":     true,
		"paritytech/polkadot": true,
		"ethereum/client-go":  true,
		"hyperledger/fabric":  true,
	}
	if knownMainRepos[imageRepo] {
		return true
	}

	sidecarPatterns := []string{
		"filebeat", "fluentd", "prometheus", "grafana", "nginx", "envoy",
		"vault", "redis", "postgres", "mysql", "busybox", "alpine", "pause",
	}
	for _, pattern := range sidecarPatterns {
		if strings.Contains(imageRepo, pattern) || strings.Contains(containerName, pattern) {
			return false
		}
	}

	mainPatterns := []string{"polkadot", "kusama", "node", "validator", "ethereum", "geth"}
	for _, pattern := range mainPatterns {
		if strings.Contains(containerName, pattern) || strings.Contains(imageRepo, pattern) {
			return true
		}
	}
	return false
}

func (y *YAMLOperations) UpdateAllImageTagsYAML(yamlContent string, repoToTag map[string]string) (string, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return "", false, err
	}

	var updated bool
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i < len(n.Content)-1; i += 2 {
				if n.Content[i].Value == "image" {
					if y.updateImageNode(n.Content[i+1], repoToTag) {
						updated = true
					}
				}
				walk(n.Content[i+1])
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				walk(item)
			}
		}
	}

	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		walk(root.Content[0])
	} else {
		walk(&root)
	}

	if !updated {
		return yamlContent, false, nil
	}

	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	defer encoder.Close()
	if err := encoder.Encode(&root); err != nil {
		return "", false, err
	}
	return strings.TrimRight(b.String(), "\n") + "\n", true, nil
}

func (y *YAMLOperations) updateImageNode(node *yaml.Node, repoToTag map[string]string) bool {
	if node.Kind == yaml.ScalarNode {
		if idx := strings.Index(node.Value, ":"); idx > 0 {
			repo := node.Value[:idx]
			if tag, ok := repoToTag[repo]; ok {
				newVal := repo + ":" + tag
				if node.Value != newVal {
					node.Value = newVal
					return true
				}
			}
		}
	} else if node.Kind == yaml.MappingNode {
		var repo string
		var tagNode *yaml.Node
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == "repo" && node.Content[i+1].Kind == yaml.ScalarNode {
				repo = node.Content[i+1].Value
			} else if node.Content[i].Value == "tag" && node.Content[i+1].Kind == yaml.ScalarNode {
				tagNode = node.Content[i+1]
			}
		}
		if repo != "" && tagNode != nil {
			if newTag, ok := repoToTag[repo]; ok && newTag != tagNode.Value {
				tagNode.Value = newTag
				return true
			}
		}
	}
	return false
}
