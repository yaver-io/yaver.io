package main

import (
	"testing"
)

func TestClassifyMessage_TodoSignals(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"the login button is broken, add to queue", IntentTodo},
		{"queue this bug: cart total shows NaN", IntentTodo},
		{"not now but the header overlaps on small screens", IntentTodo},
		{"fix later: dark mode colors are wrong", IntentTodo},
		{"note this — signup form has no validation", IntentTodo},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_ActionSignals(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"hot reload the app", IntentAction},
		{"deploy to testflight", IntentAction},
		{"build the ios app", IntentAction},
		{"implement all the fixes", IntentAction},
		{"show me the error logs", IntentAction},
		{"restart the dev server", IntentAction},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_BugPatterns(t *testing.T) {
	tests := []struct {
		msg    string
		intent MessageIntent
	}{
		{"the checkout button doesn't work", IntentTodo},
		{"there's a crash when I tap profile", IntentTodo},
		{"this screen is broken on iphone", IntentTodo},
		{"the text is cut off on the settings page", IntentTodo},
		{"error when submitting the form", IntentTodo},
	}
	for _, tt := range tests {
		result := ClassifyMessage(tt.msg, nil)
		if result.Intent != tt.intent {
			t.Errorf("ClassifyMessage(%q) = %s, want %s", tt.msg, result.Intent, tt.intent)
		}
	}
}

func TestClassifyMessage_Continuation(t *testing.T) {
	items := []*TodoItem{
		{ID: "abc123", Description: "Login form broken", Status: TodoStatusPending, CreatedAt: "2026-03-25T10:00:00Z"},
	}

	result := ClassifyMessage("also the password field is too short", items)
	if result.Intent != IntentContinuation {
		t.Errorf("expected continuation, got %s", result.Intent)
	}
	if result.TodoID != "abc123" {
		t.Errorf("expected todoId abc123, got %s", result.TodoID)
	}
}

func TestClassifyMessage_DefaultAction(t *testing.T) {
	// Generic messages that don't match patterns default to action
	result := ClassifyMessage("refactor the auth module", nil)
	if result.Intent != IntentAction {
		t.Errorf("expected action for generic request, got %s", result.Intent)
	}
}

func TestDetectProjectInfo(t *testing.T) {
	// Test with current directory (should at least get a name)
	info := DetectProjectInfo("/tmp")
	if info.Name != "tmp" {
		t.Errorf("expected name 'tmp', got %s", info.Name)
	}
}
