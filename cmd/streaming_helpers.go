package main

import (
	"fmt"
	"time"
)

func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

func (m *tuiModel) findMessageByID(id string) *ChatMessage {
	for i := range m.messages {
		if m.messages[i].ID == id {
			return &m.messages[i]
		}
	}
	return nil
}

func (m *tuiModel) handleStreamAppend(messageID, text string) {
	if msg := m.findMessageByID(messageID); msg != nil {
		msg.Content += text
		m.isStreaming = true
		m.streamingMessageID = messageID
		m.updateViewportContent()
	}
}

func (m *tuiModel) startStreamingMessage(role, initialContent string) string {
	messageID := generateMessageID()
	msg := ChatMessage{
		ID:        messageID,
		Role:      role,
		Content:   initialContent,
		Timestamp: time.Now(),
		Actions:   []string{},
	}
	
	m.messages = append(m.messages, msg)
	m.isStreaming = true
	m.streamingMessageID = messageID
	m.updateViewportContent()
	
	return messageID
}

func (m *tuiModel) completeStreamingMessage() {
	m.isStreaming = false
	m.streamingMessageID = ""
	m.streamingToolID = ""
}

func (m *tuiModel) updateStreamingToolProgress(toolID, status string) {
	if msg := m.findMessageByID(m.streamingToolID); msg != nil {
		msg.Content = status
		m.updateViewportContent()
	}
}
