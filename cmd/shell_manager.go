package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

type ShellCommand struct {
	ID         string
	Command    string
	Args       []string
	Status     string // "running", "completed", "failed"
	Output     []string
	ExitCode   int
	StartTime  time.Time
	EndTime    *time.Time
	WorkingDir string
}

type ShellManager struct {
	commands map[string]*ShellCommand
	mutex    sync.RWMutex
}

func NewShellManager() *ShellManager {
	return &ShellManager{
		commands: make(map[string]*ShellCommand),
	}
}

func (sm *ShellManager) ExecuteBackground(ctx context.Context, id, command string, args []string, workingDir string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	shellCmd := &ShellCommand{
		ID:         id,
		Command:    command,
		Args:       args,
		Status:     "running",
		Output:     make([]string, 0),
		StartTime:  time.Now(),
		WorkingDir: workingDir,
	}

	sm.commands[id] = shellCmd

	go sm.runCommand(ctx, shellCmd)
	return nil
}

func (sm *ShellManager) runCommand(ctx context.Context, shellCmd *ShellCommand) {
	defer func() {
		sm.mutex.Lock()
		now := time.Now()
		shellCmd.EndTime = &now
		if shellCmd.Status == "running" {
			shellCmd.Status = "completed"
		}
		sm.mutex.Unlock()
	}()

	cmd := exec.CommandContext(ctx, shellCmd.Command, shellCmd.Args...)
	if shellCmd.WorkingDir != "" {
		cmd.Dir = shellCmd.WorkingDir
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		sm.mutex.Lock()
		shellCmd.Status = "failed"
		shellCmd.Output = append(shellCmd.Output, fmt.Sprintf("Failed to start command: %v", err))
		shellCmd.ExitCode = 1
		sm.mutex.Unlock()
		return
	}
	defer ptmx.Close()

	go func() {
		scanner := bufio.NewScanner(ptmx)
		for scanner.Scan() {
			line := scanner.Text()
			sm.mutex.Lock()
			if shellCmd.Status == "running" {
				shellCmd.Output = append(shellCmd.Output, line)
			}
			sm.mutex.Unlock()
		}
	}()

	err = cmd.Wait()
	
	sm.mutex.Lock()
	if ctx.Err() == context.Canceled {
		shellCmd.Status = "cancelled"
	} else if err != nil {
		shellCmd.Status = "failed"
		if exitError, ok := err.(*exec.ExitError); ok {
			shellCmd.ExitCode = exitError.ExitCode()
		} else {
			shellCmd.ExitCode = 1
		}
	} else {
		shellCmd.Status = "completed"
		shellCmd.ExitCode = 0
	}
	sm.mutex.Unlock()
}

func (sm *ShellManager) ExecuteInteractive(ctx context.Context, command string, args []string, workingDir string) (*ShellCommand, io.ReadWriteCloser, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start interactive command: %w", err)
	}

	shellCmd := &ShellCommand{
		ID:         generateCommandID(),
		Command:    command,
		Args:       args,
		Status:     "running",
		StartTime:  time.Now(),
		WorkingDir: workingDir,
	}

	sm.mutex.Lock()
	sm.commands[shellCmd.ID] = shellCmd
	sm.mutex.Unlock()

	go func() {
		defer func() {
			sm.mutex.Lock()
			now := time.Now()
			shellCmd.EndTime = &now
			if shellCmd.Status == "running" {
				shellCmd.Status = "completed"
			}
			sm.mutex.Unlock()
		}()

		err := cmd.Wait()
		sm.mutex.Lock()
		if ctx.Err() == context.Canceled {
			shellCmd.Status = "cancelled"
		} else if err != nil {
			shellCmd.Status = "failed"
			if exitError, ok := err.(*exec.ExitError); ok {
				shellCmd.ExitCode = exitError.ExitCode()
			} else {
				shellCmd.ExitCode = 1
			}
		} else {
			shellCmd.Status = "completed"
			shellCmd.ExitCode = 0
		}
		sm.mutex.Unlock()
	}()

	return shellCmd, ptmx, nil
}

func (sm *ShellManager) GetCommand(id string) (*ShellCommand, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	cmd, exists := sm.commands[id]
	if !exists {
		return nil, false
	}

	cmdCopy := *cmd
	cmdCopy.Output = make([]string, len(cmd.Output))
	copy(cmdCopy.Output, cmd.Output)
	
	return &cmdCopy, true
}

func (sm *ShellManager) GetRecentOutput(id string, since time.Time) ([]string, bool) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	cmd, exists := sm.commands[id]
	if !exists {
		return nil, false
	}

	output := make([]string, len(cmd.Output))
	copy(output, cmd.Output)
	
	return output, true
}

func (sm *ShellManager) ListCommands() map[string]*ShellCommand {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	result := make(map[string]*ShellCommand)
	for id, cmd := range sm.commands {
		cmdCopy := *cmd
		cmdCopy.Output = make([]string, len(cmd.Output))
		copy(cmdCopy.Output, cmd.Output)
		result[id] = &cmdCopy
	}
	
	return result
}

func (sm *ShellManager) KillCommand(id string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	cmd, exists := sm.commands[id]
	if !exists {
		return fmt.Errorf("command not found: %s", id)
	}
	
	if cmd.Status == "running" {
		cmd.Status = "cancelled"
	}
	
	return nil
}

func generateCommandID() string {
	return fmt.Sprintf("cmd_%d", time.Now().UnixNano())
}
