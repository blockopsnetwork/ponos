package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
	"gopkg.in/yaml.v3"
)

type GitOperations struct{}

func NewGitOperations() *GitOperations {
	return &GitOperations{}
}

func (g *GitOperations) CreateBranchFromMain(ctx context.Context, client *github.Client, owner, repo, branchName string) (*github.Reference, error) {
	// Get the main branch ref first
	mainRef, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/main")
	if err != nil {
		return nil, fmt.Errorf("failed to get main ref: %v", err)
	}

	// Create new branch from main
	newRef := &github.Reference{
		Ref: github.String("refs/heads/" + branchName),
		Object: &github.GitObject{
			SHA: mainRef.Object.SHA,
		},
	}
	
	createdRef, _, err := client.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch %s: %v", branchName, err)
	}

	return createdRef, nil
}

func (g *GitOperations) CreatePR(ctx context.Context, client *github.Client, owner, repo, branchName, prTitle, prBody string) (*github.PullRequest, error) {
	pr := &github.NewPullRequest{
		Title: &prTitle,
		Head:  &branchName,
		Base:  github.String("main"),
		Body:  &prBody,
	}

	pullRequest, _, err := client.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %v", err)
	}

	return pullRequest, nil
}

func (g *GitOperations) CreateCommitFromFiles(ctx context.Context, client *github.Client, owner, repo, branch string, filesToCommit []fileCommitData, commitMsg string) (*github.Commit, error) {
	ref, _, err := client.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get ref: %v", err)
	}

	commit, _, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %v", err)
	}

	var treeEntries []*github.TreeEntry
	for _, f := range filesToCommit {
		blob, _, err := client.Git.CreateBlob(ctx, f.owner, f.repo, &github.Blob{
			Content:  &f.newYAML,
			Encoding: github.String("utf-8"),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create blob for %s: %v", f.path, err)
		}

		treeEntries = append(treeEntries, &github.TreeEntry{
			Path: &f.path,
			Mode: github.String("100644"),
			Type: github.String("blob"),
			SHA:  blob.SHA,
		})
	}

	tree, _, err := client.Git.CreateTree(ctx, owner, repo, *commit.Tree.SHA, treeEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %v", err)
	}

	newCommit, _, err := client.Git.CreateCommit(ctx, owner, repo, &github.Commit{
		Message: &commitMsg,
		Tree:    tree,
		Parents: []*github.Commit{commit},
		Author: &github.CommitAuthor{
			Name:  github.String("Ponos Bot"),
			Email: github.String("ponos@blockops.sh"),
			Date:  &github.Timestamp{Time: time.Now()},
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %v", err)
	}

	_, _, err = client.Git.UpdateRef(ctx, owner, repo, &github.Reference{
		Ref: github.String("refs/heads/" + branch),
		Object: &github.GitObject{
			SHA: newCommit.SHA,
		},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update ref: %v", err)
	}

	return newCommit, nil
}

func (g *GitOperations) PrepareFileUpdates(ctx context.Context, client *github.Client, filesToUpdate []fileInfo, imageToTag map[string]string) ([]fileCommitData, []imageUpgrade, error) {
	var filesToCommit []fileCommitData
	var upgrades []imageUpgrade
	
	yamlOps := NewYAMLOperations()

	fmt.Printf("DEBUG: PrepareFileUpdates called with %d files, imageToTag: %v\n", len(filesToUpdate), imageToTag)

	for _, f := range filesToUpdate {
		fmt.Printf("DEBUG: Processing file %s/%s:%s\n", f.owner, f.repo, f.path)
		file, _, _, ferr := client.Repositories.GetContents(ctx, f.owner, f.repo, f.path, nil)
		if ferr != nil || file == nil {
			fmt.Printf("DEBUG: Failed to get file content for %s: %v\n", f.path, ferr)
			continue
		}
		content, cerr := file.GetContent()
		if cerr != nil {
			fmt.Printf("DEBUG: Failed to decode file content for %s: %v\n", f.path, cerr)
			continue
		}

		newYAML, updated, uerr := yamlOps.UpdateAllImageTagsYAML(content, imageToTag)
		if uerr != nil {
			fmt.Printf("DEBUG: Failed to update YAML for %s: %v\n", f.path, uerr)
			continue
		}
		if !updated {
			fmt.Printf("DEBUG: No updates needed for file %s\n", f.path)
			continue
		}

		fmt.Printf("DEBUG: File %s was updated successfully\n", f.path)

		// Track upgrades for reporting
		var oldToNew []imageUpgrade
		var root yaml.Node
		if yaml.Unmarshal([]byte(content), &root) == nil {
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
								if tag, ok := imageToTag[repo]; ok {
									newVal := repo + ":" + tag
									if img != newVal {
										oldToNew = append(oldToNew, imageUpgrade{file: f.path, oldImg: img, newImg: newVal})
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
		}
		upgrades = append(upgrades, oldToNew...)

		filesToCommit = append(filesToCommit, fileCommitData{
			owner:   f.owner,
			repo:    f.repo,
			path:    f.path,
			sha:     *file.SHA,
			newYAML: newYAML,
		})
	}

	return filesToCommit, upgrades, nil
}