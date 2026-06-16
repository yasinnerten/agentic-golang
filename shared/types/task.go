package types

import "time"

// TaskStatus represents the lifecycle of a task.
type TaskStatus string

const (
	TaskStatusPending          TaskStatus = "pending"
	TaskStatusRunning          TaskStatus = "running"
	TaskStatusWaitingUser      TaskStatus = "waiting_for_user"
	TaskStatusWaitingEvidence  TaskStatus = "waiting_for_evidence"
	TaskStatusWaitingTool      TaskStatus = "waiting_for_tool"
	TaskStatusWaitingSubagent  TaskStatus = "waiting_for_subagent"
	TaskStatusRetrying         TaskStatus = "retrying"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusCancelled        TaskStatus = "cancelled"
	TaskStatusTimedOut         TaskStatus = "timed_out"
	TaskStatusNeedsHumanReview TaskStatus = "needs_human_review"
)

// Task is a unit of work created from checklist items or user actions.
type Task struct {
	TaskID                 string     `json:"task_id"`
	TaskType               string     `json:"task_type"`
	Title                  string     `json:"title"`
	Description            string     `json:"description"`
	RelatedQuestionGroupID string     `json:"related_question_group_id"`
	RelatedChecklistItemID string     `json:"related_checklist_item_id"`
	RelatedRoutePathID     string     `json:"related_route_path_id"`
	AssignedTo             string     `json:"assigned_to"`
	TaskStatus             string     `json:"task_status"`
	Priority               int        `json:"priority"`
	DueDate                *time.Time `json:"due_date,omitempty"`
	CreatedByAgentID       string     `json:"created_by_agent_id"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// TaskCreateInput is used to create a new task.
type TaskCreateInput struct {
	TaskType               string `json:"task_type"`
	Title                  string `json:"title"`
	Description            string `json:"description"`
	RelatedQuestionGroupID string `json:"related_question_group_id"`
	RelatedChecklistItemID string `json:"related_checklist_item_id"`
	RelatedRoutePathID     string `json:"related_route_path_id"`
	Priority               int    `json:"priority"`
}
