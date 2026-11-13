package core

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// commitPolicyInstruction carries the author's current save choice outside the stable system prefix.
func commitPolicyInstruction(project *Project, policy string) string {
	if policy == "auto" {
		return localText(project,
			"Output save policy: auto. The author requests automatic saving of the story products requested in this turn. Use structured proposal tools for outlines, settings and other records; the application validates and saves them with a reversible receipt. Finish requested prose through the writing workflow. Preserve discussed alternatives as alternatives, and keep inferred chapter memories source-linked. Report only changes actually saved in the tool results.",
			"本轮成果保存方式为自动保存。作者授权将本轮要求创作的大纲、章节与设定写入作品。大纲和设定等记录用结构化提案工具提交，由应用校验并保存，生成可撤销回执。正文经写作流程完成。讨论中的备选方案保留方案身份，章后推断保留来源。回执只描述工具实际保存的内容。")
	}
	return localText(project,
		"Output save policy: review. Keep structured changes and prose as candidates for the author's review. Discussed plans and interpretations retain their draft identity.",
		"本轮成果保存方式为审阅后保存。结构化修改与正文保留为待审候选，讨论中的规划和解释保留草案身份。")
}

// existingWritingComposition reuses whole-chapter cards only before drafting or when replacing a chapter.
func existingWritingComposition(project *Project, job Job, mode string) *WritingComposition {
	chapter := findChapter(project, job.ChapterID)
	if chapter == nil {
		return nil
	}
	if mode == "append" && (strings.TrimSpace(chapter.Body) != "" || job.Action != "agent" && strings.TrimSpace(job.Text) != "") {
		return nil
	}
	for _, plan := range planningPath(project, chapter, job.WorkspaceContext) {
		if plan.Level != "chapter" || plan.Status != "confirmed" || plan.StartChapter != chapter.Number {
			continue
		}
		intent := strings.TrimSpace(plan.Goal)
		if intent == "" {
			intent = job.Instruction
		}
		composition := &WritingComposition{Intent: intent, Approach: planSynopsis(project, plan), SceneCards: plan.SceneCards, OpenQuestions: plan.OpenQuestions}
		if completeWritingComposition(composition) {
			return copyWritingComposition(composition)
		}
	}
	return nil
}

// confirmedWritingPlans freezes the confirmed chapter outlines inside one explicit planning scope.
func confirmedWritingPlans(project *Project, planID string) ([]StoryPlan, error) {
	plans := append([]StoryPlan{}, project.Studio.Plans...)
	byID := map[string]StoryPlan{}
	confirmed := map[uint32]int{}
	for _, plan := range plans {
		byID[plan.ID] = plan
		if plan.Level == "chapter" && plan.Status == "confirmed" {
			confirmed[plan.StartChapter]++
		}
	}
	if planID != "" {
		if _, exists := byID[planID]; !exists {
			return nil, errors.New("Choose a confirmed chapter outline before writing a new chapter")
		}
	}
	selected := []StoryPlan{}
	for _, plan := range plans {
		if plan.Level != "chapter" || plan.Status != "confirmed" || plan.StartChapter == 0 {
			continue
		}
		matches := planID == ""
		for ancestor, depth := plan, 0; !matches && depth <= len(plans); depth++ {
			matches = ancestor.ID == planID
			if ancestor.ParentID == "" {
				break
			}
			ancestor = byID[ancestor.ParentID]
		}
		if !matches {
			continue
		}
		if confirmed[plan.StartChapter] > 1 {
			return nil, errors.New("This chapter has multiple confirmed outlines. Keep one confirmed outline before writing")
		}
		selected = append(selected, plan)
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].StartChapter < selected[j].StartChapter })
	return selected, nil
}

