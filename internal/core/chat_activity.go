package core

import "time"

// ChatActivity preserves the display order of public model output and validated tool execution.
type ChatActivity struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Purpose string `json:"purpose,omitempty"`
	StepID  string `json:"step_id,omitempty"`
	Status  string `json:"status"`
}

// updateChatActivity replaces one growing item without exposing private provider packets.
func (engine *Engine) updateChatActivity(jobID string, item ChatActivity) {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	job := engine.jobs[jobID]
	if job == nil || item.Text == "" && item.StepID == "" {
		return
	}
	for i := range job.Activity {
		if job.Activity[i].ID == item.ID {
			job.Activity[i] = item
			engine.checkpointActivityLocked(job)
			return
		}
	}
	job.Activity = append(job.Activity, item)
	engine.checkpointActivityLocked(job)
}

// checkpointActivityLocked retains streamed reasoning even before the first answer token arrives.
func (engine *Engine) checkpointActivityLocked(job *Job) {
	if time.Since(job.checkpointedAt) < time.Second {
		return
	}
	job.checkpointedAt = time.Now()
	if err := engine.store.SaveRun(job.ID, job.ProjectID, storedJob(job)); err != nil {
		job.Error = "Unable to save a recovery checkpoint: " + err.Error()
	}
}
