package core

import (
	"fmt"
	"strings"
)

// ChapterReceipt reports one chapter's outcome inside a multi-chapter production run.
type ChapterReceipt struct {
	ChapterNumber uint32 `json:"chapter_number"`
	ChapterID     string `json:"chapter_id,omitempty"`
	Title         string `json:"title,omitempty"`
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	Calls         int    `json:"calls"`
}

// runChapterProduction is the single chapter-production capability behind every entry
// point: direct continue/rewrite tasks and the coordinator's writer tool. With count > 1
// it walks confirmed chapter outlines in order; each chapter is produced, checkpointed
// and saved on its own boundary, so a failure records a receipt and never discards the
// chapters that already finished.
func (engine *Engine) runChapterProduction(job Job, project *Project, selected SelectedModel, initial Prompt, mode string, count int, step agentStepEnvelope) (string, int, error) {
	if count <= 1 {
		return engine.runWritingTask(job, project, selected, initial, mode, writingStageCallBudget)
	}
	count = min(count, 10)
	scopeID := step.PlanID
	if scopeID == "" && job.WorkspaceContext != nil {
		scopeID = job.WorkspaceContext.ActivePlanID
	}
	plans, err := confirmedWritingPlans(project, scopeID)
	if err != nil {
		return "", 0, err
	}
	receipts := []ChapterReceipt{}
	totalCalls := 0
	lastNumber := uint32(0)
	if step.Chapter > 0 {
		lastNumber = step.Chapter - 1
	} else if chapter := findChapter(project, job.ChapterID); chapter != nil {
		// The writer entry may have ensured an unwritten chapter already; that chapter is
		// the first target, not a chapter to skip past.
		if strings.TrimSpace(chapter.Body) != "" {
			lastNumber = chapter.Number
		} else {
			lastNumber = chapter.Number - 1
		}
	}
	produced := 0
	for _, plan := range plans {
		if plan.StartChapter <= lastNumber || produced >= count {
			continue
		}
		if err := job.ctx.Err(); err != nil {
			return productionSummary(project, receipts, true), totalCalls, err
		}
		number := plan.StartChapter
		lastNumber = number
		alreadyWritten := false
		for _, chapter := range project.Chapters {
			if chapter.Number == number && strings.TrimSpace(chapter.Body) != "" {
				receipts = append(receipts, ChapterReceipt{ChapterNumber: number, ChapterID: chapter.ID, Title: chapter.Title, Status: "skipped", Summary: localText(project, "Already written; left unchanged.", "已有正文，未改动。")})
				alreadyWritten = true
				break
			}
		}
		if alreadyWritten {
			continue
		}
		produced++
		chapterJob := job
		engine.jobsMu.Lock()
		if active := engine.jobs[job.ID]; active != nil {
			active.ChapterID, active.Writing = "", nil
			chapterJob.ChapterID = ""
		}
		engine.jobsMu.Unlock()
		if err := engine.ensureWritingChapter(project, &chapterJob, agentStepEnvelope{PlanID: plan.ID, Chapter: number, Query: step.Query}); err != nil {
			receipts = append(receipts, ChapterReceipt{ChapterNumber: number, Status: "failed", Summary: err.Error()})
			engine.addStep(job.ID, "writer", "error", fmt.Sprintf("Chapter %d: %v", number, err))
			continue
		}
		chapter := findChapter(project, chapterJob.ChapterID)
		if chapter == nil {
			receipts = append(receipts, ChapterReceipt{ChapterNumber: number, Status: "failed", Summary: "Chapter not found after creation"})
			continue
		}
		if strings.TrimSpace(chapter.Body) != "" {
			produced--
			receipts = append(receipts, ChapterReceipt{ChapterNumber: number, ChapterID: chapter.ID, Title: chapter.Title, Status: "skipped", Summary: localText(project, "Already written; left unchanged.", "已有正文，未改动。")})
			engine.addStep(job.ID, "writer", "done", fmt.Sprintf("Chapter %d skipped: already written", number))
			continue
		}
		workspace := WorkspaceContext{}
		if job.WorkspaceContext != nil {
			workspace = *job.WorkspaceContext
		}
		workspace.ActivePlanID, workspace.ActiveChapterID = plan.ID, chapter.ID
		chapterJob.WorkspaceContext = &workspace
		scoped := scopedAgentRequest(project, chapterJob, agentStepEnvelope{Tool: "writer", Query: step.Query, Mode: mode, EvidenceIDs: step.EvidenceIDs})
		prompt, err := prepareAI(project, scoped, int(selected.Config.ContextChars))
		if err != nil {
			receipts = append(receipts, ChapterReceipt{ChapterNumber: number, ChapterID: chapter.ID, Title: chapter.Title, Status: "failed", Summary: err.Error()})
			engine.addStep(job.ID, "writer", "error", fmt.Sprintf("Chapter %d: %v", number, err))
			continue
		}
		prompt.Skills = job.Prompt.Skills
		text, used, err := engine.runWritingTask(chapterJob, project, selected, prompt, mode, writingStageCallBudget)
		totalCalls += used
		receipt := ChapterReceipt{ChapterNumber: number, ChapterID: chapter.ID, Title: chapter.Title, Calls: used}
		if err != nil {
			receipt.Status, receipt.Summary = "failed", err.Error()
			engine.addStep(job.ID, "writer", "error", fmt.Sprintf("Chapter %d: %v", number, err))
		} else {
			outcome := engine.writingOutcome(job.ID)
			receipt.Status, receipt.Summary = "done", writingResultSummary(project, outcome)
			if outcome == nil || outcome.ReviewStatus != "complete" || !outcome.Saved {
				receipt.Status = "needs_review"
			}
			if charCount(text) > 200 {
				text = string([]rune(text)[:200])
			}
			receipt.Summary += " " + text
			engine.addStep(job.ID, "writer", "done", fmt.Sprintf("Chapter %d produced", number))
		}
		receipts = append(receipts, receipt)
		if receipt.Status == "needs_review" {
			receipts = append(receipts, ChapterReceipt{ChapterNumber: number, Status: "stopped", Summary: fmt.Sprintf(localText(project, "The batch paused for author review. %d requested chapters remain.", "本批次暂停，等待作者审阅，还剩 %d 章未生成。"), count-produced)})
			return productionSummary(project, receipts, false), totalCalls, nil
		}
	}
	if produced < count {
		receipts = append(receipts, ChapterReceipt{ChapterNumber: lastNumber, Status: "stopped", Summary: fmt.Sprintf(localText(project, "The selected scope has no more confirmed chapter outlines. %d requested chapters remain.", "所选范围内没有更多已确认章纲，还剩 %d 章未生成。"), count-produced)})
	}
	return productionSummary(project, receipts, false), totalCalls, nil
}

// productionSummary renders receipts as the author-facing result of a batch run.
func productionSummary(project *Project, receipts []ChapterReceipt, cancelled bool) string {
	lines := []string{}
	if cancelled {
		lines = append(lines, localText(project, "Multi-chapter production was cancelled. Chapters already saved are kept:", "多章生成已取消，已保存的章节保留："))
	} else {
		lines = append(lines, localText(project, "Multi-chapter production finished:", "多章生成结束："))
	}
	for _, receipt := range receipts {
		status := map[string]string{"done": "✓", "needs_review": "!", "failed": "✗", "skipped": "→", "stopped": "·"}[receipt.Status]
		if status == "" {
			status = "·"
		}
		summary := receipt.Summary
		if charCount(summary) > 160 {
			summary = string([]rune(summary)[:160])
		}
		lines = append(lines, fmt.Sprintf("%s Chapter %d %s — %s", status, receipt.ChapterNumber, receipt.Title, summary))
	}
	return strings.Join(lines, "\n")
}
