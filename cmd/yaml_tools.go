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
	mainRepos := y.ExtractMainApplicationRepos(yamlContent)
	if len(mainRepos) > 0 {
		return mainRepos
	}
	
	// Fallback to extracting all repos if we can't identify main containers
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
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
			for i := 0; i < len(n.Content)-1; i += 2 {
				key := n.Content[i]
				val := n.Content[i+1]
				if key.Value == "image" && val.Kind == yaml.ScalarNode {
					img := val.Value
					if idx := strings.Index(img, ":"); idx > 0 {
						repo := img[:idx]
						if !repoSet[repo] {
							repos = append(repos, repo)
							repoSet[repo] = true
						}
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

func (y *YAMLOperations) ExtractMainApplicationRepos(yamlContent string) []string {
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
		return nil
	}

	var mainRepos []string
	repoSet := make(map[string]bool)
	
	var walk func(n *yaml.Node, depth int)
	walk = func(n *yaml.Node, depth int) {
		if n == nil {
			return
		}

		switch n.Kind {
		case yaml.MappingNode:
			var currentContainerName string
			
			for i := 0; i < len(n.Content)-1; i += 2 {
				key := n.Content[i]
				val := n.Content[i+1]
				
				if key.Value == "name" && val.Kind == yaml.ScalarNode {
					currentContainerName = val.Value
				}
				
				if key.Value == "image" {
					if val.Kind == yaml.ScalarNode {
						img := val.Value
						if idx := strings.Index(img, ":"); idx > 0 {
							repo := img[:idx]
							isMain := y.IsMainContainer(currentContainerName, repo)
							if isMain && !repoSet[repo] {
								mainRepos = append(mainRepos, repo)
								repoSet[repo] = true
							}
						}
					} else if val.Kind == yaml.MappingNode {
						var repo string
						for j := 0; j < len(val.Content)-1; j += 2 {
							subKey := val.Content[j]
							subVal := val.Content[j+1]
							if subKey.Value == "repo" && subVal.Kind == yaml.ScalarNode {
								repo = subVal.Value
								break
							}
						}
						if repo != "" {
							isMain := y.IsMainContainer(currentContainerName, repo)
							if isMain && !repoSet[repo] {
								mainRepos = append(mainRepos, repo)
								repoSet[repo] = true
							}
						}
					}
				}
				
				walk(val, depth+1)
			}
			
		case yaml.SequenceNode:
			for _, item := range n.Content {
				walk(item, depth+1)
			}
		}
	}

	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		walk(root.Content[0], 0)
	} else {
		walk(&root, 0)
	}

	return mainRepos
}

func (y *YAMLOperations) IsMainContainer(containerName, imageRepo string) bool {
	containerName = strings.ToLower(containerName)
	imageRepo = strings.ToLower(imageRepo)
	
	// Known blockchain node repositories that should be updated
	knownMainRepos := map[string]bool{
		"parity/polkadot":     true,
		"parity/kusama":       true,
		"paritytech/polkadot": true,
		"paritytech/kusama":   true,
		"ethereum/client-go":  true,
		"hyperledger/fabric":  true,
	}
	
	if knownMainRepos[imageRepo] {
		return true
	}
	
	// Sidecar/utility containers that should NOT be updated
	sidecarPatterns := []string{
		"filebeat", "fluentd", "logstash", "fluent-bit",
		"prometheus", "grafana", "jaeger", "zipkin", 
		"nginx", "envoy", "istio", "linkerd",
		"vault", "consul", "redis", "memcached",
		"postgres", "mysql", "mongodb",
		"busybox", "alpine", "ubuntu", "centos",
		"pause", "k8s.gcr.io/pause",
		"cloudflare", "certbot",
	}
	
	for _, pattern := range sidecarPatterns {
		if strings.Contains(imageRepo, pattern) || strings.Contains(containerName, pattern) {
			return false
		}
	}
	
	// Check if container name suggests it's a main application
	mainContainerPatterns := []string{
		"polkadot", "kusama", "node", "validator", "archive",
		"ethereum", "geth", "consensus", "execution",
	}
	
	for _, pattern := range mainContainerPatterns {
		if strings.Contains(containerName, pattern) || strings.Contains(imageRepo, pattern) {
			return true
		}
	}
	
	// Default to false for safety - only update containers we're confident about
	return false
}

func (y *YAMLOperations) UpdateAllImageTagsYAML(yamlContent string, repoToTag map[string]string) (string, bool, error) {
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlContent), &root)
	if err != nil {
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
				key := n.Content[i]
				val := n.Content[i+1]
				
				if key.Value == "image" {
					if val.Kind == yaml.ScalarNode {
						// Handle simple format: image: "parity/polkadot:stable2503-9"
						img := val.Value
						if idx := strings.Index(img, ":"); idx > 0 {
							repo := img[:idx]
							if tag, ok := repoToTag[repo]; ok {
								newVal := repo + ":" + tag
								if val.Value != newVal {
									val.Value = newVal
									updated = true
								}
							}
						}
					} else if val.Kind == yaml.MappingNode {
						// Handle mapped format: image: { repo: parity/polkadot, tag: stable2503-9 }
						var repo, currentTag string
						var tagNode *yaml.Node
						
						// Find repo and tag fields
						for j := 0; j < len(val.Content)-1; j += 2 {
							subKey := val.Content[j]
							subVal := val.Content[j+1]
							if subKey.Value == "repo" && subVal.Kind == yaml.ScalarNode {
								repo = subVal.Value
							} else if subKey.Value == "tag" && subVal.Kind == yaml.ScalarNode {
								currentTag = subVal.Value
								tagNode = subVal
							}
						}
						
						// Update tag if we have a new one for this repo
						if repo != "" && tagNode != nil {
							if newTag, ok := repoToTag[repo]; ok && newTag != currentTag {
								tagNode.Value = newTag
								updated = true
							}
						}
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
	if !updated {
		return yamlContent, false, nil
	}
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	defer encoder.Close()
	err = encoder.Encode(&root)
	if err != nil {
		return "", false, err
	}
	return strings.TrimRight(b.String(), "\n") + "\n", true, nil
}