// resolveWritingPlan selects an explicit or focused confirmed chapter plan, then the earliest unwritten chapter.
func resolveWritingPlan(project *Project, job Job, step agentStepEnvelope) (*StoryPlan, error) {
	planID := step.PlanID
	if planID == "" && step.Chapter == 0 && job.WorkspaceContext != nil {
		planID = job.WorkspaceContext.ActivePlanID
	}
	plans, err := confirmedWritingPlans(project, planID)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		if step.Chapter > 0 && plan.StartChapter != step.Chapter {
			continue
		}
		written := false
		for _, chapter := range project.Chapters {
			written = written || chapter.Number == plan.StartChapter && strings.TrimSpace(chapter.Body) != ""
		}
		if !written || plan.ID == planID || step.Chapter > 0 {
			return &plan, nil
		}
	}
	return nil, errors.New("Choose a confirmed chapter outline before writing a new chapter")
}

// ensureWritingChapter creates the outline-backed target once and durably binds retries to that chapter.
func (engine *Engine) ensureWritingChapter(project *Project, job *Job, step agentStepEnvelope) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	active := engine.jobs[job.ID]
	if active == nil || job.ctx.Err() != nil {
		return errors.New("Task cancelled")
	}
	copy := active.snapshot()
	target := step.ChapterID
	if target == "" && step.PlanID == "" && step.Chapter == 0 {
		if job.WorkspaceContext == nil || job.WorkspaceContext.ActivePlanID == "" {
			target = job.ChapterID
		}
		if copy.Writing != nil && copy.Writing.TargetChapterID != "" {
			target = copy.Writing.TargetChapterID
		}
	}
	var updated Project
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		var err error
		updated, err = readProject(db, project.ID)
		if err != nil {
			return err
		}
		chapter := findChapter(&updated, target)
		if target != "" && chapter == nil {
			return errors.New("Select a chapter from this project")
		}
		autoWrite := job.CommitPolicy == "auto"
		if chapter == nil {
			plan, err := resolveWritingPlan(&updated, *job, step)
			if err != nil {
				return err
			}
			for i := range updated.Chapters {
				if updated.Chapters[i].Number == plan.StartChapter {
					chapter = &updated.Chapters[i]
					break
				}
			}
			if chapter == nil {
				created := NewChapter(plan.StartChapter, plan.Title)
				created.Synopsis = planSynopsis(&updated, *plan)
				if plan.TargetCharacters > 0 {
					words := plan.TargetCharacters
					created.TargetWords = &words
				}
				updated.Chapters = append(updated.Chapters, created)
				sortChapters(&updated)
				chapter = findChapter(&updated, created.ID)
				if err := saveProject(db, &updated, updated.Revision); err != nil {
					return err
				}
				copy.BaseRevision = updated.Revision
			}
		}
		if copy.ChapterID != chapter.ID && (copy.Writing == nil || copy.Writing.TargetChapterID != chapter.ID) {
			copy.Writing = nil
		}
		// A new writer call after a completed-but-unaccepted candidate may follow a
		// repaired outline. Retain the old changeset, but rebuild its stale context.
		if copy.Writing != nil && copy.Writing.Phase == "needs_review" && copy.Writing.SourcePrompt != nil {
			if err := validateWritingSnapshot(&updated, *copy.Writing.SourcePrompt); err != nil {
				copy.Writing = nil
			}
		}
		copy.ChapterID = chapter.ID
		if autoWrite {
			if copy.Writing == nil {
				copy.Writing = &WritingRun{Mode: "append", Phase: "brief"}
			}
			copy.Writing.AutoWrite, copy.Writing.TargetChapterID = true, chapter.ID
		}
		if copy.Writing != nil {
			copy.Writing.AutoWrite = autoWrite
		}
		_, err = db.ExecContext(background(), "UPDATE runs SET document=? WHERE id=? AND project_id=?", storedJob(&copy), job.ID, project.ID)
		return err
	})
	if err != nil {
		return err
	}
	updated.requestLanguage, updated.taskLanguage = project.requestLanguage, project.taskLanguage
	*project = updated
	active.ChapterID, active.Writing, active.BaseRevision = copy.ChapterID, copyWritingRun(copy.Writing), copy.BaseRevision
	job.ChapterID, job.Writing, job.BaseRevision = active.ChapterID, copyWritingRun(active.Writing), active.BaseRevision
	return nil
}

// saveAutomaticChapter atomically adopts reviewed prose, scene cards and source-linked deltas with an undoable receipt.
func (engine *Engine) saveAutomaticChapter(job Job, project *Project, run *WritingRun) error {
	if job.CommitPolicy != "auto" {
		return nil
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	active := engine.jobs[job.ID]
	if active == nil || job.ctx.Err() != nil {
		return errors.New("Task cancelled")
	}
	copy := active.snapshot()
	if copy.Writing != nil && copy.Writing.Saved {
		run.Saved = true
		return nil
	}
	if len(copy.ChangeSets) == 0 {
		return errors.New("Unable to stage the writing candidate. The draft is retained in the task")
	}
	cs := copy.ChangeSets[len(copy.ChangeSets)-1]
	cs.Operations = append([]ChangeOperation{}, cs.Operations...)
	if run.SourcePrompt == nil || run.ReviewStatus != "complete" {
		return errors.New("The writing candidate needs a completed source-checked review before automatic saving")
	}
	var updated Project
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		var err error
		updated, err = readProject(db, project.ID)
		if err != nil {
			return err
		}
		// A published receipt makes a checkpoint retry a readback, even after later author edits.
		if published := findChangeSet(&updated, cs.ID); published != nil && published.Status == ChangeSetApplied {
			if published.JobID != job.ID {
				return errors.New("The writing receipt belongs to another task")
			}
			setJobChangeSet(&copy, *published)
			copy.Writing.Saved = true
			_, err = db.ExecContext(background(), "UPDATE runs SET document=? WHERE id=? AND project_id=?", storedJob(&copy), job.ID, project.ID)
			return err
		}
		if updated.Revision != project.Revision || findChapter(&updated, job.ChapterID) == nil {
			return errors.New("The story changed during writing. Rebuild this chapter's context before continuing")
		}
		if err := validateWritingSnapshot(&updated, *run.SourcePrompt); err != nil {
			return err
		}
		chapterOperation := false
		for _, operation := range cs.Operations {
			if operation.TargetKind == TargetChapter && operation.TargetID == job.ChapterID {
				var patch chapterProsePatch
				if err := decodeJSON(operation.AfterValue, &patch); err != nil || patch.Body != run.Draft {
					return errors.New("The staged prose differs from the reviewed writing candidate")
				}
				chapterOperation = true
			}
		}
		if !chapterOperation {
			return errors.New("The writing changeset has no matching chapter operation")
		}
		cs.BaseRevision = updated.Revision
		cs.CommitPolicy = "auto"
		if err := applyChangeSetToProject(&updated, &cs); err != nil {
			return err
		}
		if chapter := findChapter(&updated, job.ChapterID); chapter == nil || hashText(chapter.Body) != run.CandidateHash {
			return errors.New("The saved prose boundary differs from the reviewed writing candidate")
		}
		cs.AppliedRevision = updated.Revision + 1
		upsertChangeSetInProject(&updated, cs)
		if err := saveProject(db, &updated, updated.Revision); err != nil {
			return err
		}
		if err := writeChangeSetRecord(db, project.ID, cs); err != nil {
			return err
		}
		setJobChangeSet(&copy, cs)
		for i := range copy.Proposals {
			for _, op := range cs.Operations {
				if copy.Proposals[i].ID == op.ProposalID {
					copy.Proposals[i].Status = "accepted"
				}
			}
		}
		copy.Writing.Saved = true
		copy.BaseRevision = updated.Revision
		copy.Progress = fmt.Sprintf(localText(project, "Chapter saved at revision %d; the changeset receipt supports undo.", "章节已保存至版本 %d，可通过变更回执撤销。"), updated.Revision)
		_, err = db.ExecContext(background(), "UPDATE runs SET document=? WHERE id=? AND project_id=?", storedJob(&copy), job.ID, project.ID)
		return err
	})
	if err != nil {
		return err
	}
	updated.requestLanguage, updated.taskLanguage = project.requestLanguage, project.taskLanguage
	*project = updated
	active.Proposals, active.ChangeSets, active.Writing, active.Progress, active.BaseRevision = copy.Proposals, copy.ChangeSets, copy.Writing, copy.Progress, copy.BaseRevision
	run.Saved = true
	return nil
}
